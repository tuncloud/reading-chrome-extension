package crypto

import (
	"strings"
	"testing"
)

// RFC 7636 Appendix B test vector.
const (
	rfcVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	rfcChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func TestS256ChallengeMatchesRFC(t *testing.T) {
	if got := S256Challenge(rfcVerifier); got != rfcChallenge {
		t.Fatalf("got %q", got)
	}
	if !ValidChallenge(rfcChallenge) {
		t.Fatal("RFC challenge should be valid")
	}
}

func TestVerifyPKCE(t *testing.T) {
	if !VerifyPKCE(rfcVerifier, rfcChallenge) {
		t.Fatal("RFC pair must verify")
	}
	if VerifyPKCE(strings.Repeat("a", 64), rfcChallenge) {
		t.Fatal("wrong verifier must fail")
	}
	if VerifyPKCE("short", S256Challenge("short")) {
		t.Fatal("verifier shorter than 43 chars must fail")
	}
}
