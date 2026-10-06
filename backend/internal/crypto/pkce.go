package crypto

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// S256Challenge returns base64url(SHA-256(verifier)) without padding (RFC 7636 §4.2).
func S256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ValidVerifier: 43–128 chars of [A-Za-z0-9-._~] (RFC 7636 §4.1).
func ValidVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; !isB64URLByte(c) && c != '.' && c != '~' {
			return false
		}
	}
	return true
}

// ValidChallenge: an S256 challenge is exactly 43 base64url chars.
func ValidChallenge(c string) bool { return len(c) == 43 && isB64URL(c) }

// VerifyPKCE checks the verifier against the stored challenge in constant time.
func VerifyPKCE(verifier, challenge string) bool {
	if !ValidVerifier(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(S256Challenge(verifier)), []byte(challenge)) == 1
}
