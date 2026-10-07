package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// logout serves POST /v1/auth/logout (spec §2.17).
func (h *handlers) logout(c echo.Context) error {
	if err := h.Auth.Logout(c.Request().Context(), principal(c)); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// deleteMe serves DELETE /v1/me (spec §2.17).
func (h *handlers) deleteMe(c echo.Context) error {
	if err := h.Account.Delete(c.Request().Context(), principal(c)); err != nil {
		return err
	}
	return c.NoContent(http.StatusAccepted)
}
