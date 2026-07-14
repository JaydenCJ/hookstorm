// Tests for signature computation and verification. A wrong signature here is
// a real security bug: hookstorm's whole signatures-enforced check trusts that
// a "bad" signature is bad the same way a provider's tampered one would be.
package sign

import "testing"

// RFC 4231 HMAC-SHA256 Test Case 2 is an independent, well-known vector: it
// proves our MAC is real HMAC-SHA256, not something that merely round-trips
// with itself.
const (
	rfcKey  = "Jefe"
	rfcData = "what do ya want for nothing?"
	rfcMAC  = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
)

func TestHexSchemeMatchesRFC4231Vector(t *testing.T) {
	_, value, err := Compute(Hex, []byte(rfcKey), []byte(rfcData), 0)
	if err != nil {
		t.Fatal(err)
	}
	if value != rfcMAC {
		t.Fatalf("hex MAC = %s, want %s", value, rfcMAC)
	}
}

func TestGitHubSchemePrefixesTheSameDigest(t *testing.T) {
	_, value, err := Compute(GitHub, []byte(rfcKey), []byte(rfcData), 0)
	if err != nil {
		t.Fatal(err)
	}
	if value != "sha256="+rfcMAC {
		t.Fatalf("github value = %s, want sha256=%s", value, rfcMAC)
	}
}

func TestHeaderNames(t *testing.T) {
	cases := map[Scheme]string{
		GitHub: "X-Hub-Signature-256",
		Stripe: "Stripe-Signature",
		Hex:    "X-Signature",
	}
	for s, want := range cases {
		if got := Header(s); got != want {
			t.Fatalf("Header(%s) = %s, want %s", s, got, want)
		}
	}
}

func TestComputeRejectsUnknownScheme(t *testing.T) {
	if _, _, err := Compute("hmac-md5", []byte("k"), []byte("b"), 0); err == nil {
		t.Fatal("expected error for unknown scheme")
	}
}

func TestValidAndSchemesList(t *testing.T) {
	if !Valid(GitHub) || !Valid(Stripe) || !Valid(Hex) {
		t.Fatal("known schemes must be Valid")
	}
	if Valid("nope") {
		t.Fatal("unknown scheme reported Valid")
	}
	if len(Schemes) != 3 {
		t.Fatalf("Schemes has %d entries, want 3", len(Schemes))
	}
}

func TestVerifyAcceptsOwnGitHubSignature(t *testing.T) {
	secret, body := []byte("whsec_test"), []byte(`{"id":"evt_1"}`)
	_, value, _ := Compute(GitHub, secret, body, 0)
	if !Verify(GitHub, secret, body, value) {
		t.Fatal("Verify rejected a signature it just produced")
	}
}

func TestVerifyAcceptsOwnStripeSignature(t *testing.T) {
	secret, body := []byte("whsec_test"), []byte(`{"id":"evt_1"}`)
	_, value, _ := Compute(Stripe, secret, body, 1750000000)
	if !Verify(Stripe, secret, body, value) {
		t.Fatal("Verify rejected its own stripe signature")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	_, value, _ := Compute(GitHub, []byte("right"), body, 0)
	if Verify(GitHub, []byte("wrong"), body, value) {
		t.Fatal("Verify accepted a signature made with a different key")
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	secret := []byte("whsec_test")
	_, value, _ := Compute(GitHub, secret, []byte(`{"amount":100}`), 0)
	if Verify(GitHub, secret, []byte(`{"amount":999}`), value) {
		t.Fatal("Verify accepted a signature over a different body")
	}
}

func TestStripeVerifyIsBoundToTimestamp(t *testing.T) {
	// The Stripe signature covers "t.body"; the same v1 digest under a
	// different t must not verify, which is the whole point of the scheme.
	secret, body := []byte("whsec_test"), []byte(`{"id":"evt_1"}`)
	_, value, _ := Compute(Stripe, secret, body, 1750000000)
	tampered := "t=1750000999," + value[len("t=1750000000,"):]
	if Verify(Stripe, secret, body, tampered) {
		t.Fatal("Verify accepted a stripe signature with a swapped timestamp")
	}
}

func TestVerifyRejectsMalformedStripeHeader(t *testing.T) {
	secret, body := []byte("k"), []byte("b")
	for _, bad := range []string{"garbage", "t=abc,v1=deadbeef", "v1=deadbeef", "t=1"} {
		if Verify(Stripe, secret, body, bad) {
			t.Fatalf("stripe Verify accepted malformed header %q", bad)
		}
	}
}

func TestEmptyBodySignsAndVerifies(t *testing.T) {
	secret := []byte("whsec_test")
	for _, s := range Schemes {
		_, value, err := Compute(s, secret, []byte{}, 100)
		if err != nil {
			t.Fatalf("Compute(%s, empty) errored: %v", s, err)
		}
		if !Verify(s, secret, []byte{}, value) {
			t.Fatalf("Verify(%s) failed on an empty body", s)
		}
	}
}

func TestUnicodeBodyIsHandledByteExact(t *testing.T) {
	secret := []byte("whsec_test")
	body := []byte(`{"note":"日本語 · émoji 🎯"}`)
	_, value, _ := Compute(GitHub, secret, body, 0)
	if !Verify(GitHub, secret, body, value) {
		t.Fatal("unicode body failed to verify against its own signature")
	}
}
