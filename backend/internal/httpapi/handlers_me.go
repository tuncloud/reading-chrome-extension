package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// getConfig serves GET /v1/config (spec §2.10).
func (h *handlers) getConfig(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "max-age=3600")
	return c.JSON(http.StatusOK, h.Config.Public)
}

// getMe serves GET /v1/me (spec §2.5).
func (h *handlers) getMe(c echo.Context) error {
	p := principal(c)
	inst, err := h.activeInstallation(c.Request().Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user":         userView{ID: p.UserID, Name: p.Profile.Name},
		"device":       deviceView{ID: p.Device.ID(), Name: p.Device.Name},
		"installation": viewInstallation(inst),
	})
}
