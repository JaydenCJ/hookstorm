// Tests for rendering. Output is a stable interface (people grep it, machines
// parse the JSON), so these pin the shape rather than the prose.
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/hookstorm/internal/analyze"
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

func TestPlanTextHasHeaderAndEveryDelivery(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 8, Seed: 13, DuplicateRate: 0.5, BadSigRate: 0.35})
	var buf bytes.Buffer
	PlanText(&buf, p)
	out := buf.String()
	if !strings.Contains(out, "hookstorm plan — seed 13") {
		t.Fatal("missing plan header")
	}
	for _, d := range p.Deliveries {
		if !strings.Contains(out, d.DeliveryID) {
			t.Fatalf("delivery %s not rendered", d.DeliveryID)
		}
	}
	if !strings.Contains(out, "duplicate") {
		t.Fatal("duplicate legend missing")
	}
}

func TestPlanJSONIsValidAndTagged(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 5, Seed: 1})
	var buf bytes.Buffer
	if err := PlanJSON(&buf, p); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		Summary       struct {
			Deliveries int `json:"deliveries"`
		} `json:"summary"`
		Deliveries []map[string]any `json:"deliveries"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("plan JSON does not parse: %v", err)
	}
	if out.Tool != "hookstorm" || out.SchemaVersion != 1 {
		t.Fatalf("bad envelope: %+v", out)
	}
	if out.Summary.Deliveries != len(out.Deliveries) {
		t.Fatal("summary/deliveries length mismatch")
	}
}

func TestPlanJSONEmptyDeliveriesIsArrayNotNull(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 0, Seed: 1})
	var buf bytes.Buffer
	if err := PlanJSON(&buf, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"deliveries": []`) {
		t.Fatalf("empty deliveries should render as [], got:\n%s", buf.String())
	}
}

func TestRunTextShowsPassVerdict(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 4, Seed: 1})
	res := analyze.Result{Passed: true, Checks: []analyze.Check{
		{Name: analyze.CheckHealthy, Status: analyze.Pass, Detail: "all clean"},
	}}
	var buf bytes.Buffer
	RunText(&buf, "http://127.0.0.1:9/webhook", p, res)
	if !strings.Contains(buf.String(), "verdict: PASS") {
		t.Fatalf("missing pass verdict:\n%s", buf.String())
	}
}

func TestRunTextShowsFailVerdictWithEvidence(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 4, Seed: 1})
	res := analyze.Result{Passed: false, Checks: []analyze.Check{
		{Name: analyze.CheckSignatures, Status: analyze.Fail, Detail: "1 accepted", Evidence: []string{"dlv_00001 missing accepted with 200"}},
	}}
	var buf bytes.Buffer
	RunText(&buf, "http://127.0.0.1:9/webhook", p, res)
	out := buf.String()
	if !strings.Contains(out, "verdict: FAIL") {
		t.Fatal("missing fail verdict")
	}
	if !strings.Contains(out, "dlv_00001 missing accepted with 200") {
		t.Fatal("evidence for a failing check should be printed")
	}
}

func TestRunJSONCarriesVerdictAndChecks(t *testing.T) {
	p := buildPlan(t, plan.Config{Events: 4, Seed: 1})
	res := analyze.Result{Passed: false, Checks: []analyze.Check{
		{Name: analyze.CheckIdempotent, Status: analyze.Fail, Detail: "x", Violations: 2},
	}}
	var buf bytes.Buffer
	if err := RunJSON(&buf, "http://127.0.0.1:9/webhook", p, []deliver.Outcome{}, res); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		Passed        bool   `json:"passed"`
		Checks        []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
		Deliveries []any `json:"deliveries"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("run JSON does not parse: %v", err)
	}
	if out.Tool != "hookstorm" || out.SchemaVersion != 1 || out.Passed {
		t.Fatalf("bad envelope: %+v", out)
	}
	if len(out.Checks) != 1 || out.Checks[0].Name != analyze.CheckIdempotent {
		t.Fatalf("checks not carried through: %+v", out.Checks)
	}
	if out.Deliveries == nil {
		t.Fatal("deliveries should be [] not null")
	}
}
