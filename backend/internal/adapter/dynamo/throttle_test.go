package dynamo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

type failingGetDB struct {
	dynamo.API
	err error
}

func (f failingGetDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return nil, f.err
}

// Review focus 2 (adapter half): every DynamoDB capacity error is visible as domain.ErrThrottled
// and the SDK error stays reachable with errors.As.
func TestThrottleErrorsAreTagged(t *testing.T) {
	cases := []struct {
		code  string
		err   error
		reach func(error) bool
	}{
		{"ProvisionedThroughputExceededException", &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")},
			func(err error) bool { var e *types.ProvisionedThroughputExceededException; return errors.As(err, &e) }},
		{"ThrottlingException", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"},
			func(err error) bool {
				var e *smithy.GenericAPIError
				return errors.As(err, &e) && e.Code == "ThrottlingException"
			}},
		{"RequestLimitExceeded", &smithy.GenericAPIError{Code: "RequestLimitExceeded", Message: "slow down"},
			func(err error) bool {
				var e *smithy.GenericAPIError
				return errors.As(err, &e) && e.Code == "RequestLimitExceeded"
			}},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			env := dynamotest.New(t)
			s := dynamo.New(failingGetDB{env.DB, c.err}, env.Table)
			_, err := s.GetProfile(context.Background(), "u1")
			if !errors.Is(err, domain.ErrThrottled) {
				t.Fatalf("err %v does not match domain.ErrThrottled", err)
			}
			if !c.reach(err) {
				t.Fatal("SDK error no longer reachable with errors.As")
			}
		})
	}
}

func TestNotFoundIsNotThrottled(t *testing.T) {
	env := dynamotest.New(t)
	if _, err := env.Store.GetProfile(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrThrottled) {
		t.Fatalf("not-found error %v", err)
	}
}
