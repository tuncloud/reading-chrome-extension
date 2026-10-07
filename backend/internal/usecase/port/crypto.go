package port

// Sealer encrypts Notion tokens at rest with AAD = bot_id (C18). *crypto.Keyring
// implements it.
type Sealer interface {
	Seal(plain, aad []byte) (string, error)
	Open(sealed string, aad []byte) ([]byte, error)
}
