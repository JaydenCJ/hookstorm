// Command reference-handler is a runnable webhook receiver you can point
// hookstorm at. It exists so you can watch hookstorm pass a correct handler
// and fail a buggy one without writing your own target first. Pick a bug with
// --mode:
//
//	correct         verifies signatures, de-duplicates by event id (all checks pass)
//	no-sig-check    skips signature verification (fails signatures-enforced)
//	non-idempotent  processes every accepted delivery (fails idempotent)
//	flaky           500s the first attempt of each delivery (retries recover it)
//	lossy           silently drops even-sequence events (fails no-loss)
//
// It listens on loopback by default and exposes GET /receipts so hookstorm's
// idempotency and loss checks can run.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/JaydenCJ/hookstorm/internal/refhandler"
	"github.com/JaydenCJ/hookstorm/internal/sign"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	secret := flag.String("secret", "whsec_hookstorm", "signing secret")
	scheme := flag.String("scheme", "github", "signature scheme: github, stripe, or hex")
	mode := flag.String("mode", "correct", "correct, no-sig-check, non-idempotent, flaky, or lossy")
	flag.Parse()

	m, ok := modeFromName(*mode)
	if !ok {
		fmt.Fprintf(os.Stderr, "reference-handler: unknown --mode %q (want correct, no-sig-check, non-idempotent, flaky, or lossy)\n", *mode)
		os.Exit(2)
	}
	if !sign.Valid(sign.Scheme(*scheme)) {
		fmt.Fprintf(os.Stderr, "reference-handler: unknown --scheme %q (want github, stripe, or hex)\n", *scheme)
		os.Exit(2)
	}

	h := refhandler.New([]byte(*secret), sign.Scheme(*scheme), m)
	log.Printf("reference-handler listening on http://%s  (mode=%s, scheme=%s)", *addr, *mode, *scheme)
	log.Printf("POST http://%s/webhook   GET http://%s/receipts", *addr, *addr)
	if err := http.ListenAndServe(*addr, h); err != nil {
		log.Fatalf("reference-handler: %v", err)
	}
}

func modeFromName(name string) (refhandler.Mode, bool) {
	switch name {
	case "correct":
		return refhandler.Correct(), true
	case "no-sig-check":
		return refhandler.Mode{VerifySignature: false, Idempotent: true}, true
	case "non-idempotent":
		return refhandler.Mode{VerifySignature: true, Idempotent: false}, true
	case "flaky":
		return refhandler.Mode{VerifySignature: true, Idempotent: true, FailFirstAttempt: true}, true
	case "lossy":
		return refhandler.Mode{VerifySignature: true, Idempotent: true, DropEvenSeq: true}, true
	default:
		return refhandler.Mode{}, false
	}
}
