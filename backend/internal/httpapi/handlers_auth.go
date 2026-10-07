package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

type tokenRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
	DeviceName   string `json:"device_name"`
}

// authToken serves POST /v1/auth/token (spec §2.3).
func (h *handlers) authToken(c echo.Context) error {
	var req tokenRequest
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	g, err := h.Auth.RedeemCode(c.Request().Context(), req.Code, req.CodeVerifier, req.DeviceName)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"device_token": g.DeviceToken,
		"device_id":    g.DeviceID,
		"user":         userView{ID: g.User.ID, Name: g.User.Name},
		"installation": viewInstallation(g.Installation),
	})
}
