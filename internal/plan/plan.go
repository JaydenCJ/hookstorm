// Package plan turns a storm configuration and a seed into a concrete,
// fully-deterministic list of deliveries. This is the pure heart of
// hookstorm: no network, no clock, no goroutines — just "given these knobs
// and this seed, here is the exact sequence of HTTP requests we are about to
// make." Because it is deterministic, a failing storm is reproducible from
// its seed alone, and the whole thing is unit-testable without a server.
//
// The faults it models are the ones that break real webhook handlers:
//
//	duplicates  the same event delivered more than once (at-least-once)
//	reordering  deliveries shuffled within a bounded window (out-of-order)
//	slow        each delivery carries a scheduled jitter delay
//	bad sigs    some deliveries get a wrong-key, tampered, or missing signature
//
// Retries (resending after a 5xx) are not planned here — they are a reaction
// to the handler's behaviour and so live in the deliver package.
package plan

import (
	"fmt"

	"github.com/JaydenCJ/hookstorm/internal/event"
	"github.com/JaydenCJ/hookstorm/internal/rng"
)

// SigMode is how a single delivery is signed.
type SigMode string

const (
	// SigValid is a correct signature over the real body. A correct handler
	// must accept these (and, after de-duplication, process them exactly once).
	SigValid SigMode = "valid"
	// SigWrongKey is a well-formed signature computed with the wrong secret —
	// the classic "someone rotated the signing key" or spoofing case.
	SigWrongKey SigMode = "wrong-key"
	// SigTampered is a correct-looking signature over a body that was then
	// modified in flight — the payload-tampering attack signatures exist to stop.
	SigTampered SigMode = "tampered"
	// SigMissing omits the signature header entirely. A handler that requires
	// signatures must reject these too.
	SigMissing SigMode = "missing"
)

// Bad reports whether a mode is one a correct handler must reject.
func (m SigMode) Bad() bool { return m != SigValid }

// Delivery is one HTTP request the storm will make. DeliveryID is assigned in
// creation order and is stable across retries (a retried delivery reuses it,
// exactly like a provider's delivery UUID); Seq is the position on the wire
// after reordering.
type Delivery struct {
	Seq        int     `json:"seq"`
	DeliveryID string  `json:"delivery_id"`
	EventID    string  `json:"event_id"`
	EventSeq   int     `json:"event_seq"`
	EventType  string  `json:"event_type"`
	Duplicate  bool    `json:"duplicate"`
	Sig        SigMode `json:"signature"`
	DelayMs    int     `json:"delay_ms"`
}

// Config is the set of storm knobs. The zero value is a valid, benign storm
// (no faults); Validate fills in sensible defaults where 0 means "off" is not
// the intent.
type Config struct {
	Events        int     `json:"events"`
	Seed          uint64  `json:"seed"`
	DuplicateRate float64 `json:"duplicate_rate"` // P(an event gets extra deliveries)
	MaxDuplicates int     `json:"max_duplicates"` // cap on extra deliveries per event
	BadSigRate    float64 `json:"bad_sig_rate"`   // fraction of deliveries signed wrong
	MissingRate   float64 `json:"missing_rate"`   // of the bad ones, fraction with no header
	ReorderWindow int     `json:"reorder_window"` // shuffle window (<=1 keeps order)
	MaxDelayMs    int     `json:"max_delay_ms"`   // upper bound on per-delivery jitter
}

// Plan is a built storm: the config it came from, the logical events, and the
// concrete deliveries in wire order.
type Plan struct {
	Config     Config        `json:"config"`
	Events     []event.Event `json:"events"`
	Deliveries []Delivery    `json:"deliveries"`
}

// Validate checks the rates and counts and returns a normalized copy. Rates
// must be in [0,1]; counts must be non-negative. MaxDuplicates defaults to 1
// when duplicates are enabled but no cap was given.
func (c Config) Validate() (Config, error) {
	if c.Events < 0 {
		return c, fmt.Errorf("events must be >= 0, got %d", c.Events)
	}
	for _, r := range []struct {
		name string
		v    float64
	}{
		{"duplicate-rate", c.DuplicateRate},
		{"bad-sig-rate", c.BadSigRate},
		{"missing-rate", c.MissingRate},
	} {
		if r.v < 0 || r.v > 1 {
			return c, fmt.Errorf("%s must be in [0,1], got %g", r.name, r.v)
		}
	}
	if c.MaxDuplicates < 0 {
		return c, fmt.Errorf("max-duplicates must be >= 0, got %d", c.MaxDuplicates)
	}
	if c.ReorderWindow < 0 {
		return c, fmt.Errorf("reorder-window must be >= 0, got %d", c.ReorderWindow)
	}
	if c.MaxDelayMs < 0 {
		return c, fmt.Errorf("max-delay-ms must be >= 0, got %d", c.MaxDelayMs)
	}
	if c.DuplicateRate > 0 && c.MaxDuplicates == 0 {
		c.MaxDuplicates = 1
	}
	return c, nil
}

// Build constructs the deterministic delivery plan for a config. The same
// config (seed included) always yields byte-identical deliveries.
func Build(c Config) (Plan, error) {
	c, err := c.Validate()
	if err != nil {
		return Plan{}, err
	}
	events := event.Generate(c.Events)

	// Independent sub-streams per concern, so tuning one fault does not shift
	// the draws every other fault sees. The fork order is part of the format.
	root := rng.New(c.Seed)
	dupRNG := root.Fork()
	sigRNG := root.Fork()
	delayRNG := root.Fork()
	orderRNG := root.Fork()

	var deliveries []Delivery
	next := 0
	for _, e := range events {
		copies := 1
		if dupRNG.Chance(c.DuplicateRate) && c.MaxDuplicates > 0 {
			copies += 1 + dupRNG.Intn(c.MaxDuplicates) // 2..(1+MaxDuplicates) total
		}
		for k := 0; k < copies; k++ {
			next++
			deliveries = append(deliveries, Delivery{
				DeliveryID: fmt.Sprintf("dlv_%05d", next),
				EventID:    e.ID,
				EventSeq:   e.Sequence,
				EventType:  e.Type,
				Duplicate:  k > 0,
				Sig:        SigValid,
			})
		}
	}

	// Signature modes. A bad delivery is missing with probability MissingRate,
	// otherwise split evenly between a wrong key and a tampered body.
	for i := range deliveries {
		if !sigRNG.Chance(c.BadSigRate) {
			continue
		}
		switch {
		case sigRNG.Chance(c.MissingRate):
			deliveries[i].Sig = SigMissing
		case sigRNG.Chance(0.5):
			deliveries[i].Sig = SigWrongKey
		default:
			deliveries[i].Sig = SigTampered
		}
	}

	// Slow-delivery jitter.
	if c.MaxDelayMs > 0 {
		for i := range deliveries {
			deliveries[i].DelayMs = delayRNG.Intn(c.MaxDelayMs + 1)
		}
	}

	// Reordering: shuffle within consecutive windows so events arrive
	// out-of-order but not arbitrarily far from their true position.
	reorder(deliveries, c.ReorderWindow, orderRNG)

	for i := range deliveries {
		deliveries[i].Seq = i
	}
	return Plan{Config: c, Events: events, Deliveries: deliveries}, nil
}

// reorder shuffles the slice within consecutive blocks of size window. A
// window of 0 or 1 leaves the order untouched; a window >= len is a full
// shuffle. Bounded, local reordering models what really happens when a
// provider delivers in parallel with limited concurrency.
func reorder(ds []Delivery, window int, r *rng.RNG) {
	if window <= 1 {
		return
	}
	for start := 0; start < len(ds); start += window {
		end := start + window
		if end > len(ds) {
			end = len(ds)
		}
		block := ds[start:end]
		r.Shuffle(len(block), func(i, j int) {
			block[i], block[j] = block[j], block[i]
		})
	}
}

// Summary is a compact tally of what a plan will do, for reports and the
// `plan` command's header.
type Summary struct {
	Events       int `json:"events"`
	Deliveries   int `json:"deliveries"`
	Duplicates   int `json:"duplicates"`
	ValidSigs    int `json:"valid_sigs"`
	WrongKeySigs int `json:"wrong_key_sigs"`
	TamperedSigs int `json:"tampered_sigs"`
	MissingSigs  int `json:"missing_sigs"`
	Delayed      int `json:"delayed"`
}

// Summarize counts the deliveries by category.
func (p Plan) Summarize() Summary {
	s := Summary{Events: len(p.Events), Deliveries: len(p.Deliveries)}
	for _, d := range p.Deliveries {
		if d.Duplicate {
			s.Duplicates++
		}
		if d.DelayMs > 0 {
			s.Delayed++
		}
		switch d.Sig {
		case SigValid:
			s.ValidSigs++
		case SigWrongKey:
			s.WrongKeySigs++
		case SigTampered:
			s.TamperedSigs++
		case SigMissing:
			s.MissingSigs++
		}
	}
	return s
}

// ValidlyDelivered returns the set of event IDs that received at least one
// correctly-signed delivery. These are exactly the events a correct handler
// is expected to process (once); everything else it must reject. The analyzer
// uses this to judge idempotency, loss, and spurious processing.
func (p Plan) ValidlyDelivered() map[string]bool {
	set := make(map[string]bool)
	for _, d := range p.Deliveries {
		if d.Sig == SigValid {
			set[d.EventID] = true
		}
	}
	return set
}
