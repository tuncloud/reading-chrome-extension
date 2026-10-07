package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/domain"
)

// apiError renders as {"error":{"code","message"}} (spec §2.0).
type apiError struct {
	status int
	code   string
	msg    string
}

func (e *apiError) Error() string { return e.code + ": " + e.msg }

func errInvalid(format string, a ...any) *apiError {
	return &apiError{http.StatusBadRequest, "invalid_request", fmt.Sprintf(format, a...)}
}
func errInvalidGrant() *apiError {
	return &apiError{http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired or already used"}
}
func errUnauthorized() *apiError {
	return &apiError{http.StatusUnauthorized, "unauthorized", "missing, unknown or revoked device token"}
}
func errNoInstallation() *apiError {
	return &apiError{http.StatusConflict, "no_installation", "connect Notion first"}
}
func errConflict(msg string) *apiError { return &apiError{http.StatusConflict, "conflict", msg} }
func errNotFound(msg string) *apiError { return &apiError{http.StatusNotFound, "not_found", msg} }
func errTooLarge(msg string) *apiError {
	return &apiError{http.StatusRequestEntityTooLarge, "payload_too_large", msg}
}
func errCapacity() *apiError {
	return &apiError{http.StatusServiceUnavailable, "capacity_exceeded", "storage is busy; retry with backoff"}
}

func toAPIError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	var he *echo.HTTPError
	if errors.As(err, &he) {
		switch he.Code {
		case http.StatusNotFound:
			return errNotFound("no such route")
		case http.StatusMethodNotAllowed:
			return &apiError{he.Code, "method_not_allowed", "method not allowed"}
		case http.StatusRequestEntityTooLarge:
			return errTooLarge("request body exceeds 256 KB")
		}
		if he.Code < 500 {
			return &apiError{he.Code, "invalid_request", http.StatusText(he.Code)}
		}
	}
	if errors.Is(err, domain.ErrThrottled) {
		return errCapacity()
	}
	return &apiError{http.StatusInternalServerError, "internal", "internal error"}
}

func errorHandler(log *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		ae := toAPIError(err)
		if ae.status >= 500 {
			log.ErrorContext(c.Request().Context(), "request failed", "path", c.Request().URL.Path, "err", err)
		}
		_ = c.JSON(ae.status, map[string]any{"error": map[string]string{"code": ae.code, "message": ae.msg}})
	}
}

// decodeJSON reads the request body into v; oversized bodies become 413.
func decodeJSON(c echo.Context, v any) error {
	if err := json.NewDecoder(c.Request().Body).Decode(v); err != nil {
		var he *echo.HTTPError
		if errors.As(err, &he) && he.Code == http.StatusRequestEntityTooLarge {
			return errTooLarge("request body exceeds 256 KB")
		}
		return errInvalid("malformed JSON body")
	}
	return nil
}
