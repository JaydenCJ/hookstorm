// Package report renders plans and run verdicts as human-readable text or
// stable JSON. It is pure formatting: it never computes a verdict (that is
// analyze) or touches the network (that is deliver).
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/hookstorm/internal/analyze"
	"github.com/JaydenCJ/hookstorm/internal/plan"
)

// stormLine is the one-line description of what a storm contains, shared by the
// plan view and the run verdict.
func stormLine(p plan.Plan) string {
	s := p.Summarize()
	parts := []string{
		fmt.Sprintf("%d events", s.Events),
		fmt.Sprintf("%d deliveries", s.Deliveries),
	}
	if s.Duplicates > 0 {
		parts = append(parts, fmt.Sprintf("%d duplicates", s.Duplicates))
	}
	bad := s.WrongKeySigs + s.TamperedSigs + s.MissingSigs
	if bad > 0 {
		parts = append(parts, fmt.Sprintf("%d bad signatures (%d wrong-key, %d tampered, %d missing)",
			bad, s.WrongKeySigs, s.TamperedSigs, s.MissingSigs))
	}
	if s.Delayed > 0 {
		parts = append(parts, fmt.Sprintf("%d delayed", s.Delayed))
	}
	return strings.Join(parts, " · ")
}

// PlanText prints the storm header and every delivery in wire order, so a run
// can be inspected (and reproduced from its seed) before any request is sent.
func PlanText(w io.Writer, p plan.Plan) {
	fmt.Fprintf(w, "hookstorm plan — seed %d\n", p.Config.Seed)
	fmt.Fprintf(w, "%s\n\n", stormLine(p))
	fmt.Fprintf(w, "%-4s %-11s %-11s %-22s %-10s %s\n", "seq", "delivery", "event", "type", "signature", "delay")
	for _, d := range p.Deliveries {
		delay := "-"
		if d.DelayMs > 0 {
			delay = fmt.Sprintf("%dms", d.DelayMs)
		}
		tag := string(d.Sig)
		if d.Duplicate {
			tag += "*"
		}
		fmt.Fprintf(w, "%-4d %-11s %-11s %-22s %-10s %s\n",
			d.Seq, d.DeliveryID, d.EventID, d.EventType, tag, delay)
	}
	fmt.Fprintf(w, "\n(* = duplicate delivery of an event already sent)\n")
}

// RunText prints the storm header, each check with its verdict, and the overall
// result. Failing checks quote their evidence.
func RunText(w io.Writer, target string, p plan.Plan, res analyze.Result) {
	fmt.Fprintf(w, "hookstorm run — %d deliveries to %s\n", len(p.Deliveries), target)
	fmt.Fprintf(w, "storm: seed %d · %s\n\n", p.Config.Seed, stormLine(p))

	fmt.Fprintf(w, "checks\n")
	for _, c := range res.Checks {
		fmt.Fprintf(w, "  %-4s %-24s %s\n", c.Status, c.Name, c.Detail)
		if c.Status == analyze.Fail {
			for _, e := range c.Evidence {
				fmt.Fprintf(w, "         └─ %s\n", e)
			}
		}
	}
	fmt.Fprintf(w, "\nverdict: %s\n", verdict(res.Passed))
}

func verdict(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}
