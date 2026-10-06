package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func mustKeyring(t *testing.T, spec string) *Keyring {
	t.Helper()
	k, err := ParseKeyring(spec)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := mustKeyring(t, "v1:"+testKey(1))
	sealed, err := k.Seal([]byte("secret_abc"), []byte("bot-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "secret_abc") {
		t.Fatalf("unexpected sealed form %q", sealed)
	}
	got, err := k.Open(sealed, []byte("bot-1"))
	if err != nil || string(got) != "secret_abc" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestOpenWithDifferentAADFails(t *testing.T) {
	k := mustKeyring(t, "v1:"+testKey(1))
	sealed, _ := k.Seal([]byte("secret"), []byte("bot-1"))
	if _, err := k.Open(sealed, []byte("bot-2")); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
}

func TestOldKeyOpensAfterRotation(t *testing.T) {
	old := mustKeyring(t, "v0:"+testKey(0))
	sealed, _ := old.Seal([]byte("secret"), []byte("bot-1"))

	rotated := mustKeyring(t, "v1:"+testKey(1)+",v0:"+testKey(0))
	got, err := rotated.Open(sealed, []byte("bot-1"))
	if err != nil || string(got) != "secret" {
		t.Fatalf("Open after rotation = %q, %v", got, err)
	}
	resealed, _ := rotated.Seal([]byte("secret"), []byte("bot-1"))
	if !strings.HasPrefix(resealed, "v1:") || rotated.CurrentVersion() != "v1" {
		t.Fatalf("new seals must use the first key, got %q", resealed)
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	k := mustKeyring(t, "v1:"+testKey(1))
	sealed, _ := k.Seal([]byte("secret"), []byte("bot-1"))
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	raw[len(raw)-1] ^= 0x01
	tampered := "v1:" + base64.StdEncoding.EncodeToString(raw)
	if _, err := k.Open(tampered, []byte("bot-1")); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	k := mustKeyring(t, "v1:"+testKey(1))
	a, _ := k.Seal([]byte("same"), []byte("bot-1"))
	b, _ := k.Seal([]byte("same"), []byte("bot-1"))
	if a == b {
		t.Fatal("two seals of the same plaintext must differ (C18 nonce reuse)")
	}
}

func TestParseKeyringRejectsBadInput(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	for _, spec := range []string{"", "v1", "v1:not*base64", "v1:" + short, "v1:" + testKey(1) + ",v1:" + testKey(2)} {
		if _, err := ParseKeyring(spec); err == nil {
			t.Errorf("ParseKeyring(%q) succeeded, want error", spec)
		}
	}
}

func TestOpenUnknownVersionFails(t *testing.T) {
	k := mustKeyring(t, "v1:"+testKey(1))
	if _, err := k.Open("v9:AAAA", []byte("bot-1")); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
}
