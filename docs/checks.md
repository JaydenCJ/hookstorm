# Correctness checks

After a storm runs, hookstorm evaluates a fixed set of checks. Each is a
property that is actually decidable from the outside — hookstorm never guesses
from payload content. A check is `PASS`, `FAIL`, or `SKIP` (not applicable to
this storm, or needs information that was not provided). The overall verdict is
`FAIL` — and `hookstorm run` exits 1 — if any applicable check fails.

Three checks work purely from the HTTP responses hookstorm observed. Three more
need the handler to report what it actually processed, via a `--receipts-url`
endpoint; without it they `SKIP`.

| Check | Needs receipts | Passes when | Catches |
|---|---|---|---|
| `signatures-enforced` | no | every wrong-key / tampered / missing delivery got a 4xx | a handler that never verifies the signature (the bypass bug) |
| `handler-healthy` | no | no delivery — good or bad — caused a 5xx or dropped the connection | crashes on duplicates, panics on malformed input, timeouts under load |
| `retries-recover` | no | every delivery that had to be retried ended 2xx or terminally 4xx | a handler that stays broken after a transient failure |
| `idempotent` | yes | each validly-delivered event was processed **at most once** | double-charging on redelivered / duplicated events |
| `no-loss` | yes | each validly-delivered event was processed **at least once** | events silently dropped under reordering or load |
| `no-spurious-processing` | yes | nothing was processed that was never validly delivered | acting on a rejected or unsigned event |

## The receipts endpoint

To enable the last three checks, expose a `GET` endpoint that returns the set
of event ids your handler has committed side effects for, and pass its URL as
`--receipts-url`. The body is JSON:

```json
{
  "processed": [
    { "id": "evt_00001", "count": 1 },
    { "id": "evt_00002", "count": 1 }
  ]
}
```

`count` is how many times the handler acted on that event; a correct,
idempotent handler always reports `1`. `count` may be omitted, in which case it
is treated as `1`, so a minimal handler can return
`{"processed":[{"id":"evt_00001"}]}`.

The event id is the value hookstorm sends in the `X-Hookstorm-Event` header and
in the body's `"id"` field — that is the idempotency key your handler should
de-duplicate on. See [`examples/reference-handler`](../examples/reference-handler)
for a complete implementation of both the webhook and receipts endpoints.

## Why "validly delivered"

Several checks are phrased against the events that received **at least one
correctly-signed delivery**. An event whose every delivery had a bad signature
should never be processed, so it is excluded from `idempotent` and `no-loss`
(and its appearance in receipts is what `no-spurious-processing` catches). This
keeps the checks honest under storms that mix valid and invalid signatures for
the same event.
