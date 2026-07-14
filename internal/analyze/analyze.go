// Package analyze turns a plan plus the outcomes observed while running it
// (and, optionally, the receipts the handler reports) into a verdict: a set of
// named checks, each PASS, FAIL, or SKIP, and an overall pass/fail the CLI can
// exit on. Every check is a property that is actually decidable from the
// outside — hookstorm never guesses. Checks that need information hookstorm
// cannot observe over HTTP (was each event processed exactly once?) SKIP
// unless the handler exposes a receipts endpoint.
package analyze

import (
	"fmt"
	"sort"

	"github.com/JaydenCJ/hookstorm/internal/deliver"
	"github.com/JaydenCJ/hookstorm/internal/plan"
)

// Check names. Stable strings: they appear in JSON output and are asserted on.
const (
	CheckSignatures = "signatures-enforced"
	CheckHealthy    = "handler-healthy"
	CheckRetries    = "retries-recover"
	CheckIdempotent = "idempotent"
	CheckNoLoss     = "no-loss"
	CheckNoSpurious = "no-spurious-processing"
)

// Status is a per-check verdict.
type Status string

const (
	Pass Status = "PASS"
	Fail Status = "FAIL"
	Skip Status = "SKIP"
)

// Receipts is what a handler reports it committed side effects for. A correct
// handler processes each validly-delivered event exactly once, and nothing it
// should have rejected.
type Receipts struct {
	Processed []Receipt `json:"processed"`
}

// Receipt is one processed event and how many times the handler acted on it.
// A handler that de-duplicates always reports count 1.
type Receipt struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// Counts collapses receipts to a map. A bare id with no count is treated as 1,
// so a minimal handler can report `{"processed":[{"id":"evt_1"}]}`.
func (r Receipts) Counts() map[string]int {
	m := make(map[string]int, len(r.Processed))
	for _, rc := range r.Processed {
		c := rc.Count
		if c == 0 {
			c = 1
		}
		m[rc.ID] += c
	}
	return m
}

// Check is one property's verdict.
type Check struct {
	Name       string   `json:"name"`
	Status     Status   `json:"status"`
	Detail     string   `json:"detail"`
	Violations int      `json:"violations"`
	Evidence   []string `json:"evidence,omitempty"`
}

// Result is the whole verdict.
type Result struct {
	Checks []Check `json:"checks"`
	Passed bool    `json:"passed"`
}

// maxEvidence caps how many offending items each check quotes, keeping reports
// bounded while still pointing at concrete failures.
const maxEvidence = 5

// Analyze evaluates every check and returns the verdict. receipts may be nil,
// in which case the receipt-based checks SKIP.
func Analyze(p plan.Plan, outcomes []deliver.Outcome, receipts *Receipts) Result {
	byDelivery := make(map[string]deliver.Outcome, len(outcomes))
	for _, o := range outcomes {
		byDelivery[o.DeliveryID] = o
	}

	var checks []Check
	checks = append(checks, checkSignatures(p, byDelivery))
	checks = append(checks, checkHealthy(p, byDelivery))
	checks = append(checks, checkRetries(p, byDelivery))
	checks = append(checks, checkReceipts(p, receipts)...)

	passed := true
	for _, c := range checks {
		if c.Status == Fail {
			passed = false
		}
	}
	return Result{Checks: checks, Passed: passed}
}

// checkSignatures: every deliberately-bad delivery must be rejected (any non
// 2xx). A bad signature that gets a 2xx is a signature-verification bypass —
// the single most dangerous webhook-handler bug there is.
func checkSignatures(p plan.Plan, byDelivery map[string]deliver.Outcome) Check {
	c := Check{Name: CheckSignatures}
	bad := 0
	for _, d := range p.Deliveries {
		if !d.Sig.Bad() {
			continue
		}
		bad++
		o := byDelivery[d.DeliveryID]
		if o.Accepted() {
			c.Violations++
			c.addEvidence(fmt.Sprintf("%s %s accepted with %d (must reject)", d.DeliveryID, d.Sig, o.Status))
		}
	}
	if bad == 0 {
		c.Status = Skip
		c.Detail = "no bad-signature deliveries in this storm"
		return c
	}
	if c.Violations == 0 {
		c.Status = Pass
		c.Detail = fmt.Sprintf("all %d bad-signature deliveries were rejected", bad)
	} else {
		c.Status = Fail
		c.Detail = fmt.Sprintf("%d of %d bad-signature deliveries were accepted", c.Violations, bad)
	}
	return c
}

// checkHealthy: no delivery — good or bad — should make the handler return 5xx
// or drop the connection. Even malformed input deserves a clean 4xx.
func checkHealthy(p plan.Plan, byDelivery map[string]deliver.Outcome) Check {
	c := Check{Name: CheckHealthy}
	if len(p.Deliveries) == 0 {
		c.Status = Skip
		c.Detail = "no deliveries"
		return c
	}
	for _, d := range p.Deliveries {
		o := byDelivery[d.DeliveryID]
		switch {
		case o.Failed():
			c.Violations++
			c.addEvidence(fmt.Sprintf("%s no response (%s)", d.DeliveryID, shortErr(o.Err)))
		case o.ServerError():
			c.Violations++
			c.addEvidence(fmt.Sprintf("%s returned %d", d.DeliveryID, o.Status))
		}
	}
	if c.Violations == 0 {
		c.Status = Pass
		c.Detail = fmt.Sprintf("all %d deliveries got a clean response", len(p.Deliveries))
	} else {
		c.Status = Fail
		c.Detail = fmt.Sprintf("%d deliveries hit a 5xx or transport failure", c.Violations)
	}
	return c
}

// checkRetries: any delivery that had to be retried should have eventually
// resolved (2xx) or been terminally rejected (4xx). A delivery still failing
// after every retry means the handler never recovered.
func checkRetries(p plan.Plan, byDelivery map[string]deliver.Outcome) Check {
	c := Check{Name: CheckRetries}
	retried := 0
	for _, d := range p.Deliveries {
		o := byDelivery[d.DeliveryID]
		if o.Attempts <= 1 {
			continue
		}
		retried++
		if !o.Accepted() && !o.Rejected() {
			c.Violations++
			c.addEvidence(fmt.Sprintf("%s still failing after %d attempts (last %d)", d.DeliveryID, o.Attempts, o.Status))
		}
	}
	if retried == 0 {
		c.Status = Skip
		c.Detail = "no delivery needed a retry"
		return c
	}
	if c.Violations == 0 {
		c.Status = Pass
		c.Detail = fmt.Sprintf("all %d retried deliveries resolved", retried)
	} else {
		c.Status = Fail
		c.Detail = fmt.Sprintf("%d of %d retried deliveries never recovered", c.Violations, retried)
	}
	return c
}

// checkReceipts evaluates the three properties that need the handler to tell
// us what it processed: exactly-once (idempotent), no-loss, and no-spurious.
func checkReceipts(p plan.Plan, receipts *Receipts) []Check {
	idempotent := Check{Name: CheckIdempotent}
	noLoss := Check{Name: CheckNoLoss}
	noSpurious := Check{Name: CheckNoSpurious}

	if receipts == nil {
		idempotent.skip("no receipts endpoint provided (pass --receipts-url)")
		noLoss.skip("no receipts endpoint provided (pass --receipts-url)")
		noSpurious.skip("no receipts endpoint provided (pass --receipts-url)")
		return []Check{idempotent, noLoss, noSpurious}
	}

	counts := receipts.Counts()
	valid := p.ValidlyDelivered()
	validIDs := sortedKeys(valid)

	// idempotent: no validly-delivered event processed more than once.
	for _, id := range validIDs {
		if counts[id] > 1 {
			idempotent.Violations++
			idempotent.addEvidence(fmt.Sprintf("%s processed %d times (duplicates not de-duplicated)", id, counts[id]))
		}
	}
	finish(&idempotent, len(validIDs) == 0,
		"no validly-delivered events to de-duplicate",
		fmt.Sprintf("every validly-delivered event was processed at most once (%d events)", len(validIDs)),
		fmt.Sprintf("%d events were processed more than once", idempotent.Violations))

	// no-loss: every validly-delivered event was processed at least once.
	for _, id := range validIDs {
		if counts[id] == 0 {
			noLoss.Violations++
			noLoss.addEvidence(fmt.Sprintf("%s was delivered with a valid signature but never processed", id))
		}
	}
	finish(&noLoss, len(validIDs) == 0,
		"no validly-delivered events to account for",
		fmt.Sprintf("every validly-delivered event was processed (%d events)", len(validIDs)),
		fmt.Sprintf("%d validly-delivered events were lost", noLoss.Violations))

	// no-spurious: nothing was processed that was never validly delivered
	// (e.g. a bad-signature event the handler wrongly acted on).
	for _, id := range sortedCountKeys(counts) {
		if !valid[id] {
			noSpurious.Violations++
			noSpurious.addEvidence(fmt.Sprintf("%s was processed but never had a valid signature", id))
		}
	}
	finish(&noSpurious, false,
		"",
		"the handler processed only validly-delivered events",
		fmt.Sprintf("%d events were processed despite never being validly delivered", noSpurious.Violations))

	return []Check{idempotent, noLoss, noSpurious}
}

// finish sets a receipt check's terminal status from its violation count.
func finish(c *Check, skip bool, skipDetail, passDetail, failDetail string) {
	switch {
	case skip:
		c.skip(skipDetail)
	case c.Violations == 0:
		c.Status = Pass
		c.Detail = passDetail
	default:
		c.Status = Fail
		c.Detail = failDetail
	}
}

func (c *Check) skip(detail string) {
	c.Status = Skip
	c.Detail = detail
}

func (c *Check) addEvidence(s string) {
	if len(c.Evidence) < maxEvidence {
		c.Evidence = append(c.Evidence, s)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func shortErr(e string) string {
	if e == "" {
		return "transport error"
	}
	return e
}
