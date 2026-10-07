package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// logout serves POST /v1/auth/logout (spec §2.17).
func (h *handlers) logout(c echo.Context) error {
	p := principal(c)
	if err := h.Store.RevokeDevice(c.Request().Context(), p.UserID, p.Device.ID(), p.TokenHMAC, h.Now()); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
