// Package sign computes and verifies the HMAC-SHA256 signatures that real
// webhook providers attach to their deliveries. hookstorm uses it to sign the
// deliveries it sends (correctly, or deliberately wrong), and the bundled
// reference handler uses the exact same code to verify them — so a "bad
// signature" in a storm is bad in precisely the way a provider's would be.
//
// Three wire formats are supported, covering the shapes almost every provider
// uses in practice:
//
//	github  X-Hub-Signature-256: sha256=<hex>            HMAC over the raw body
//	stripe  Stripe-Signature: t=<unix>,v1=<hex>          HMAC over "t.body"
//	hex     X-Signature: <hex>                           HMAC over the raw body
//
// All comparisons are constant-time (hmac.Equal), so a handler built on this
// package cannot leak the expected signature through a timing side channel.
package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Scheme names a signature wire format.
type Scheme string

const (
	// GitHub is the "sha256=<hex>" style used by GitHub, Shopify, and many
	// clones. The signed payload is the raw request body.
	GitHub Scheme = "github"
	// Stripe binds the signature to a timestamp: the signed payload is
	// "<timestamp>.<body>", and the header carries both fields. This defeats
	// replay of an old body against a new timestamp.
	Stripe Scheme = "stripe"
	// Hex is a bare lowercase-hex HMAC of the body, with no scheme prefix.
	Hex Scheme = "hex"
)

// Schemes lists every supported scheme, for CLI validation and help text.
var Schemes = []Scheme{GitHub, Stripe, Hex}

// Valid reports whether s is a scheme hookstorm understands.
func Valid(s Scheme) bool {
	for _, k := range Schemes {
		if s == k {
			return true
		}
	}
	return false
}

// Header returns the HTTP header name a scheme writes its signature to.
func Header(s Scheme) string {
	switch s {
	case GitHub:
		return "X-Hub-Signature-256"
	case Stripe:
		return "Stripe-Signature"
	default:
		return "X-Signature"
	}
}

// mac returns the lowercase-hex HMAC-SHA256 of payload under secret.
func mac(secret, payload []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// signedPayload returns the exact bytes a scheme runs the MAC over. For Stripe
// that is "timestamp.body"; for the others it is the body verbatim.
func signedPayload(s Scheme, body []byte, ts int64) []byte {
	if s == Stripe {
		prefix := strconv.FormatInt(ts, 10) + "."
		return append([]byte(prefix), body...)
	}
	return body
}

// Compute returns the header name and value that sign body under secret for a
// scheme. ts is the unix timestamp; it is ignored by every scheme except
// Stripe, which embeds it in the header.
func Compute(s Scheme, secret, body []byte, ts int64) (name, value string, err error) {
	if !Valid(s) {
		return "", "", fmt.Errorf("sign: unknown scheme %q", s)
	}
	digest := mac(secret, signedPayload(s, body, ts))
	switch s {
	case GitHub:
		return Header(s), "sha256=" + digest, nil
	case Stripe:
		return Header(s), fmt.Sprintf("t=%d,v1=%s", ts, digest), nil
	default:
		return Header(s), digest, nil
	}
}

// parseStripe pulls the timestamp and the (first) v1 signature out of a Stripe
// header value. Unknown comma-separated fields are ignored, matching Stripe's
// forward-compatible format.
func parseStripe(headerValue string) (ts string, v1 string, ok bool) {
	for _, part := range strings.Split(headerValue, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			if v1 == "" { // first v1 wins; providers may list several
				v1 = kv[1]
			}
		}
	}
	return ts, v1, ts != "" && v1 != ""
}

// Verify reports whether headerValue is a valid signature for body under
// secret in scheme s. It never returns an error: any malformed header, wrong
// key, or tampered body is simply a false, which is exactly how a hardened
// handler should treat all of them.
func Verify(s Scheme, secret, body []byte, headerValue string) bool {
	if headerValue == "" || !Valid(s) {
		return false
	}
	switch s {
	case GitHub:
		got, ok := strings.CutPrefix(headerValue, "sha256=")
		if !ok {
			return false
		}
		return equalHex(got, mac(secret, body))
	case Stripe:
		tsStr, v1, ok := parseStripe(headerValue)
		if !ok {
			return false
		}
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			return false
		}
		return equalHex(v1, mac(secret, signedPayload(Stripe, body, ts)))
	default:
		return equalHex(headerValue, mac(secret, body))
	}
}

// equalHex is a constant-time comparison of two hex strings. hmac.Equal keeps
// the comparison independent of where the first differing byte is.
func equalHex(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
