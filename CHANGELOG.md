# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Deterministic storm planner: given a seed and a set of knobs, produces a
  byte-identical delivery plan of duplicates, reordered and slow deliveries,
  and wrong-key / tampered / missing signatures, built on a self-contained
  splitmix64 generator so a storm reproduces on any machine and Go version.
- HMAC-SHA256 signing and constant-time verification for three wire formats —
  `github` (`sha256=…`), `stripe` (`t=…,v1=…` over `t.body`), and `hex` —
  shared verbatim between the sender and the bundled reference handler.
- `run` subcommand: delivers a storm to a target over HTTP with a bounded
  worker pool, retries on 5xx / transport failure, optional per-delivery
  jitter, and an optional receipts endpoint; prints a per-check verdict in
  text or JSON (`schema_version: 1`) and exits 1 on failure for CI gating.
- Six black-box correctness checks — `signatures-enforced`, `handler-healthy`,
  `retries-recover`, and (when a receipts endpoint is supplied) `idempotent`,
  `no-loss`, and `no-spurious-processing` — each PASS/FAIL/SKIP with quoted
  evidence for failures.
- `plan` subcommand: prints the deterministic storm plan offline (no target),
  in text or JSON, for inspection and reproduction.
- `sign` subcommand: signs a body from stdin or `--body` and prints the header
  a provider would attach.
- A configurable reference handler (`examples/reference-handler`) with
  correct, no-sig-check, non-idempotent, flaky, and lossy modes, used by the
  tests, the examples, and `scripts/smoke.sh`.
- Signature-format and correctness-check references (`docs/signatures.md`,
  `docs/checks.md`) and a runnable CI-gate example (`examples/ci-gate.sh`).
- 89 deterministic offline tests (unit + in-process HTTP integration against
  the reference handler) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/hookstorm/releases/tag/v0.1.0
