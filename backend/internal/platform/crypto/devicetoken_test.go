package crypto

import (
	"strings"
	"testing"
)

func TestNewDeviceTokenFormat(t *testing.T) {
	a, err := NewDeviceToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewDeviceToken()
	if !strings.HasPrefix(a, "rt_") || len(a) != 46 || !ValidDeviceTokenFormat(a) {
		t.Fatalf("bad token %q", a)
	}
	if a == b {
		t.Fatal("tokens must be random")
	}
}

func TestValidDeviceTokenFormatRejects(t *testing.T) {
	for _, tok := range []string{"", "rt_short", "xx_" + strings.Repeat("a", 43), "rt_" + strings.Repeat("a", 42) + "!"} {
		if ValidDeviceTokenFormat(tok) {
			t.Errorf("%q accepted", tok)
		}
	}
}

func TestTokenHMAC(t *testing.T) {
	p1, p2 := []byte(strings.Repeat("p", 32)), []byte(strings.Repeat("q", 32))
	h := TokenHMAC(p1, "rt_x")
	if len(h) != 64 || h != TokenHMAC(p1, "rt_x") {
		t.Fatalf("HMAC must be 64 hex chars and deterministic, got %q", h)
	}
	if h == TokenHMAC(p2, "rt_x") {
		t.Fatal("HMAC must depend on the pepper")
	}
}
