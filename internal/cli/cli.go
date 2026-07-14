// Package cli implements the hookstorm command-line interface. Run takes argv
// and two writers and returns an exit code, so the entire surface is testable
// in-process against an httptest server without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/hookstorm/internal/plan"
	"github.com/JaydenCJ/hookstorm/internal/version"
)

// Exit codes. `run` returns ExitBreach when the verdict fails, so it works as a
// CI/pre-push policy gate.
const (
	ExitOK      = 0
	ExitBreach  = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "run":
		return runStorm(args[1:], stdout, stderr)
	case "plan":
		return runPlan(args[1:], stdout, stderr)
	case "sign":
		return runSign(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "hookstorm %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "hookstorm: unknown command %q\n\n", args[0])
		usage(stderr)
		return ExitUsage
	}
}

// stormFlags are the knobs shared by `run` and `plan` that shape the storm.
type stormFlags struct {
	events        int
	seed          uint64
	duplicates    float64
	maxDuplicates int
	badSig        float64
	missing       float64
	reorderWindow int
	maxDelayMs    int
}

func (s *stormFlags) register(fs *flag.FlagSet) {
	fs.IntVar(&s.events, "events", 12, "number of logical events in the storm")
	fs.Uint64Var(&s.seed, "seed", 1, "seed for the deterministic storm (same seed = same storm)")
	fs.Float64Var(&s.duplicates, "duplicates", 0.3, "probability an event gets extra (duplicate) deliveries [0,1]")
	fs.IntVar(&s.maxDuplicates, "max-duplicates", 2, "cap on extra deliveries per duplicated event")
	fs.Float64Var(&s.badSig, "bad-sig", 0.2, "fraction of deliveries given a broken signature [0,1]")
	fs.Float64Var(&s.missing, "missing", 0.34, "fraction of broken signatures that omit the header entirely [0,1]")
	fs.IntVar(&s.reorderWindow, "reorder-window", 4, "shuffle deliveries within windows of this size (<=1 keeps order)")
	fs.IntVar(&s.maxDelayMs, "max-delay-ms", 0, "upper bound on per-delivery jitter in milliseconds")
}

func (s *stormFlags) config() plan.Config {
	return plan.Config{
		Events:        s.events,
		Seed:          s.seed,
		DuplicateRate: s.duplicates,
		MaxDuplicates: s.maxDuplicates,
		BadSigRate:    s.badSig,
		MissingRate:   s.missing,
		ReorderWindow: s.reorderWindow,
		MaxDelayMs:    s.maxDelayMs,
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `hookstorm %s — stress-test a webhook handler with real delivery semantics

Usage:
  hookstorm run   --target URL [flags]   run a storm and print the verdict (exit 1 on failure)
  hookstorm plan  [flags]                print the deterministic delivery plan (no network)
  hookstorm sign  [flags]                sign a body from stdin (utility)
  hookstorm version                      print the version

Storm flags (run and plan):
  --events N            number of logical events (default 12)
  --seed N              seed; same seed reproduces the storm exactly (default 1)
  --duplicates R        P(an event gets extra deliveries) in [0,1] (default 0.3)
  --max-duplicates N    cap on extra deliveries per event (default 2)
  --bad-sig R           fraction of deliveries signed wrong, [0,1] (default 0.2)
  --missing R           fraction of bad signatures with no header, [0,1] (default 0.34)
  --reorder-window N    shuffle deliveries within windows of N (default 4)
  --max-delay-ms N      per-delivery jitter upper bound in ms (default 0)

Run flags:
  --secret S            signing secret (default "whsec_hookstorm")
  --scheme S            signature scheme: github, stripe, or hex (default github)
  --concurrency N       parallel delivery workers (default 4)
  --max-retries N       retries on 5xx / transport failure (default 2)
  --timeout DUR         per-request timeout, e.g. 5s (default 10s)
  --receipts-url URL    GET endpoint reporting processed events (enables idempotency checks)
  --format FORMAT       text (default) or json

Sign flags:
  --secret S            signing secret (default "whsec_hookstorm")
  --scheme S            github, stripe, or hex (default github)
  --body S              body to sign (default: read stdin)
  --timestamp N         unix timestamp for the stripe scheme (default: now)

Exit codes: 0 ok · 1 verdict failed · 2 usage error · 3 runtime error
`, version.Version)
}

// unknownFormat is a small shared error message helper.
func unknownFormat(stderr io.Writer, got string, allowed string) int {
	fmt.Fprintf(stderr, "hookstorm: unknown --format %q (want %s)\n", got, allowed)
	return ExitUsage
}

// trimTrailingNewlines is used by `sign` to sign the body a caller piped in
// without an accidental trailing newline changing the MAC.
func trimTrailingNewlines(b []byte) []byte {
	return []byte(strings.TrimRight(string(b), "\n"))
}
