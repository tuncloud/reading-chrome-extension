package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/labstack/echo/v4"
)

// Spec §2.0: a throttled DynamoDB read (requireDevice does three per request) is a
// retryable 503 capacity_exceeded, not a 500.
func TestToAPIErrorMapsThrottlingTo503(t *testing.T) {
	cases := map[string]error{
		"provisioned throughput": &types.ProvisionedThroughputExceededException{Message: aws.String("x")},
		"wrapped":                fmt.Errorf("get device: %w", &types.ProvisionedThroughputExceededException{Message: aws.String("x")}),
		"throttling":             &smithy.GenericAPIError{Code: "ThrottlingException", Message: "x"},
		"request limit":          &smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "x"},
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
