#!/usr/bin/env bash
# End-to-end smoke test for hookstorm: builds the CLI and the bundled reference
# handler, starts a correct handler and a deliberately buggy one on loopback,
# and asserts on the real verdicts. No external network, idempotent, seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
SERVER_PIDS=()
cleanup() {
  for pid in "${SERVER_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

HS="$WORKDIR/hookstorm"
REFH="$WORKDIR/reference-handler"

# start_handler <mode> <port>: launch a reference handler and wait until it
# answers on loopback. Bounded readiness poll — no fixed sleep, no flakiness.
start_handler() {
  local mode="$1" port="$2"
  "$REFH" --addr "127.0.0.1:$port" --mode "$mode" >/dev/null 2>&1 &
  SERVER_PIDS+=("$!")
  local i
  for i in $(seq 1 100); do
    if "$HS" run --target "http://127.0.0.1:$port/webhook" --events 1 --seed 1 \
        --bad-sig 0 --duplicates 0 >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.05
  done
  fail "handler ($mode) never became ready on port $port"
}

echo "1. build"
(cd "$ROOT" && go build -o "$HS" ./cmd/hookstorm) || fail "go build (cli) failed"
(cd "$ROOT" && go build -o "$REFH" ./examples/reference-handler) || fail "go build (handler) failed"

echo "2. version matches manifest"
"$HS" version | grep -qx "hookstorm 0.1.0" || fail "--version mismatch"

echo "3. plan is offline and deterministic"
A="$("$HS" plan --events 8 --seed 13 --bad-sig 0.35 --duplicates 0.5)"
B="$("$HS" plan --events 8 --seed 13 --bad-sig 0.35 --duplicates 0.5)"
[ "$A" = "$B" ] || fail "plan output is not deterministic"
echo "$A" | grep -q "dlv_00001" || fail "plan missing deliveries"
echo "$A" | grep -q "tampered" || fail "plan missing a tampered signature"

echo "4. sign matches the RFC 4231 HMAC-SHA256 vector"
SIG="$(printf 'what do ya want for nothing?' | "$HS" sign --secret Jefe --scheme hex)"
echo "$SIG" | grep -q "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843" \
  || fail "sign KAT mismatch"

echo "5. correct handler passes every check"
start_handler correct 18471
OUT="$("$HS" run --target http://127.0.0.1:18471/webhook \
  --receipts-url http://127.0.0.1:18471/receipts \
  --events 12 --seed 13 --bad-sig 0.3 --duplicates 0.5)"
echo "$OUT" | grep -q "verdict: PASS" || fail "correct handler did not pass:\n$OUT"
echo "$OUT" | grep -q "signatures-enforced" || fail "signatures check missing"

echo "6. signature bypass is caught (exit 1)"
start_handler no-sig-check 18472
set +e
"$HS" run --target http://127.0.0.1:18472/webhook \
  --receipts-url http://127.0.0.1:18472/receipts \
  --events 12 --seed 13 --bad-sig 0.4 >"$WORKDIR/hs_bypass.out" 2>&1
code=$?
set -e
[ "$code" -eq 1 ] || fail "signature bypass should exit 1, got $code"
grep -q "FAIL signatures-enforced" "$WORKDIR/hs_bypass.out" || fail "bypass not reported"

echo "7. non-idempotent handler fails the idempotency check"
start_handler non-idempotent 18473
set +e
NI="$("$HS" run --target http://127.0.0.1:18473/webhook \
  --receipts-url http://127.0.0.1:18473/receipts \
  --events 12 --seed 13 --duplicates 1 --max-duplicates 2 --bad-sig 0)"
code=$?
set -e
[ "$code" -eq 1 ] || fail "non-idempotent handler should exit 1, got $code"
echo "$NI" | grep -q "FAIL idempotent" || fail "idempotency bug not reported:\n$NI"

echo "8. flaky handler recovers under retries (exit 0)"
start_handler flaky 18474
"$HS" run --target http://127.0.0.1:18474/webhook \
  --events 10 --seed 7 --bad-sig 0 --max-retries 2 >/dev/null \
  || fail "flaky handler should pass once retries recover it"

echo "9. JSON output is machine-readable"
JSON="$("$HS" run --format json --target http://127.0.0.1:18474/webhook \
  --events 6 --seed 1 --bad-sig 0 --max-retries 2)"
echo "$JSON" | grep -q '"tool": "hookstorm"' || fail "json envelope missing"
echo "$JSON" | grep -q '"schema_version": 1' || fail "json schema_version missing"

echo "10. usage errors exit 2"
set +e
"$HS" run --events 4 >/dev/null 2>&1        # missing --target
[ $? -eq 2 ] || fail "missing target should exit 2"
"$HS" plan --format yaml >/dev/null 2>&1     # bad format
[ $? -eq 2 ] || fail "bad --format should exit 2"
set -e

rm -f "$WORKDIR/hs_bypass.out"
echo "SMOKE OK"
