// End-to-end tests of the CLI, driven in-process: Run is called with argv and
// captured writers, exactly as main() would, and for `run` the --target points
// at an in-process httptest server built from the bundled reference handler.
// Everything is loopback and deterministic — no external network, no sleeps.
package cli

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JaydenCJ/hookstorm/internal/refhandler"
	"github.com/JaydenCJ/hookstorm/internal/sign"
)

// invoke runs the CLI and returns exit code, stdout, and stderr.
func invoke(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func serve(t *testing.T, mode refhandler.Mode) (webhook, receipts string) {
	t.Helper()
	h := refhandler.New([]byte("whsec_hookstorm"), sign.GitHub, mode)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + "/webhook", srv.URL + "/receipts"
}

func TestVersionMatchesManifest(t *testing.T) {
	code, out, _ := invoke("version")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(out) != "hookstorm 0.1.0" {
		t.Fatalf("version output = %q", out)
	}
}

func TestNoArgsIsUsageError(t *testing.T) {
	code, _, errb := invoke()
	if code != ExitUsage {
		t.Fatalf("exit %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errb, "Usage:") {
		t.Fatal("usage text should go to stderr")
	}
}

func TestPlanIsOfflineAndDeterministic(t *testing.T) {
	code1, out1, _ := invoke("plan", "--events", "8", "--seed", "13", "--bad-sig", "0.35", "--duplicates", "0.5")
	code2, out2, _ := invoke("plan", "--events", "8", "--seed", "13", "--bad-sig", "0.35", "--duplicates", "0.5")
	if code1 != ExitOK || code2 != ExitOK {
		t.Fatalf("plan exit codes %d %d", code1, code2)
	}
	if out1 != out2 {
		t.Fatal("plan output is not deterministic across runs")
	}
	if !strings.Contains(out1, "dlv_00001") {
		t.Fatal("plan output missing deliveries")
	}
}

func TestPlanRejectsBadFormat(t *testing.T) {
	code, _, _ := invoke("plan", "--format", "yaml")
	if code != ExitUsage {
		t.Fatalf("bad format exit %d, want %d", code, ExitUsage)
	}
}

func TestPlanRejectsInvalidRate(t *testing.T) {
	code, _, errb := invoke("plan", "--duplicates", "2")
	if code != ExitUsage {
		t.Fatalf("invalid rate exit %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errb, "duplicate-rate") {
		t.Fatalf("expected a rate error, got %q", errb)
	}
}

func TestRunAgainstCorrectHandlerPasses(t *testing.T) {
	webhook, receipts := serve(t, refhandler.Correct())
	code, out, _ := invoke("run",
		"--target", webhook, "--receipts-url", receipts,
		"--events", "8", "--seed", "13", "--bad-sig", "0.35", "--duplicates", "0.5")
	if code != ExitOK {
		t.Fatalf("correct handler run exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "verdict: PASS") {
		t.Fatalf("expected PASS verdict:\n%s", out)
	}
}

func TestRunAgainstBuggyHandlerFailsWithExit1(t *testing.T) {
	webhook, receipts := serve(t, refhandler.Mode{VerifySignature: true, Idempotent: false})
	code, out, _ := invoke("run",
		"--target", webhook, "--receipts-url", receipts,
		"--events", "8", "--seed", "13", "--duplicates", "1", "--max-duplicates", "2", "--bad-sig", "0")
	if code != ExitBreach {
		t.Fatalf("buggy handler run exit %d, want %d\n%s", code, ExitBreach, out)
	}
	if !strings.Contains(out, "verdict: FAIL") || !strings.Contains(out, "idempotent") {
		t.Fatalf("expected an idempotency failure:\n%s", out)
	}
}

func TestRunJSONOutputAndBypassDetection(t *testing.T) {
	webhook, receipts := serve(t, refhandler.Mode{VerifySignature: false, Idempotent: true})
	code, out, _ := invoke("run", "--format", "json",
		"--target", webhook, "--receipts-url", receipts,
		"--events", "10", "--seed", "13", "--bad-sig", "0.5")
	if code != ExitBreach {
		t.Fatalf("signature bypass should exit %d, got %d\n%s", ExitBreach, code, out)
	}
	if !strings.Contains(out, `"passed": false`) || !strings.Contains(out, "signatures-enforced") {
		t.Fatalf("json verdict missing bypass:\n%s", out)
	}
}

func TestRunRequiresTarget(t *testing.T) {
	code, _, errb := invoke("run", "--events", "4")
	if code != ExitUsage {
		t.Fatalf("missing target exit %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errb, "--target") {
		t.Fatalf("expected target error, got %q", errb)
	}
}

func TestRunRejectsUnknownScheme(t *testing.T) {
	code, _, _ := invoke("run", "--target", "http://127.0.0.1:9/webhook", "--scheme", "hmac-md5")
	if code != ExitUsage {
		t.Fatalf("bad scheme exit %d, want %d", code, ExitUsage)
	}
}

func TestRunReceiptsUnreachableIsRuntimeError(t *testing.T) {
	webhook, _ := serve(t, refhandler.Correct())
	// A receipts URL on a port nothing is listening on: connection refused.
	code, _, errb := invoke("run",
		"--target", webhook, "--receipts-url", "http://127.0.0.1:1/receipts",
		"--events", "2", "--seed", "1", "--bad-sig", "0")
	if code != ExitRuntime {
		t.Fatalf("unreachable receipts exit %d, want %d", code, ExitRuntime)
	}
	if !strings.Contains(errb, "receipts") {
		t.Fatalf("expected a receipts error, got %q", errb)
	}
}

func TestSignSubcommandMatchesKnownVector(t *testing.T) {
	// RFC 4231 test case 2 again, straight through the CLI.
	code, out, _ := invoke("sign", "--secret", "Jefe", "--scheme", "hex", "--body", "what do ya want for nothing?")
	if code != ExitOK {
		t.Fatalf("sign exit %d", code)
	}
	want := "X-Signature: 5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
	if strings.TrimSpace(out) != want {
		t.Fatalf("sign output = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestSignRejectsUnknownScheme(t *testing.T) {
	code, _, _ := invoke("sign", "--scheme", "rot13", "--body", "x")
	if code != ExitUsage {
		t.Fatalf("bad sign scheme exit %d, want %d", code, ExitUsage)
	}
}
