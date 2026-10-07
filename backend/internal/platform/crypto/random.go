// Package crypto holds the app-level primitives from spec D7/C18/C20:
// versioned AES-256-GCM sealing, device-token HMAC and PKCE (RFC 7636).
package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

// RandomB64URL returns n cryptographically random bytes as unpadded base64url.
func RandomB64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isB64URLByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

func isB64URL(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isB64URLByte(s[i]) {
			return false
		}
	}
	return true
}
