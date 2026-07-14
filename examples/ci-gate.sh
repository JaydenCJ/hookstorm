#!/usr/bin/env bash
# ci-gate.sh <mode>: start a reference handler in the given mode, storm it, and
# exit with hookstorm's verdict — 0 if every correctness check passed, 1 if any
# failed. Drop this shape into a pre-push hook or pipeline step, pointing
# --target at your own handler instead of the reference one.
set -euo pipefail

MODE="${1:-correct}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-18090}"
WORKDIR="$(mktemp -d)"
SRV=""
cleanup() {
  [ -n "$SRV" ] && kill "$SRV" 2>/dev/null || true
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

HS="$WORKDIR/hookstorm"
REFH="$WORKDIR/reference-handler"
go build -o "$HS" "$ROOT/cmd/hookstorm"
go build -o "$REFH" "$ROOT/examples/reference-handler"

"$REFH" --addr "127.0.0.1:$PORT" --mode "$MODE" >/dev/null 2>&1 &
SRV=$!

# Bounded readiness poll (no fixed sleep).
READY=0
for _ in $(seq 1 100); do
  if "$HS" run --target "http://127.0.0.1:$PORT/webhook" --events 1 --seed 1 \
      --bad-sig 0 --duplicates 0 >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 0.05
done
if [ "$READY" -ne 1 ]; then
  echo "ci-gate: handler never became ready on port $PORT (is it already in use?)" >&2
  exit 3
fi

# The real gate: a stormy delivery with duplicates and bad signatures, plus the
# receipts endpoint so the idempotency and loss checks run. Deliberately not
# `exec` — the EXIT trap must still fire to stop the handler and free the port.
"$HS" run \
  --target "http://127.0.0.1:$PORT/webhook" \
  --receipts-url "http://127.0.0.1:$PORT/receipts" \
  --events 20 --seed 13 --duplicates 0.6 --max-duplicates 2 --bad-sig 0.3
