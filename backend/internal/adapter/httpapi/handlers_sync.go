package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// syncPush serves POST /v1/sync/push (spec §2.8). It is accepted whatever the
// installation status; Notion catches up via resync.
func (h *handlers) syncPush(c echo.Context) error {
	p := principal(c)
	var req pushRequestDTO
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	resp, err := h.ReadSync.Push(c.Request().Context(), p.InstallationID(), p.Device.ID, req.toInput())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pushResponse(resp))
}
