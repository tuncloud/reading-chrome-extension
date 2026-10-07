package domain

// User is a person who connected Notion (spec §3.1 PROFILE).
type User struct {
	ID                   string
	NotionUserID         string
	Name                 string
	Email                string
	ActiveInstallationID string
	CreatedAt            string
	DeletedAt            string
}

// Deleted reports whether account deletion is pending.
func (u User) Deleted() bool { return u.DeletedAt != "" }

// Device is one extension install. Its bearer token is kept only as TokenHMAC (C20).
type Device struct {
	ID         string
	UserID     string
	Name       string
	TokenHMAC  string
	CreatedAt  string
	LastSeenAt string
	RevokedAt  string
}

// Revoked reports whether the device was logged out or its account deleted.
func (d Device) Revoked() bool { return d.RevokedAt != "" }

// DeviceToken resolves a token HMAC to its device.
type DeviceToken struct {
	UserID   string
	DeviceID string
}

// OAuthState is a pending Notion authorization (single use, spec §2.1).
type OAuthState struct {
	ExtRedirect   string
	CodeChallenge string
}

// AuthCode is the one-time code the extension redeems for a device token (spec §2.2–§2.3).
type AuthCode struct {
	UserID         string
	InstallationID string
	CodeChallenge  string
	ExtRedirect    string
}

// maxOneTimeCodeLen bounds OAuth state and auth codes (ours are 43 chars) before they
// are used as storage keys: an oversized key is a storage error, not "expired".
const maxOneTimeCodeLen = 128

// PlausibleOneTimeCode reports whether s could be one of our state or auth code values.
func PlausibleOneTimeCode(s string) bool { return s != "" && len(s) <= maxOneTimeCodeLen }
