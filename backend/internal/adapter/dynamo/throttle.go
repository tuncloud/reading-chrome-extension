package dynamo

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"reading-tracker/backend/internal/domain"
)

// taggingAPI marks DynamoDB capacity errors so callers outside this adapter can
// recognise them with errors.Is(err, domain.ErrThrottled) (spec §2.0 → 503), while
// errors.As still reaches the SDK error for this package's own classification.
type taggingAPI struct{ API }

type throttledError struct{ err error }

func (e throttledError) Error() string   { return e.err.Error() }
func (e throttledError) Unwrap() []error { return []error{domain.ErrThrottled, e.err} }

func tag(err error) error {
	if err != nil && isThrottle(err) {
		return throttledError{err}
	}
	return err
}

func (t taggingAPI) GetItem(ctx context.Context, in *dynamodb.GetItemInput, o ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	out, err := t.API.GetItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) PutItem(ctx context.Context, in *dynamodb.PutItemInput, o ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	out, err := t.API.PutItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, o ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	out, err := t.API.UpdateItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, o ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	out, err := t.API.DeleteItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) Query(ctx context.Context, in *dynamodb.QueryInput, o ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	out, err := t.API.Query(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, o ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	out, err := t.API.BatchGetItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, o ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	out, err := t.API.BatchWriteItem(ctx, in, o...)
	return out, tag(err)
}

func (t taggingAPI) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, o ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	out, err := t.API.TransactWriteItems(ctx, in, o...)
	return out, tag(err)
}
