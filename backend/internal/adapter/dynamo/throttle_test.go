package dynamo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

type throttledGetDB struct{ dynamo.API }

func (throttledGetDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return nil, &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")}
}

// Review focus 2 (adapter half): throttling is visible as domain.ErrThrottled.
func TestThrottleErrorsAreTagged(t *testing.T) {
	env := dynamotest.New(t)
	s := dynamo.New(throttledGetDB{env.DB}, env.Table)
	_, err := s.GetProfile(context.Background(), "u1")
	if !errors.Is(err, domain.ErrThrottled) {
		t.Fatalf("err %v does not match domain.ErrThrottled", err)
	}
	var pte *types.ProvisionedThroughputExceededException
	if !errors.As(err, &pte) {
		t.Fatal("SDK error no longer reachable with errors.As")
	}
	if _, err := env.Store.GetProfile(context.Background(), "missing"); !errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrThrottled) {
		t.Fatalf("not-found error %v", err)
	}
}
