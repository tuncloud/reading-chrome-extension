package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DeviceTokenPrefix marks extension bearer tokens: "rt_" + 43 base64url chars.
const DeviceTokenPrefix = "rt_"

// NewDeviceToken returns "rt_" + base64url(32 random bytes) (spec §2.3).
func NewDeviceToken() (string, error) {
	s, err := RandomB64URL(32)
	if err != nil {
		return "", err
	}
	return DeviceTokenPrefix + s, nil
}

// ValidDeviceTokenFormat rejects malformed bearer tokens before any DynamoDB read.
func ValidDeviceTokenFormat(tok string) bool {
	rest, ok := strings.CutPrefix(tok, DeviceTokenPrefix)
	return ok && len(rest) == 43 && isB64URL(rest)
}

// TokenHMAC is the only form a device token is stored in (C20).
func TokenHMAC(pepper []byte, token string) string {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}
