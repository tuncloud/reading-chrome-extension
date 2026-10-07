package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// logout serves POST /v1/auth/logout (spec §2.17).
func (h *handlers) logout(c echo.Context) error {
	p := principal(c)
	if err := h.Store.RevokeDevice(c.Request().Context(), p.UserID, p.Device.ID, p.TokenHMAC, h.Now()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// deleteMe serves DELETE /v1/me (spec §2.17): enqueue the purge, mark the profile
// deleted, and revoke every device. Notion content is never deleted.
//
// The order keeps every failure retryable. Nothing irreversible happens before the
// purge is queued, so a failed Send leaves the account untouched and the client retries.
// If marking fails after Send, the worker acks the purge as a live user and the retry
// re-sends and marks. Once the profile is marked the middleware rejects the user, so a
// failed revoke still locks them out and the purge deletes the devices and tokens anyway.
// This relies on the queue's delivery delay (at least the 120 s debounce bucket) exceeding
// this handler's runtime, so the worker never sees the purge before deleted_at commits.
func (h *handlers) deleteMe(c echo.Context) error {
	ctx := c.Request().Context()
	p := principal(c)
	now := h.Now()
	devices, err := h.Store.ListDevices(ctx, p.UserID)
	if err != nil {
		return err
	}
	if err := h.sq.EnqueuePurge(ctx, p.UserID, p.InstallationID()); err != nil {
		return err
	}
	if err := h.Store.MarkProfileDeleted(ctx, p.UserID, now); err != nil {
		return err
	}
	for _, d := range devices {
		if d.RevokedAt != "" {
			continue
		}
		if err := h.Store.RevokeDevice(ctx, p.UserID, d.ID, d.TokenHMAC, now); err != nil {
			return err
		}
	}
	return c.NoContent(http.StatusAccepted)
}
