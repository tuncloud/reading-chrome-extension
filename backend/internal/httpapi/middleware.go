package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
)

const principalKey = "principal"

// Principal is the authenticated device and its user.
type Principal struct {
	UserID    string
	TokenHMAC string
	Device    *domain.Device
	Profile   *domain.User
}

// InstallationID is the user's active installation (one per user in v1), or "".
func (p *Principal) InstallationID() string { return p.Profile.ActiveInstallationID }

func principal(c echo.Context) *Principal { return c.Get(principalKey).(*Principal) }

// requireDevice authenticates "Authorization: Bearer rt_…" (spec §2.4).
func (h *handlers) requireDevice(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tok, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
		if !ok || !crypto.ValidDeviceTokenFormat(tok) {
			return errUnauthorized()
		}
		ctx := c.Request().Context()
		mac := crypto.TokenHMAC(h.Config.DeviceTokenPepper, tok)
		ref, err := h.Store.GetDeviceToken(ctx, mac)
		if errors.Is(err, domain.ErrNotFound) {
			return errUnauthorized()
		}
		if err != nil {
			return err
		}
		dev, err := h.Store.GetDevice(ctx, ref.UserID, ref.DeviceID)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && dev.RevokedAt != "") {
			return errUnauthorized()
		}
		if err != nil {
			return err
		}
		prof, err := h.Store.GetProfile(ctx, ref.UserID)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && prof.DeletedAt != "") {
			return errUnauthorized()
		}
		if err != nil {
			return err
		}
		h.touch(ctx, ref.UserID, dev)
		c.Set(principalKey, &Principal{UserID: ref.UserID, TokenHMAC: mac, Device: dev, Profile: prof})
		return next(c)
	}
}

// touch updates last_seen_at at most hourly. The in-memory check skips the write
// (and its WCU) on most requests; the conditional update closes races.
func (h *handlers) touch(ctx context.Context, uid string, dev *domain.Device) {
	now := h.Now()
	if seen, err := time.Parse(time.RFC3339, dev.LastSeenAt); err == nil && now.Sub(seen) < time.Hour {
		return
	}
	if err := h.Store.TouchDevice(ctx, uid, dev.ID, now); err != nil {
		h.Log.WarnContext(ctx, "auth: touch device failed", "err", err)
	}
}

// requestLogger logs method, path, status and latency. Never the query string
// (OAuth codes/state), headers or bodies (C13).
func requestLogger(log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			status := c.Response().Status
			if err != nil {
				status = toAPIError(err).status
			}
			log.InfoContext(c.Request().Context(), "request", "method", c.Request().Method,
				"path", c.Request().URL.Path, "status", status, "ms", time.Since(start).Milliseconds())
			return err
		}
	}
}
