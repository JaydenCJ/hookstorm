// Package whproto holds the wire-level constants shared between hookstorm's
// sender (the deliver package) and its bundled reference handler (the
// refhandler package). Keeping them here — in a leaf package that imports
// nothing — lets both sides agree on the header names without an import cycle,
// and documents the tiny "protocol" a hookstorm delivery speaks.
package whproto

// Headers every delivery carries. They mirror the metadata real webhook
// providers attach: a stable event id that is the idempotency key, a
// per-delivery id that stays constant across retries, the attempt counter, and
// the event type.
const (
	// HeaderEvent carries the logical event id (the idempotency key). A correct
	// handler de-duplicates on this.
	HeaderEvent = "X-Hookstorm-Event"
	// HeaderDelivery carries the per-delivery id, constant across retries of the
	// same delivery, unique across duplicates — like a provider's delivery UUID.
	HeaderDelivery = "X-Hookstorm-Delivery"
	// HeaderAttempt carries the 1-based attempt number for this delivery.
	HeaderAttempt = "X-Hookstorm-Attempt"
	// HeaderEventType carries the event type, e.g. "invoice.paid".
	HeaderEventType = "X-Hookstorm-Event-Type"
)
