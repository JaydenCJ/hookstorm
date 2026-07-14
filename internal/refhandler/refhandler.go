// Package refhandler is a small, configurable webhook receiver used as a
// target for storms — by the test suite, the examples, and scripts/smoke.sh.
// Its whole reason to exist is to have known-good and known-buggy handlers to
// aim hookstorm at, so the tool's verdicts can be checked against ground
// truth. A Mode flips individual real-world bugs on:
//
//	VerifySignature=false   accepts any signature (the bypass bug)
//	Idempotent=false        processes every accepted delivery (double-processing)
//	FailFirstAttempt=true   500s the first time it sees a delivery (flaky, retryable)
//	DropEvenSeq=true        200s but silently drops even-sequence events (loss)
//
// The default Mode (all zero except VerifySignature and Idempotent set true via
// Correct) is a handler that does everything right.
package refhandler

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/JaydenCJ/hookstorm/internal/sign"
	"github.com/JaydenCJ/hookstorm/internal/whproto"
)

// Mode selects which behaviours (correct or buggy) the handler exhibits.
type Mode struct {
	VerifySignature  bool
	Idempotent       bool
	FailFirstAttempt bool
	DropEvenSeq      bool
}

// Correct returns the Mode of a handler that does everything right: verifies
// signatures and de-duplicates by event id.
func Correct() Mode {
	return Mode{VerifySignature: true, Idempotent: true}
}

// Handler is a stateful in-memory webhook receiver. It is safe for concurrent
// use, so a storm may hit it from many workers at once.
type Handler struct {
	secret []byte
	scheme sign.Scheme
	mode   Mode

	mu        sync.Mutex
	processed map[string]int  // event id -> times committed
	seenDlv   map[string]bool // delivery id -> already attempted once
}

// New builds a handler that verifies against secret/scheme and behaves per
// mode.
func New(secret []byte, scheme sign.Scheme, mode Mode) *Handler {
	return &Handler{
		secret:    secret,
		scheme:    scheme,
		mode:      mode,
		processed: make(map[string]int),
		seenDlv:   make(map[string]bool),
	}
}

// ServeHTTP routes POST /webhook (receive a delivery) and GET /receipts
// (report what was processed). Anything else is 404.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/webhook":
		h.receive(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/receipts":
		h.serveReceipts(w)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	// Signature gate. A correct handler rejects anything that does not verify,
	// before it parses or acts on the body.
	if h.mode.VerifySignature {
		header := r.Header.Get(sign.Header(h.scheme))
		if !sign.Verify(h.scheme, h.secret, body, header) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	eventID := r.Header.Get(whproto.HeaderEvent)
	deliveryID := r.Header.Get(whproto.HeaderDelivery)

	// Flaky mode: fail the first time a given delivery is attempted, so a
	// retrying sender must try again. This is transient, not a rejection.
	if h.mode.FailFirstAttempt {
		h.mu.Lock()
		firstTime := !h.seenDlv[deliveryID]
		h.seenDlv[deliveryID] = true
		h.mu.Unlock()
		if firstTime {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
	}

	// Lossy mode: acknowledge but silently drop even-sequence events. The
	// sender sees a 2xx and moves on, but the event is never committed.
	if h.mode.DropEvenSeq && seqIsEven(body) {
		w.WriteHeader(http.StatusOK)
		return
	}

	h.commit(eventID)
	w.WriteHeader(http.StatusOK)
}

// commit records that the handler acted on an event. An idempotent handler
// records each event id at most once; a buggy one records every accepted
// delivery.
func (h *Handler) commit(eventID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mode.Idempotent && h.processed[eventID] > 0 {
		return
	}
	h.processed[eventID]++
}

// receiptEntry / receiptsBody are the wire shape of GET /receipts. hookstorm
// decodes this into analyze.Receipts; the field tags must match.
type receiptEntry struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

type receiptsBody struct {
	Processed []receiptEntry `json:"processed"`
}

func (h *Handler) serveReceipts(w http.ResponseWriter) {
	body := receiptsBody{Processed: h.snapshot()}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// snapshot returns the processed counts, sorted by id for deterministic output.
func (h *Handler) snapshot() []receiptEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]receiptEntry, 0, len(h.processed))
	for id, n := range h.processed {
		out = append(out, receiptEntry{ID: id, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Processed returns a copy of the internal processed counts, for tests that
// want ground truth without going through the HTTP receipts endpoint.
func (h *Handler) Processed() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]int, len(h.processed))
	for k, v := range h.processed {
		out[k] = v
	}
	return out
}

// seqIsEven parses the body's "sequence" field and reports whether it is even.
// A parse failure is treated as odd (i.e. not dropped), so malformed bodies
// are never silently lost by the lossy mode.
func seqIsEven(body []byte) bool {
	var payload struct {
		Sequence json.Number `json:"sequence"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	n, err := strconv.Atoi(payload.Sequence.String())
	if err != nil {
		return false
	}
	return n%2 == 0
}
