# Contributing to hookstorm

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22 and nothing else — hookstorm is standard-library only.

```bash
git clone https://github.com/JaydenCJ/hookstorm && cd hookstorm
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the CLI and the bundled reference handler, starts a
correct handler and several deliberately buggy ones on loopback, and asserts on
the real verdicts across every subcommand; it must finish by printing
`SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (89 deterministic tests, no external network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable modules
   (plan generation, signing, and analysis never touch the network — only the
   `deliver` package does).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in the PR.
- The delivery plan is a reproducibility contract: the same seed must always
  produce a byte-identical storm. Changes to `plan` or `rng` need a test
  pinning the new behaviour, and the golden `rng` vector must not move.
- New signature schemes go in `internal/sign` with a known-answer test and a
  row in `docs/signatures.md`; new correctness checks go in `internal/analyze`
  with a row in `docs/checks.md`.
- No network calls at startup, no telemetry. The only requests hookstorm makes
  are the deliveries you aim at your `--target` and the optional receipts GET.
- Code comments and doc comments are written in English.
- Determinism first: identical input must produce identical verdicts, and tests
  must never depend on wall-clock timing (inject the sleeper/clock instead).

## Reporting bugs

Include the output of `hookstorm version`, the exact command you ran (seed and
all flags — a storm is reproducible from them), and the verdict output. For a
misclassified check, the `--format json` output is the most useful, since it
carries every delivery outcome the analyzer saw.

## Security

Please do not open public issues for security problems; use GitHub's private
vulnerability reporting on this repository instead.
