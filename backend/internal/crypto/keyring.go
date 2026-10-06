package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrOpen hides why a sealed value failed to open (unknown key, bad AAD, tampering).
var ErrOpen = errors.New("crypto: cannot open sealed value")

// Keyring seals with the first (current) key and opens with any listed key, so
// keys rotate by prepending a new version (spec D7, scenario 19).
type Keyring struct {
	current string
	aeads   map[string]cipher.AEAD
}

// ParseKeyring parses TOKEN_ENC_KEYS: "v1:<b64 32B>[,v0:<b64 32B>…]", first = current.
func ParseKeyring(spec string) (*Keyring, error) {
	k := &Keyring{aeads: map[string]cipher.AEAD{}}
	for i, part := range strings.Split(spec, ",") {
		ver, b64, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok || ver == "" {
			return nil, fmt.Errorf("TOKEN_ENC_KEYS entry %d: want <version>:<base64 key>", i)
		}
		if _, dup := k.aeads[ver]; dup {
			return nil, fmt.Errorf("TOKEN_ENC_KEYS: duplicate version %q", ver)
		}
		key, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("TOKEN_ENC_KEYS %s: %w", ver, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("TOKEN_ENC_KEYS %s: key is %d bytes, want 32", ver, len(key))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		k.aeads[ver] = aead
		if i == 0 {
			k.current = ver
		}
	}
	return k, nil
}

// CurrentVersion is the key version new seals use.
func (k *Keyring) CurrentVersion() string { return k.current }

// Seal encrypts plain with a fresh 12-byte nonce (C18) and returns "<ver>:<b64(nonce‖ct)>".
func (k *Keyring) Seal(plain, aad []byte) (string, error) {
	aead := k.aeads[k.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := aead.Seal(nonce, nonce, plain, aad)
	return k.current + ":" + base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal. aad must match the value used when sealing (bot_id).
func (k *Keyring) Open(sealed string, aad []byte) ([]byte, error) {
	ver, b64, ok := strings.Cut(sealed, ":")
	aead, known := k.aeads[ver]
	if !ok || !known {
		return nil, ErrOpen
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrOpen
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}
