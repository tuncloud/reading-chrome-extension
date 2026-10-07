package notion

import (
	"errors"
	"net/http"
	"strings"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// translate classifies a client error for the use cases (spec §2.15). notFound is
// what a 404 means for this call: NotionPageGone (PATCH a page),
// NotionDataSourceMissing (query or create in a data source) or NotionNotFound.
// Setup errors pass through unchanged.
func translate(err error, notFound port.NotionErrorKind) error {
	if err == nil {
		return nil
	}
	var se *domain.SetupError
	if errors.As(err, &se) {
		return err
	}
	e, ok := AsAPIError(err)
	if !ok {
		return &port.NotionError{Kind: port.NotionTransient, Message: err.Error(), Err: err}
	}
	ne := &port.NotionError{Status: e.Status, Code: e.Code, Message: e.Message, RetryAfter: e.RetryAfter, Err: err}
	switch {
	case e.Status == http.StatusTooManyRequests || e.Status == 529:
		ne.Kind = port.NotionRateLimited
	case e.Status >= 500:
		ne.Kind = port.NotionTransient
	case e.Code == "invalid_grant":
		ne.Kind = port.NotionInvalidGrant
	case e.Status == http.StatusUnauthorized:
		ne.Kind = port.NotionUnauthorized
	case e.Status == http.StatusConflict:
		ne.Kind = port.NotionConflict
	case e.Status == http.StatusNotFound:
		ne.Kind = notFound
	case notFound == port.NotionPageGone && e.Status == http.StatusBadRequest && e.Code == "validation_error" &&
		strings.Contains(strings.ToLower(e.Message), "archived"):
		ne.Kind = port.NotionPageGone // "Can't edit block that is archived"
	default:
		ne.Kind = port.NotionRejected
	}
	return ne
}

// isNotFound reports a raw client 404 (used before translation, inside this adapter).
func isNotFound(err error) bool {
	e, ok := AsAPIError(err)
	return ok && e.Status == http.StatusNotFound
}
