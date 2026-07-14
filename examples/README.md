# hookstorm examples

A runnable reference handler and a CI-gate script, both offline and
self-contained.

## reference-handler

A configurable webhook receiver to aim hookstorm at, so you can watch it pass a
correct handler and fail a buggy one without writing a target first. Pick a bug
with `--mode`: `correct`, `no-sig-check`, `non-idempotent`, `flaky`, or
`lossy`. It exposes `POST /webhook` and `GET /receipts`.

```bash
# terminal 1: start a correct handler on loopback
go run ./examples/reference-handler --addr 127.0.0.1:8080 --mode correct

# terminal 2: storm it — all checks pass
hookstorm run --target http://127.0.0.1:8080/webhook \
  --receipts-url http://127.0.0.1:8080/receipts \
  --events 12 --seed 13

# now try a handler that forgot to verify signatures — signatures-enforced FAILs
go run ./examples/reference-handler --addr 127.0.0.1:8081 --mode no-sig-check
hookstorm run --target http://127.0.0.1:8081/webhook --events 12 --seed 13 --bad-sig 0.4
```

## ci-gate.sh

Shows `hookstorm run` as a policy gate: it starts a handler, storms it, and
exits non-zero if any correctness check fails — ready to drop into a pre-push
hook or a pipeline step.

```bash
bash examples/ci-gate.sh correct        ; echo "exit: $?"   # 0
bash examples/ci-gate.sh non-idempotent ; echo "exit: $?"   # 1
```

Both use pinned seeds and loopback only, so their output is identical on every
machine.
