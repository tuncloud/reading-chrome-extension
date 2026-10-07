package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/usecase/readsync"
)

// syncPush serves POST /v1/sync/push (spec §2.8). It is accepted whatever the
// installation status; Notion catches up via resync.
func (h *handlers) syncPush(c echo.Context) error {
	p := principal(c)
	if p.InstallationID() == "" {
		return errNoInstallation()
	}
	var req pushRequestDTO
	if err := decodeJSON(c, &req); err != nil {
		return err
	}
	resp, err := h.ReadSync.Push(c.Request().Context(), p.InstallationID(), p.Device.ID, req.toInput())
	var env *readsync.EnvelopeError
	switch {
	case errors.As(err, &env):
		if env.TooLarge {
			return errTooLarge(env.Msg)
		}
		return errInvalid("%s", env.Msg)
	case errors.Is(err, readsync.ErrAllThrottled):
		return errCapacity()
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, pushResponse(resp))
}
