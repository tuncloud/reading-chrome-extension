package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/oklog/ulid/v2"

	"reading-tracker/backend/internal/crypto"
	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/textutil"
)

type tokenRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	DeviceName   string `json:"device_name"`
}

// authToken serves POST /v1/auth/token (spec §2.3). The code is consumed before the
// PKCE check, so a wrong verifier burns it.
func (h *handlers) authToken(c echo.Context) error {
	ctx := c.Request().Context()
	var req tokenRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	now := h.Now()
	ac, err := h.Store.ConsumeAuthCode(ctx, req.Code, now)
	if errors.Is(err, store.ErrNotFound) {
		return errInvalidGrant()
	}
	if err != nil {
		return err
	}
	if !crypto.VerifyPKCE(req.CodeVerifier, ac.CodeChallenge) {
		return errInvalidGrant()
	}
	token, err := crypto.NewDeviceToken()
	if err != nil {
		return err
	}
	deviceID := ulid.Make().String()
	dev := store.Device{Name: deviceName(req.DeviceName), TokenHMAC: crypto.TokenHMAC(h.Config.DeviceTokenPepper, token),
		CreatedAt: store.Timestamp(now), LastSeenAt: store.Timestamp(now)}
	if err := h.Store.CreateDevice(ctx, ac.UserID, deviceID, dev); err != nil {
		return err
	}
	prof, err := h.Store.GetProfile(ctx, ac.UserID)
	if err != nil {
		return err
	}
	inst, err := h.Store.GetInstallation(ctx, ac.InstallationID)
	if errors.Is(err, store.ErrNotFound) {
		inst, err = nil, nil
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"device_token": token,
		"device_id":    deviceID,
		"user":         userView{ID: ac.UserID, Name: prof.Name},
		"installation": viewInstallation(inst),
	})
}

func deviceName(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "Unknown device"
	}
	return textutil.Truncate(s, 100)
}
