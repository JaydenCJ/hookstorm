package report

import (
	"encoding/json"
	"io"

	"github.com/JaydenCJ/hookstorm/internal/analyze"
	"github.com/JaydenCJ/hookstorm/internal/deliver"
	"github.com/JaydenCJ/hookstorm/internal/plan"
	"github.com/JaydenCJ/hookstorm/internal/version"
)

// schemaVersion bumps only on breaking changes to the JSON shape, independently
// of the tool version.
const schemaVersion = 1

// PlanJSON renders a plan as indented JSON with a trailing newline.
func PlanJSON(w io.Writer, p plan.Plan) error {
	out := struct {
		Tool          string          `json:"tool"`
		Version       string          `json:"version"`
		SchemaVersion int             `json:"schema_version"`
		Config        plan.Config     `json:"config"`
		Summary       plan.Summary    `json:"summary"`
		Deliveries    []plan.Delivery `json:"deliveries"`
	}{
		Tool:          "hookstorm",
		Version:       version.Version,
		SchemaVersion: schemaVersion,
		Config:        p.Config,
		Summary:       p.Summarize(),
		Deliveries:    emptyIfNilDeliveries(p.Deliveries),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// RunJSON renders the run verdict, the storm summary, and every outcome.
func RunJSON(w io.Writer, target string, p plan.Plan, outcomes []deliver.Outcome, res analyze.Result) error {
	out := struct {
		Tool          string            `json:"tool"`
		Version       string            `json:"version"`
		SchemaVersion int               `json:"schema_version"`
		Target        string            `json:"target"`
		Config        plan.Config       `json:"config"`
		Summary       plan.Summary      `json:"summary"`
		Passed        bool              `json:"passed"`
		Checks        []analyze.Check   `json:"checks"`
		Deliveries    []deliver.Outcome `json:"deliveries"`
	}{
		Tool:          "hookstorm",
		Version:       version.Version,
		SchemaVersion: schemaVersion,
		Target:        target,
		Config:        p.Config,
		Summary:       p.Summarize(),
		Passed:        res.Passed,
		Checks:        res.Checks,
		Deliveries:    emptyIfNilOutcomes(outcomes),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func emptyIfNilDeliveries(d []plan.Delivery) []plan.Delivery {
	if d == nil {
		return []plan.Delivery{}
	}
	return d
}

func emptyIfNilOutcomes(o []deliver.Outcome) []deliver.Outcome {
	if o == nil {
		return []deliver.Outcome{}
	}
	return o
}
