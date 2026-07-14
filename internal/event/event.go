// Package event models the logical webhook events a storm delivers. An event
// is the thing that "really happened" exactly once (an invoice was paid, an
// order shipped); a storm may deliver it zero, one, or many times, in any
// order, with good or bad signatures. Keeping the logical event separate from
// its deliveries is what lets hookstorm reason about idempotency and loss:
// "this event was delivered four times — did the handler process it exactly
// once?"
package event

import (
	"encoding/json"
	"fmt"
)

// Event is a single logical occurrence. ID is the idempotency key a correct
// handler must de-duplicate on; Sequence is the order in which events truly
// happened, which reordering faults deliberately scramble on the wire.
type Event struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Sequence int    `json:"sequence"`
	Data     Data   `json:"data"`
}

// Data is a tiny, deterministic payload body. It is intentionally boring:
// storms exercise delivery semantics, not payload parsing, so the body just
// has to be stable and valid JSON.
type Data struct {
	N int `json:"n"`
}

// types cycles through a realistic mix of event names so per-type behaviour
// (a handler that only de-duplicates some types, say) has something to bite
// on. Order is fixed for determinism.
var types = []string{
	"invoice.created",
	"invoice.paid",
	"customer.updated",
	"subscription.canceled",
	"payment.succeeded",
	"payment.failed",
	"order.shipped",
	"order.refunded",
}

// ID returns the canonical idempotency key for the nth event (1-based).
func ID(seq int) string {
	return fmt.Sprintf("evt_%05d", seq)
}

// Generate returns n events with stable IDs, cycling types, and sequence
// numbers 1..n. It never touches the clock or a random source, so the same n
// always yields the same events.
func Generate(n int) []Event {
	if n < 0 {
		n = 0
	}
	out := make([]Event, n)
	for i := 0; i < n; i++ {
		seq := i + 1
		out[i] = Event{
			ID:       ID(seq),
			Type:     types[i%len(types)],
			Sequence: seq,
			Data:     Data{N: seq},
		}
	}
	return out
}

// Body renders the event as the exact JSON bytes that go on the wire. The MAC
// is computed over these bytes, so Body must be deterministic: struct field
// order is fixed and no map is marshalled.
func (e Event) Body() []byte {
	b, err := json.Marshal(e)
	if err != nil {
		// Event has only fixed scalar fields; marshalling cannot fail.
		panic(fmt.Sprintf("event: marshal %s: %v", e.ID, err))
	}
	return b
}
