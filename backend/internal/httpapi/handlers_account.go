package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/queue"
)

// logout serves POST /v1/auth/logout (spec §2.17).
func (h *handlers) logout(c echo.Context) error {
	p := principal(c)
	if err := h.Store.RevokeDevice(c.Request().Context(), p.UserID, p.Device.ID(), p.TokenHMAC, h.Now()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// deleteMe serves DELETE /v1/me (spec §2.17): revoke every device, mark the
// profile deleted, and let the worker purge the data. Notion content is never deleted.
func (h *handlers) deleteMe(c echo.Context) error {
	ctx := c.Request().Context()
	p := principal(c)
	now := h.Now()
	devices, err := h.Store.ListDevices(ctx, p.UserID)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if d.RevokedAt != "" {
			continue
		}
		if err := h.Store.RevokeDevice(ctx, p.UserID, d.ID(), d.TokenHMAC, now); err != nil {
			return err
		}
	}
	if err := h.Store.MarkProfileDeleted(ctx, p.UserID, now); err != nil {
		return err
	}
	msg := queue.Purge(p.UserID, p.InstallationID())
	if err := h.Queue.Send(ctx, queue.Outgoing{Message: msg, DedupID: msg.DedupID(now, h.Config.DebounceBucket)}); err != nil {
		return err
	}
	return c.NoContent(http.StatusAccepted)
}
