package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/JaydenCJ/hookstorm/internal/analyze"
	"github.com/JaydenCJ/hookstorm/internal/deliver"
	"github.com/JaydenCJ/hookstorm/internal/plan"
	"github.com/JaydenCJ/hookstorm/internal/report"
	"github.com/JaydenCJ/hookstorm/internal/sign"
)

// runPlan builds and prints the deterministic storm plan. It never touches the
// network, so it is the fastest way to see (and reproduce) exactly what a seed
// will do.
func runPlan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sf stormFlags
	sf.register(fs)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *format != "text" && *format != "json" {
		return unknownFormat(stderr, *format, "text or json")
	}
	p, err := plan.Build(sf.config())
	if err != nil {
		fmt.Fprintf(stderr, "hookstorm: %v\n", err)
		return ExitUsage
	}
	if *format == "json" {
		if err := report.PlanJSON(stdout, p); err != nil {
			fmt.Fprintf(stderr, "hookstorm: %v\n", err)
			return ExitRuntime
		}
		return ExitOK
	}
	report.PlanText(stdout, p)
	return ExitOK
}

// runStorm builds a plan, delivers it to --target, optionally fetches receipts,
// analyzes the outcomes, and prints the verdict. It exits 1 when the verdict
// fails so it can gate CI.
func runStorm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var sf stormFlags
	sf.register(fs)
	target := fs.String("target", "", "webhook endpoint URL to storm (required)")
	secret := fs.String("secret", "whsec_hookstorm", "signing secret")
	scheme := fs.String("scheme", string(sign.GitHub), "signature scheme: github, stripe, or hex")
	concurrency := fs.Int("concurrency", 4, "parallel delivery workers")
	maxRetries := fs.Int("max-retries", 2, "retries on 5xx / transport failure")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request timeout")
	receiptsURL := fs.String("receipts-url", "", "GET endpoint reporting processed events")
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *target == "" {
		fmt.Fprintf(stderr, "hookstorm run: --target URL is required\n")
		return ExitUsage
	}
	if *format != "text" && *format != "json" {
		return unknownFormat(stderr, *format, "text or json")
	}
	if !sign.Valid(sign.Scheme(*scheme)) {
		fmt.Fprintf(stderr, "hookstorm run: unknown --scheme %q (want github, stripe, or hex)\n", *scheme)
		return ExitUsage
	}

	p, err := plan.Build(sf.config())
	if err != nil {
		fmt.Fprintf(stderr, "hookstorm: %v\n", err)
		return ExitUsage
	}

	ctx := context.Background()
	outcomes, err := deliver.Run(ctx, p, deliver.Options{
		Target:      *target,
		Secret:      []byte(*secret),
		Scheme:      sign.Scheme(*scheme),
		Concurrency: *concurrency,
		MaxRetries:  *maxRetries,
		Timeout:     *timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "hookstorm: delivery failed: %v\n", err)
		return ExitRuntime
	}

	var receipts *analyze.Receipts
	if *receiptsURL != "" {
		receipts, err = fetchReceipts(ctx, *receiptsURL, *timeout)
		if err != nil {
			fmt.Fprintf(stderr, "hookstorm: fetching receipts: %v\n", err)
			return ExitRuntime
		}
	}

	res := analyze.Analyze(p, outcomes, receipts)

	if *format == "json" {
		if err := report.RunJSON(stdout, *target, p, outcomes, res); err != nil {
			fmt.Fprintf(stderr, "hookstorm: %v\n", err)
			return ExitRuntime
		}
	} else {
		report.RunText(stdout, *target, p, res)
	}
	if !res.Passed {
		return ExitBreach
	}
	return ExitOK
}

// fetchReceipts GETs the handler's receipts endpoint and decodes it. This is
// the only outbound request hookstorm makes that is not a delivery.
func fetchReceipts(ctx context.Context, url string, timeout time.Duration) (*analyze.Receipts, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("receipts endpoint returned %d", resp.StatusCode)
	}
	var r analyze.Receipts
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decoding receipts JSON: %w", err)
	}
	return &r, nil
}

// runSign is a small utility: sign a body (from --body or stdin) and print the
// header line a provider would attach. Handy for crafting a curl by hand.
func runSign(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	secret := fs.String("secret", "whsec_hookstorm", "signing secret")
	scheme := fs.String("scheme", string(sign.GitHub), "signature scheme: github, stripe, or hex")
	body := fs.String("body", "", "body to sign (default: read stdin)")
	timestamp := fs.Int64("timestamp", 0, "unix timestamp for the stripe scheme (default: now)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if !sign.Valid(sign.Scheme(*scheme)) {
		fmt.Fprintf(stderr, "hookstorm sign: unknown --scheme %q (want github, stripe, or hex)\n", *scheme)
		return ExitUsage
	}
	var raw []byte
	if *body != "" {
		raw = []byte(*body)
	} else {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(stderr, "hookstorm sign: reading stdin: %v\n", err)
			return ExitRuntime
		}
		raw = trimTrailingNewlines(b)
	}
	ts := *timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}
	name, value, err := sign.Compute(sign.Scheme(*scheme), []byte(*secret), raw, ts)
	if err != nil {
		fmt.Fprintf(stderr, "hookstorm sign: %v\n", err)
		return ExitRuntime
	}
	fmt.Fprintf(stdout, "%s: %s\n", name, value)
	return ExitOK
}
