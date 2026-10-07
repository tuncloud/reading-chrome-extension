package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/apperr"
)

// Spec §2.0: a throttled DynamoDB read (requireDevice does three per request) is a
// retryable 503 capacity_exceeded, not a 500.
func TestToAPIErrorMapsThrottlingTo503(t *testing.T) {
	cases := map[string]error{
		"sentinel": domain.ErrThrottled,
		"wrapped":  fmt.Errorf("get profile: %w", fmt.Errorf("%w: slow down", domain.ErrThrottled)),
	}
	for name, err := range cases {
		ae := toAPIError(err)
		if ae.status != http.StatusServiceUnavailable || ae.code != "capacity_exceeded" {
			t.Errorf("%s: got %d %s", name, ae.status, ae.code)
		}
	}
}

func TestToAPIErrorKeepsOtherErrorsAndPrecedence(t *testing.T) {
	if ae := toAPIError(errors.New("boom")); ae.status != http.StatusInternalServerError || ae.code != "internal" {
		t.Errorf("plain error: got %d %s", ae.status, ae.code)
	}
	if ae := toAPIError(errUnauthorized()); ae.status != http.StatusUnauthorized {
		t.Errorf("apiError must win: got %d", ae.status)
	}
	if ae := toAPIError(echo.ErrNotFound); ae.code != "not_found" {
		t.Errorf("echo.HTTPError must win: got %s", ae.code)
	}
}
func TestToAPIErrorMapsAppErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{apperr.NewInvalid("bad %s", "x"), 400, "invalid_request"},
		{apperr.NewInvalidGrant(), 400, "invalid_grant"},
		{apperr.NewUnauthorized(), 401, "unauthorized"},
		{apperr.NewNotFound("nope"), 404, "not_found"},
		{apperr.NewConflict("busy"), 409, "conflict"},
		{apperr.NewNoInstallation(), 409, "no_installation"},
		{apperr.NewTooLarge("big"), 413, "payload_too_large"},
		{fmt.Errorf("wrapped: %w", apperr.NewCapacity()), 503, "capacity_exceeded"},
	}
	for _, c := range cases {
		if ae := toAPIError(c.err); ae.status != c.status || ae.code != c.code {
			t.Errorf("%v: got %d %s, want %d %s", c.err, ae.status, ae.code, c.status, c.code)
		}
	}
	if ae := toAPIError(apperr.NewInvalid("bad %s", "x")); ae.msg != "bad x" {
		t.Errorf("message lost: %q", ae.msg)
	}
}
