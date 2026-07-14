// Tests for storm-plan construction. The plan is the reproducible contract at
// the centre of hookstorm, so these lock down determinism and the exact effect
// of every knob.
package plan

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func mustBuild(t *testing.T, c Config) Plan {
	t.Helper()
	p, err := Build(c)
	if err != nil {
		t.Fatalf("Build(%+v) errored: %v", c, err)
	}
	return p
}

func TestBuildIsDeterministic(t *testing.T) {
	c := Config{Events: 40, Seed: 777, DuplicateRate: 0.5, MaxDuplicates: 3, BadSigRate: 0.3, ReorderWindow: 5, MaxDelayMs: 200}
	a := mustBuild(t, c)
	b := mustBuild(t, c)
	ja, _ := json.Marshal(a.Deliveries)
	jb, _ := json.Marshal(b.Deliveries)
	if string(ja) != string(jb) {
		t.Fatal("same config produced different plans")
	}
}

func TestBenignStormIsOneDeliveryPerEventInOrder(t *testing.T) {
	p := mustBuild(t, Config{Events: 10, Seed: 1})
	if len(p.Deliveries) != 10 {
		t.Fatalf("got %d deliveries, want 10", len(p.Deliveries))
	}
	for i, d := range p.Deliveries {
		if d.Duplicate {
			t.Fatalf("delivery %d unexpectedly a duplicate", i)
		}
		if d.Sig != SigValid {
			t.Fatalf("delivery %d has signature %s, want valid", i, d.Sig)
		}
		if d.EventSeq != i+1 {
			t.Fatalf("no-reorder storm out of order at %d: eventSeq %d", i, d.EventSeq)
		}
	}
}

func TestDuplicateRateZeroProducesNoDuplicates(t *testing.T) {
	p := mustBuild(t, Config{Events: 50, Seed: 3, DuplicateRate: 0})
	for _, d := range p.Deliveries {
		if d.Duplicate {
			t.Fatal("duplicate appeared with rate 0")
		}
	}
	if len(p.Deliveries) != 50 {
		t.Fatalf("got %d deliveries, want exactly 50", len(p.Deliveries))
	}
}

func TestDuplicateRateOneDuplicatesEveryEvent(t *testing.T) {
	p := mustBuild(t, Config{Events: 30, Seed: 3, DuplicateRate: 1, MaxDuplicates: 3})
	counts := deliveriesPerEvent(p)
	for id, n := range counts {
		if n < 2 {
			t.Fatalf("event %s got %d deliveries with rate 1, want >= 2", id, n)
		}
	}
}

func TestMaxDuplicatesCapsExtras(t *testing.T) {
	p := mustBuild(t, Config{Events: 40, Seed: 9, DuplicateRate: 1, MaxDuplicates: 2})
	for id, n := range deliveriesPerEvent(p) {
		if n > 3 { // 1 original + at most 2 extras
			t.Fatalf("event %s got %d deliveries, cap is 3", id, n)
		}
	}
}

func TestBadSigRateZeroAllValid(t *testing.T) {
	p := mustBuild(t, Config{Events: 40, Seed: 5, BadSigRate: 0})
	for _, d := range p.Deliveries {
		if d.Sig != SigValid {
			t.Fatalf("bad signature %s appeared with rate 0", d.Sig)
		}
	}
}

func TestBadSigRateOneAllBad(t *testing.T) {
	p := mustBuild(t, Config{Events: 40, Seed: 5, BadSigRate: 1})
	for _, d := range p.Deliveries {
		if d.Sig == SigValid {
			t.Fatal("valid signature appeared with bad-sig rate 1")
		}
	}
	if len(p.ValidlyDelivered()) != 0 {
		t.Fatal("no event should be validly delivered when every signature is bad")
	}
}

func TestMissingRateOneMakesAllBadMissing(t *testing.T) {
	p := mustBuild(t, Config{Events: 60, Seed: 2, BadSigRate: 1, MissingRate: 1})
	for _, d := range p.Deliveries {
		if d.Sig != SigMissing {
			t.Fatalf("with missing-rate 1 every bad sig should be missing, got %s", d.Sig)
		}
	}
}

func TestMissingRateZeroMakesNoMissing(t *testing.T) {
	p := mustBuild(t, Config{Events: 60, Seed: 2, BadSigRate: 1, MissingRate: 0})
	for _, d := range p.Deliveries {
		if d.Sig == SigMissing {
			t.Fatal("missing signature appeared with missing-rate 0")
		}
		if d.Sig != SigWrongKey && d.Sig != SigTampered {
			t.Fatalf("bad sig should be wrong-key or tampered, got %s", d.Sig)
		}
	}
}

func TestReorderWindowZeroKeepsCreationOrder(t *testing.T) {
	p := mustBuild(t, Config{Events: 30, Seed: 8, DuplicateRate: 0.5, MaxDuplicates: 2, ReorderWindow: 0})
	prev := 0
	for _, d := range p.Deliveries {
		n := deliveryNumber(t, d.DeliveryID)
		if n < prev {
			t.Fatalf("window 0 reordered: delivery number %d after %d", n, prev)
		}
		prev = n
	}
}

func TestReorderStaysWithinWindow(t *testing.T) {
	const window = 4
	p := mustBuild(t, Config{Events: 30, Seed: 8, DuplicateRate: 0.6, MaxDuplicates: 2, ReorderWindow: window})
	for _, d := range p.Deliveries {
		creation := deliveryNumber(t, d.DeliveryID) - 1 // 0-based creation index
		if creation/window != d.Seq/window {
			t.Fatalf("delivery %s moved across window boundary: creation block %d, seq block %d",
				d.DeliveryID, creation/window, d.Seq/window)
		}
	}
}

func TestDelaysWithinBound(t *testing.T) {
	const maxDelay = 150
	p := mustBuild(t, Config{Events: 40, Seed: 6, MaxDelayMs: maxDelay})
	anyNonZero := false
	for _, d := range p.Deliveries {
		if d.DelayMs < 0 || d.DelayMs > maxDelay {
			t.Fatalf("delay %d out of [0,%d]", d.DelayMs, maxDelay)
		}
		if d.DelayMs > 0 {
			anyNonZero = true
		}
	}
	if !anyNonZero {
		t.Fatal("no delivery got a delay despite max-delay 150")
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	bad := []Config{
		{Events: -1},
		{Events: 1, DuplicateRate: 1.5},
		{Events: 1, BadSigRate: -0.1},
		{Events: 1, MissingRate: 2},
		{Events: 1, MaxDuplicates: -3},
		{Events: 1, ReorderWindow: -1},
		{Events: 1, MaxDelayMs: -5},
	}
	for _, c := range bad {
		if _, err := Build(c); err == nil {
			t.Fatalf("Build(%+v) should have errored", c)
		}
	}
}

func TestValidateDefaultsMaxDuplicates(t *testing.T) {
	c, err := Config{Events: 5, DuplicateRate: 0.5, MaxDuplicates: 0}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxDuplicates != 1 {
		t.Fatalf("MaxDuplicates defaulted to %d, want 1", c.MaxDuplicates)
	}
}

func TestZeroEventsEmptyPlan(t *testing.T) {
	p := mustBuild(t, Config{Events: 0, Seed: 1, DuplicateRate: 1, BadSigRate: 1})
	if len(p.Deliveries) != 0 || len(p.Events) != 0 {
		t.Fatal("zero events should yield an empty plan")
	}
}

func TestSummarizeCountsMatchDeliveries(t *testing.T) {
	p := mustBuild(t, Config{Events: 40, Seed: 13, DuplicateRate: 0.5, MaxDuplicates: 2, BadSigRate: 0.4, MissingRate: 0.5, MaxDelayMs: 50})
	s := p.Summarize()
	if s.Deliveries != len(p.Deliveries) {
		t.Fatalf("summary deliveries %d != %d", s.Deliveries, len(p.Deliveries))
	}
	if s.ValidSigs+s.WrongKeySigs+s.TamperedSigs+s.MissingSigs != s.Deliveries {
		t.Fatal("signature buckets do not sum to total deliveries")
	}
	// Cross-check one bucket by hand.
	valid := 0
	for _, d := range p.Deliveries {
		if d.Sig == SigValid {
			valid++
		}
	}
	if s.ValidSigs != valid {
		t.Fatalf("summary valid %d != counted %d", s.ValidSigs, valid)
	}
}

// helpers

func deliveriesPerEvent(p Plan) map[string]int {
	m := make(map[string]int)
	for _, d := range p.Deliveries {
		m[d.EventID]++
	}
	return m
}

func deliveryNumber(t *testing.T, id string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimPrefix(id, "dlv_"))
	if err != nil {
		t.Fatalf("bad delivery id %q: %v", id, err)
	}
	return n
}
