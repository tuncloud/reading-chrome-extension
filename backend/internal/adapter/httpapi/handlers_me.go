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
	me, err := h.Account.Me(c.Request().Context(), principal(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user":         userView{ID: me.User.ID, Name: me.User.Name},
		"device":       deviceView{ID: me.Device.ID, Name: me.Device.Name},
		"installation": viewInstallation(me.Installation),
	})
}
