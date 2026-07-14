// Package deliver executes a plan against a live handler over HTTP. It is the
// one place in hookstorm that touches the network, a clock, and goroutines;
// everything else is pure. It signs each request exactly as a provider would
// (or deliberately wrong, per the delivery's SigMode), fires them through a
// bounded worker pool, retries 5xx and transport failures the way a real
// sender does, and records a structured Outcome per delivery for the analyzer
// to judge.
package deliver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/JaydenCJ/hookstorm/internal/plan"
	"github.com/JaydenCJ/hookstorm/internal/sign"
	"github.com/JaydenCJ/hookstorm/internal/whproto"
)

// Outcome is the observed result of one delivery, after any retries.
type Outcome struct {
	Seq        int          `json:"seq"`
	DeliveryID string       `json:"delivery_id"`
	EventID    string       `json:"event_id"`
	Sig        plan.SigMode `json:"signature"`
	Duplicate  bool         `json:"duplicate"`
	Status     int          `json:"status"` // final HTTP status, 0 on transport failure
	Attempts   int          `json:"attempts"`
	Err        string       `json:"error,omitempty"`
	LatencyMs  int64        `json:"latency_ms"`
}

// Accepted reports a 2xx final status.
func (o Outcome) Accepted() bool { return o.Status >= 200 && o.Status < 300 }

// Rejected reports a 4xx final status — the correct response to a bad
// signature.
func (o Outcome) Rejected() bool { return o.Status >= 400 && o.Status < 500 }

// ServerError reports a 5xx final status: the handler fell over.
func (o Outcome) ServerError() bool { return o.Status >= 500 }

// Failed reports a transport-level failure (connection refused, timeout): no
// HTTP status was ever returned.
func (o Outcome) Failed() bool { return o.Status == 0 }

// Options configure a run. The zero value is unusable (Target is required);
// New defaults fill in a client, sleeper, and clock so tests can inject
// deterministic ones.
type Options struct {
	Target      string
	Secret      []byte
	Scheme      sign.Scheme
	Concurrency int
	MaxRetries  int
	Timeout     time.Duration

	// Injectables. Left nil, they default to real implementations. Tests set
	// Sleep to a no-op and Now to a fixed clock to stay off the wall clock.
	Client *http.Client
	Sleep  func(time.Duration)
	Now    func() time.Time
}

func (o *Options) applyDefaults() {
	if o.Concurrency < 1 {
		o.Concurrency = 1
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 0
	}
	if o.Client == nil {
		timeout := o.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		o.Client = &http.Client{Timeout: timeout}
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Scheme == "" {
		o.Scheme = sign.GitHub
	}
}

// Run executes the plan and returns one Outcome per delivery, indexed by the
// delivery's Seq (so the slice is in wire order regardless of concurrency).
func Run(ctx context.Context, p plan.Plan, opts Options) ([]Outcome, error) {
	if opts.Target == "" {
		return nil, fmt.Errorf("deliver: target URL is required")
	}
	opts.applyDefaults()

	bodies := make(map[string][]byte, len(p.Events))
	for _, e := range p.Events {
		bodies[e.ID] = e.Body()
	}

	outcomes := make([]Outcome, len(p.Deliveries))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < opts.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				outcomes[i] = deliverOne(ctx, p.Deliveries[i], bodies[p.Deliveries[i].EventID], opts)
			}
		}()
	}
	for i := range p.Deliveries {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return outcomes, ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	return outcomes, nil
}

// deliverOne sends a single delivery, retrying on 5xx and transport errors up
// to MaxRetries, and returns its final Outcome.
func deliverOne(ctx context.Context, d plan.Delivery, body []byte, opts Options) Outcome {
	out := Outcome{
		Seq:        d.Seq,
		DeliveryID: d.DeliveryID,
		EventID:    d.EventID,
		Sig:        d.Sig,
		Duplicate:  d.Duplicate,
	}
	if d.DelayMs > 0 {
		opts.Sleep(time.Duration(d.DelayMs) * time.Millisecond)
	}
	start := opts.Now()
	for attempt := 1; ; attempt++ {
		out.Attempts = attempt
		status, err := send(ctx, d, body, attempt, opts)
		if err != nil {
			out.Status = 0
			out.Err = err.Error()
		} else {
			out.Status = status
			out.Err = ""
		}
		retryable := err != nil || status >= 500
		if !retryable || attempt > opts.MaxRetries {
			break
		}
		// Linear backoff; a no-op sleeper makes this free in tests.
		opts.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
	out.LatencyMs = opts.Now().Sub(start).Milliseconds()
	return out
}

// send performs one HTTP attempt and returns the status code, or an error for
// a transport-level failure.
func send(ctx context.Context, d plan.Delivery, body []byte, attempt int, opts Options) (int, error) {
	sendBody := body
	secret := opts.Secret
	ts := opts.Now().Unix()

	// SigTampered modifies the body after signing; the others change the key
	// or omit the header. This is where an abstract SigMode becomes a concrete
	// broken request.
	if d.Sig == plan.SigTampered {
		sendBody = tamper(body)
	}
	if d.Sig == plan.SigWrongKey {
		secret = wrongKey(opts.Secret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Target, bytes.NewReader(sendBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(whproto.HeaderEvent, d.EventID)
	req.Header.Set(whproto.HeaderDelivery, d.DeliveryID)
	req.Header.Set(whproto.HeaderEventType, d.EventType)
	req.Header.Set(whproto.HeaderAttempt, fmt.Sprintf("%d", attempt))

	if d.Sig != plan.SigMissing {
		name, value, err := sign.Compute(opts.Scheme, secret, body, ts)
		if err != nil {
			return 0, err
		}
		req.Header.Set(name, value)
	}

	resp, err := opts.Client.Do(req)
	if err != nil {
		return 0, err
	}
	// Drain and close so the connection can be reused across the pool.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// tamper returns a copy of body with its last byte perturbed, so the signature
// computed over the original no longer matches. Length is preserved; the exact
// mutation does not matter because a hardened handler rejects on the signature
// before it ever parses the body.
func tamper(body []byte) []byte {
	if len(body) == 0 {
		return []byte{0}
	}
	out := make([]byte, len(body))
	copy(out, body)
	out[len(out)-1] ^= 0x01
	return out
}

// wrongKey returns a secret guaranteed to differ from the real one, modelling
// a rotated or spoofed signing key.
func wrongKey(secret []byte) []byte {
	out := make([]byte, len(secret)+1)
	copy(out, secret)
	out[len(secret)] = '!'
	return out
}
