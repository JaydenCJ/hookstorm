// Tests for the verdict engine. These are pure: they build a plan, fabricate
// the outcomes a given handler would have produced, and assert on the checks —
// no server needed. Cross-package integration against a live handler lives in
// the cli and deliver packages.
package analyze

import (
	"testing"

	"github.com/JaydenCJ/hookstorm/internal/deliver"
	"github.com/JaydenCJ/hookstorm/internal/plan"
)

func buildPlan(t *testing.T, c plan.Config) plan.Plan {
	t.Helper()
	p, err := plan.Build(c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// correctOutcomes models a handler that accepts good deliveries (200) and
// rejects bad ones (401), first try, no retries.
func correctOutcomes(p plan.Plan) []deliver.Outcome {
	return outcomesFor(p, func(d plan.Delivery) (int, int) {
		if d.Sig.Bad() {
			return 401, 1
		}
		return 200, 1
	})
}

func outcomesFor(p plan.Plan, status func(plan.Delivery) (int, int)) []deliver.Outcome {
	outs := make([]deliver.Outcome, len(p.Deliveries))
	for i, d := range p.Deliveries {
		code, att := status(d)
		outs[i] = deliver.Outcome{
			Seq: d.Seq, DeliveryID: d.DeliveryID, EventID: d.EventID,
			Sig: d.Sig, Duplicate: d.Duplicate, Status: code, Attempts: att,
		}
	}
	return outs
}

// receiptsOnce is the ground truth of a correct handler: every validly
// delivered event processed exactly once.
func receiptsOnce(p plan.Plan) *Receipts {
	var rs []Receipt
	for id := range p.ValidlyDelivered() {
		rs = append(rs, Receipt{ID: id, Count: 1})
	}
	return &Receipts{Processed: rs}
}

func find(t *testing.T, res Result, name string) Check {
	t.Helper()
	for _, c := range res.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in result", name)
	return Check{}
}

func TestCorrectHandlerPassesEverything(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 30, Seed: 13, DuplicateRate: 0.5, MaxDuplicates: 2, BadSigRate: 0.3})
	res := Analyze(p, correctOutcomes(p), receiptsOnce(p))
	if !res.Passed {
		t.Fatalf("correct handler failed: %+v", res.Checks)
	}
	if find(t, res, CheckSignatures).Status != Pass {
		t.Fatal("signatures-enforced should pass")
	}
	if find(t, res, CheckIdempotent).Status != Pass {
		t.Fatal("idempotent should pass")
	}
}

func TestSignatureBypassFails(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 30, Seed: 13, BadSigRate: 0.5})
	// Handler that accepts everything, including bad signatures.
	outs := outcomesFor(p, func(plan.Delivery) (int, int) { return 200, 1 })
	res := Analyze(p, outs, nil)
	c := find(t, res, CheckSignatures)
	if c.Status != Fail {
		t.Fatalf("expected signatures-enforced FAIL, got %s", c.Status)
	}
	if c.Violations == 0 {
		t.Fatal("bypass should report violations")
	}
	if res.Passed {
		t.Fatal("overall verdict should be FAIL on a signature bypass")
	}
}

func TestSignaturesSkipWhenNoBadDeliveries(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 20, Seed: 1, BadSigRate: 0})
	res := Analyze(p, correctOutcomes(p), nil)
	if find(t, res, CheckSignatures).Status != Skip {
		t.Fatal("signatures-enforced should SKIP with no bad deliveries")
	}
}

func TestHealthyFailsOnServerError(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 10, Seed: 1})
	outs := outcomesFor(p, func(d plan.Delivery) (int, int) {
		if d.Seq == 3 {
			return 500, 1
		}
		return 200, 1
	})
	res := Analyze(p, outs, nil)
	c := find(t, res, CheckHealthy)
	if c.Status != Fail || c.Violations != 1 {
		t.Fatalf("expected 1 health violation, got %s/%d", c.Status, c.Violations)
	}
}

func TestRetriesSkipWhenNoneRetried(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 10, Seed: 1})
	res := Analyze(p, correctOutcomes(p), nil)
	if find(t, res, CheckRetries).Status != Skip {
		t.Fatal("retries-recover should SKIP when nothing was retried")
	}
}

func TestRetriesRecoverPasses(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 10, Seed: 1})
	outs := outcomesFor(p, func(d plan.Delivery) (int, int) {
		if d.Seq == 2 {
			return 200, 2 // needed one retry, then succeeded
		}
		return 200, 1
	})
	res := Analyze(p, outs, nil)
	if find(t, res, CheckRetries).Status != Pass {
		t.Fatal("a recovered retry should pass retries-recover")
	}
}

func TestRetriesFailWhenNeverRecovered(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 10, Seed: 1})
	outs := outcomesFor(p, func(d plan.Delivery) (int, int) {
		if d.Seq == 2 {
			return 500, 3 // retried to exhaustion, still failing
		}
		return 200, 1
	})
	res := Analyze(p, outs, nil)
	if find(t, res, CheckRetries).Status != Fail {
		t.Fatal("an unrecovered retry should fail retries-recover")
	}
}

func TestReceiptChecksSkipWithoutReceipts(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 10, Seed: 1, DuplicateRate: 0.5})
	res := Analyze(p, correctOutcomes(p), nil)
	for _, name := range []string{CheckIdempotent, CheckNoLoss, CheckNoSpurious} {
		if find(t, res, name).Status != Skip {
			t.Fatalf("%s should SKIP without receipts", name)
		}
	}
}

func TestIdempotentFailsOnDoubleProcessing(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 20, Seed: 13, DuplicateRate: 1, MaxDuplicates: 2})
	// Receipts that count every event twice — a handler that never dedupes.
	var rs []Receipt
	for id := range p.ValidlyDelivered() {
		rs = append(rs, Receipt{ID: id, Count: 2})
	}
	res := Analyze(p, correctOutcomes(p), &Receipts{Processed: rs})
	c := find(t, res, CheckIdempotent)
	if c.Status != Fail || c.Violations == 0 {
		t.Fatalf("double processing should fail idempotent, got %s/%d", c.Status, c.Violations)
	}
}

func TestNoLossFailsOnMissingEvent(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 20, Seed: 13})
	// Drop one validly-delivered event from the receipts.
	valid := sortedKeys(p.ValidlyDelivered())
	if len(valid) < 2 {
		t.Skip("need at least two valid events")
	}
	var rs []Receipt
	for _, id := range valid[1:] { // omit the first
		rs = append(rs, Receipt{ID: id, Count: 1})
	}
	res := Analyze(p, correctOutcomes(p), &Receipts{Processed: rs})
	c := find(t, res, CheckNoLoss)
	if c.Status != Fail || c.Violations != 1 {
		t.Fatalf("a lost event should fail no-loss once, got %s/%d", c.Status, c.Violations)
	}
}

func TestNoSpuriousFailsOnProcessedBadEvent(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 20, Seed: 13})
	rs := []Receipt{{ID: "evt_99999", Count: 1}} // never delivered at all
	for id := range p.ValidlyDelivered() {
		rs = append(rs, Receipt{ID: id, Count: 1})
	}
	res := Analyze(p, correctOutcomes(p), &Receipts{Processed: rs})
	c := find(t, res, CheckNoSpurious)
	if c.Status != Fail {
		t.Fatalf("processing a never-delivered event should fail no-spurious, got %s", c.Status)
	}
}

func TestReceiptsCountsCollapseAndDefault(t *testing.T) {
	r := Receipts{Processed: []Receipt{{ID: "a"}, {ID: "a", Count: 2}, {ID: "b", Count: 1}}}
	counts := r.Counts()
	if counts["a"] != 3 { // default-1 plus explicit 2
		t.Fatalf("a = %d, want 3", counts["a"])
	}
	if counts["b"] != 1 {
		t.Fatalf("b = %d, want 1", counts["b"])
	}
}

func TestVerdictAggregation(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 10, Seed: 1, BadSigRate: 0.5})
	// All good → passed.
	if !Analyze(p, correctOutcomes(p), nil).Passed {
		t.Fatal("expected pass when every applicable check passes")
	}
	// Introduce a single 5xx → not passed.
	outs := outcomesFor(p, func(d plan.Delivery) (int, int) {
		if d.Seq == 0 {
			return 500, 1
		}
		if d.Sig.Bad() {
			return 401, 1
		}
		return 200, 1
	})
	if Analyze(p, outs, nil).Passed {
		t.Fatal("expected fail when a check fails")
	}
}
