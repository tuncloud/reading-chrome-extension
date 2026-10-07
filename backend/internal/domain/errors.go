package domain

import "errors"

var (
	// ErrNotFound: the requested item does not exist (or a TTL item has expired, C12).
	ErrNotFound = errors.New("not found")
	// ErrConditionFailed: a guarded write lost a race (e.g. token_version, C7).
	ErrConditionFailed = errors.New("condition failed")
	// ErrThrottled: storage capacity was exceeded; the caller should retry later (spec §2.0 503).
	ErrThrottled = errors.New("storage throttled")
)

// SetupError is why an installation's Notion databases could not be resolved.
// Reason is stored in the installation's setup_error (spec §2.11 step 4).
type SetupError struct{ Reason string }

func (e *SetupError) Error() string { return "setup: " + e.Reason }

// SetupReason returns the setup_error for err; unexpected errors map to "notion_error".
func SetupReason(err error) string {
	var e *SetupError
	if errors.As(err, &e) {
		return e.Reason
	}
	return "notion_error"
}
