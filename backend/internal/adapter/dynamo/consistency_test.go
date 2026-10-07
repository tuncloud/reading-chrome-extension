package dynamo_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

// recorder captures the inputs of every read so tests can assert on request flags.
// DynamoDB Local serves every read consistently, so only the request shows the difference.
type recorder struct {
	dynamo.API
	gets    []*dynamodb.GetItemInput
	queries []*dynamodb.QueryInput
}

func (r *recorder) GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	r.gets = append(r.gets, in)
	return r.API.GetItem(ctx, in, opts...)
}

func (r *recorder) Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	r.queries = append(r.queries, in)
	return r.API.Query(ctx, in, opts...)
}

// Token rotation (C7) and logout (T17) need read-after-write, so reads must be strongly consistent.
func TestReadsAreStronglyConsistent(t *testing.T) {
	env := dynamotest.New(t)
	rec := &recorder{API: env.DB}
	s := dynamo.New(rec, env.Table)
	must(t, s.CreateUser(ctx, "u1", domain.User{NotionUserID: "n1", Name: "Tuan Do", CreatedAt: domain.Timestamp(testNow)}))
	must(t, s.CreateDevice(ctx, "u1", "d1", domain.Device{Name: "Mac", TokenHMAC: "h1", CreatedAt: domain.Timestamp(testNow), LastSeenAt: domain.Timestamp(testNow)}))

	_, err := s.GetProfile(ctx, "u1")
	must(t, err)
	_, err = s.ListDevices(ctx, "u1")
	must(t, err)

	if len(rec.gets) != 1 || len(rec.queries) != 1 {
		t.Fatalf("expected one GetItem and one Query, got %d and %d", len(rec.gets), len(rec.queries))
	}
	if !aws.ToBool(rec.gets[0].ConsistentRead) {
		t.Error("GetItem must set ConsistentRead")
	}
	if !aws.ToBool(rec.queries[0].ConsistentRead) {
		t.Error("Query must set ConsistentRead")
	}
}
