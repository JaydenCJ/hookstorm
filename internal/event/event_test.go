// Tests for the logical event model. Bodies are signed verbatim, so their
// exact bytes and stability matter more than anything else here.
package event

import (
	"encoding/json"
	"testing"
)

func TestGenerateCountAndSequences(t *testing.T) {
	events := Generate(5)
	if len(events) != 5 {
		t.Fatalf("Generate(5) returned %d events", len(events))
	}
	for i, e := range events {
		if e.Sequence != i+1 {
			t.Fatalf("event %d has sequence %d, want %d", i, e.Sequence, i+1)
		}
	}
}

func TestGenerateZeroAndNegative(t *testing.T) {
	if len(Generate(0)) != 0 {
		t.Fatal("Generate(0) should be empty")
	}
	if len(Generate(-4)) != 0 {
		t.Fatal("Generate(negative) should be empty, not panic")
	}
}

func TestIDFormatIsZeroPadded(t *testing.T) {
	if got := ID(1); got != "evt_00001" {
		t.Fatalf("ID(1) = %s, want evt_00001", got)
	}
	if got := ID(42); got != "evt_00042" {
		t.Fatalf("ID(42) = %s, want evt_00042", got)
	}
}

func TestTypesCycleDeterministically(t *testing.T) {
	events := Generate(len(types) + 2)
	if events[0].Type != types[0] {
		t.Fatalf("first type = %s, want %s", events[0].Type, types[0])
	}
	// Wraps around after the table is exhausted.
	if events[len(types)].Type != types[0] {
		t.Fatalf("type did not cycle: %s", events[len(types)].Type)
	}
}

func TestBodyIsValidJSON(t *testing.T) {
	for _, e := range Generate(8) {
		var out map[string]any
		if err := json.Unmarshal(e.Body(), &out); err != nil {
			t.Fatalf("event %s body is not valid JSON: %v", e.ID, err)
		}
		if out["id"] != e.ID {
			t.Fatalf("body id = %v, want %s", out["id"], e.ID)
		}
	}
}

func TestBodyIsStable(t *testing.T) {
	// The MAC is computed over these bytes; two calls must be byte-identical.
	e := Generate(3)[1]
	if string(e.Body()) != string(e.Body()) {
		t.Fatal("Body() is not stable across calls")
	}
	want := `{"id":"evt_00002","type":"invoice.paid","sequence":2,"data":{"n":2}}`
	if got := string(e.Body()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}
