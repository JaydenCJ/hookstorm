// Integration tests for the executor. They run real HTTP against an in-process
// httptest server bound to loopback — no external network, no wall-clock sleeps
// (the sleeper is a no-op), and every assertion is on counts, statuses, or
// set membership, never on timing. The target handler is the bundled
// refhandler, so behaviour is ground truth.
package deliver

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/JaydenCJ/hookstorm/internal/plan"
	"github.com/JaydenCJ/hookstorm/internal/refhandler"
	"github.com/JaydenCJ/hookstorm/internal/sign"
)

const testSecret = "whsec_test"

func serve(t *testing.T, mode refhandler.Mode) (string, *refhandler.Handler) {
	t.Helper()
	h := refhandler.New([]byte(testSecret), sign.GitHub, mode)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + "/webhook", h
}

func opts(target string, mut func(*Options)) Options {
	o := Options{
		Target:      target,
		Secret:      []byte(testSecret),
		Scheme:      sign.GitHub,
		Concurrency: 1,
		MaxRetries:  0,
		Sleep:       func(time.Duration) {}, // never touch the wall clock in tests
	}
	if mut != nil {
		mut(&o)
	}
	return o
}

func run(t *testing.T, p plan.Plan, o Options) []Outcome {
	t.Helper()
	outs, err := Run(context.Background(), p, o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return outs
}

func build(t *testing.T, c plan.Config) plan.Plan {
	t.Helper()
	p, err := plan.Build(c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidDeliveryAccepted(t *testing.T) {
	target, _ := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 1, Seed: 1})
	outs := run(t, p, opts(target, nil))
	if len(outs) != 1 || !outs[0].Accepted() {
		t.Fatalf("valid delivery not accepted: %+v", outs)
	}
	if outs[0].Status != 200 {
		t.Fatalf("status = %d, want 200", outs[0].Status)
	}
}

func TestBadSignaturesRejectedByCorrectHandler(t *testing.T) {
	target, _ := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 30, Seed: 13, BadSigRate: 1})
	outs := run(t, p, opts(target, nil))
	for _, o := range outs {
		if !o.Rejected() {
			t.Fatalf("bad-sig delivery %s got %d, want 4xx", o.DeliveryID, o.Status)
		}
	}
}

func TestMissingSignatureRejected(t *testing.T) {
	target, _ := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 20, Seed: 2, BadSigRate: 1, MissingRate: 1})
	outs := run(t, p, opts(target, nil))
	for _, o := range outs {
		if o.Sig != plan.SigMissing {
			t.Fatalf("expected all missing, got %s", o.Sig)
		}
		if !o.Rejected() {
			t.Fatalf("missing-sig delivery got %d, want 4xx", o.Status)
		}
	}
}

func TestSecretMismatchRejectsEverything(t *testing.T) {
	target, _ := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 15, Seed: 1})
	outs := run(t, p, opts(target, func(o *Options) { o.Secret = []byte("the-wrong-secret") }))
	for _, o := range outs {
		if !o.Rejected() {
			t.Fatalf("delivery signed with wrong secret got %d, want 4xx", o.Status)
		}
	}
}

func TestFlakyHandlerRecoversWithRetries(t *testing.T) {
	target, _ := serve(t, refhandler.Mode{VerifySignature: true, Idempotent: true, FailFirstAttempt: true})
	p := build(t, plan.Config{Events: 12, Seed: 1})
	outs := run(t, p, opts(target, func(o *Options) { o.MaxRetries = 2 }))
	for _, o := range outs {
		if !o.Accepted() {
			t.Fatalf("delivery %s never recovered: status %d after %d attempts", o.DeliveryID, o.Status, o.Attempts)
		}
		if o.Attempts != 2 {
			t.Fatalf("delivery %s took %d attempts, want 2 (fail then succeed)", o.DeliveryID, o.Attempts)
		}
	}
}

func TestNoRetriesLeavesFlakyFailing(t *testing.T) {
	target, _ := serve(t, refhandler.Mode{VerifySignature: true, Idempotent: true, FailFirstAttempt: true})
	p := build(t, plan.Config{Events: 6, Seed: 1})
	outs := run(t, p, opts(target, func(o *Options) { o.MaxRetries = 0 }))
	for _, o := range outs {
		if o.Status != 503 || o.Attempts != 1 {
			t.Fatalf("with no retries expected a single 503, got status %d attempts %d", o.Status, o.Attempts)
		}
	}
}

func TestConcurrencyDeliversEveryDeliveryOnce(t *testing.T) {
	target, _ := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 60, Seed: 13, DuplicateRate: 0.6, MaxDuplicates: 3})
	outs := run(t, p, opts(target, func(o *Options) { o.Concurrency = 8 }))
	if len(outs) != len(p.Deliveries) {
		t.Fatalf("got %d outcomes, want %d", len(outs), len(p.Deliveries))
	}
	seen := make(map[string]bool)
	for i, o := range outs {
		if o.Seq != i {
			t.Fatalf("outcome at %d has Seq %d", i, o.Seq)
		}
		if o.DeliveryID != p.Deliveries[i].DeliveryID {
			t.Fatalf("outcome %d id %s != plan %s", i, o.DeliveryID, p.Deliveries[i].DeliveryID)
		}
		if seen[o.DeliveryID] {
			t.Fatalf("duplicate outcome for %s", o.DeliveryID)
		}
		seen[o.DeliveryID] = true
	}
}

func TestCorrectHandlerDeduplicates(t *testing.T) {
	target, h := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 40, Seed: 13, DuplicateRate: 1, MaxDuplicates: 3})
	run(t, p, opts(target, func(o *Options) { o.Concurrency = 4 }))
	processed := h.Processed()
	for id := range p.ValidlyDelivered() {
		if processed[id] != 1 {
			t.Fatalf("correct handler processed %s %d times, want 1", id, processed[id])
		}
	}
}

func TestNonIdempotentHandlerDoubleProcesses(t *testing.T) {
	target, h := serve(t, refhandler.Mode{VerifySignature: true, Idempotent: false})
	p := build(t, plan.Config{Events: 30, Seed: 13, DuplicateRate: 1, MaxDuplicates: 3})
	run(t, p, opts(target, nil))
	processed := h.Processed()
	over := 0
	for id := range p.ValidlyDelivered() {
		if processed[id] > 1 {
			over++
		}
	}
	if over == 0 {
		t.Fatal("non-idempotent handler should have processed at least one event more than once")
	}
}

func TestStripeSchemeRoundTrips(t *testing.T) {
	h := refhandler.New([]byte(testSecret), sign.Stripe, refhandler.Correct())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p := build(t, plan.Config{Events: 10, Seed: 1})
	outs := run(t, p, opts(srv.URL+"/webhook", func(o *Options) {
		o.Scheme = sign.Stripe
		o.Now = func() time.Time { return time.Unix(1750000000, 0) }
	}))
	for _, o := range outs {
		if !o.Accepted() {
			t.Fatalf("stripe-signed valid delivery got %d", o.Status)
		}
	}
}

func TestDelayInvokesSleeperWithoutBlocking(t *testing.T) {
	target, _ := serve(t, refhandler.Correct())
	p := build(t, plan.Config{Events: 10, Seed: 6, MaxDelayMs: 100})
	var mu sync.Mutex
	var total time.Duration
	sleeps := 0
	outs := run(t, p, opts(target, func(o *Options) {
		o.Sleep = func(d time.Duration) {
			mu.Lock()
			total += d
			if d > 0 {
				sleeps++
			}
			mu.Unlock()
		}
	}))
	if len(outs) != len(p.Deliveries) {
		t.Fatal("delivery count changed under delays")
	}
	if sleeps == 0 || total == 0 {
		t.Fatal("delays present in the plan but the sleeper was never asked to wait")
	}
}

func TestEmptyTargetErrors(t *testing.T) {
	p := build(t, plan.Config{Events: 1, Seed: 1})
	if _, err := Run(context.Background(), p, Options{}); err == nil {
		t.Fatal("empty target should error")
	}
}
