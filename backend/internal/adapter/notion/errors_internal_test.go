package notion

import (
	"errors"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// Review focus 3: every Notion response class maps to the same outcome as before.
func TestTranslateKinds(t *testing.T) {
	cases := []struct {
		err      error
		notFound port.NotionErrorKind
		want     port.NotionErrorKind
	}{
		{&APIError{Status: 429, RetryAfter: 2 * time.Second}, port.NotionPageGone, port.NotionRateLimited},
		{&APIError{Status: 529}, port.NotionPageGone, port.NotionRateLimited},
		{&APIError{Status: 502}, port.NotionPageGone, port.NotionTransient},
		{&APIError{Status: 409, Code: "conflict_error"}, port.NotionPageGone, port.NotionConflict},
		{&APIError{Status: 401, Code: "unauthorized"}, port.NotionPageGone, port.NotionUnauthorized},
		{&APIError{Status: 400, Code: "invalid_grant"}, port.NotionNotFound, port.NotionInvalidGrant},
		{&APIError{Status: 404, Code: "object_not_found"}, port.NotionPageGone, port.NotionPageGone},
		{&APIError{Status: 404, Code: "object_not_found"}, port.NotionDataSourceMissing, port.NotionDataSourceMissing},
		{&APIError{Status: 404, Code: "object_not_found"}, port.NotionNotFound, port.NotionNotFound},
		{&APIError{Status: 400, Code: "validation_error", Message: "Can't edit block that is archived."}, port.NotionPageGone, port.NotionPageGone},
		{&APIError{Status: 400, Code: "validation_error", Message: "Can't edit block that is archived."}, port.NotionDataSourceMissing, port.NotionRejected},
		{&APIError{Status: 400, Code: "validation_error", Message: "body failed validation"}, port.NotionPageGone, port.NotionRejected},
		{&APIError{Status: 403, Code: "restricted_resource"}, port.NotionPageGone, port.NotionRejected},
		{errors.New("dial tcp: connection refused"), port.NotionPageGone, port.NotionTransient},
	}
	for i, c := range cases {
		ne, ok := port.NotionErrorOf(translate(c.err, c.notFound))
		if !ok || ne.Kind != c.want {
			t.Errorf("case %d: got %+v, want kind %s", i, ne, c.want)
		}
	}
	if ne, _ := port.NotionErrorOf(translate(&APIError{Status: 429, RetryAfter: 2 * time.Second}, port.NotionNotFound)); ne.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter lost: %+v", ne)
	}
	if translate(nil, port.NotionNotFound) != nil {
		t.Fatal("nil must stay nil")
	}
	se := &domain.SetupError{Reason: "template_not_found"}
	if got := translate(se, port.NotionNotFound); got != se {
		t.Fatalf("setup errors must pass through, got %v", got)
	}
}
