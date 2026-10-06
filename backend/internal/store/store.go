// Package store is the DynamoDB single-table repository (spec §3.1–§3.2).
package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
)

// API is the subset of *dynamodb.Client the store uses; tests wrap it to inject errors.
type API interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	BatchGetItem(context.Context, *dynamodb.BatchGetItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error)
	BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

var (
	ErrNotFound        = errors.New("store: not found")
	ErrConditionFailed = errors.New("store: condition failed")
)

// Store reads and writes the reading table.
type Store struct {
	db    API
	table string
	sleep func(time.Duration)
}

// Option configures a Store.
type Option func(*Store)

// WithSleep replaces time.Sleep in retry back-offs (tests).
func WithSleep(f func(time.Duration)) Option { return func(s *Store) { s.sleep = f } }

func New(db API, table string, opts ...Option) *Store {
	s := &Store{db: db, table: table, sleep: time.Sleep}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Timestamp formats t as RFC 3339 UTC with second precision. Every stored time uses
// it so string comparisons in condition expressions order chronologically.
func Timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// IsThrottle reports DynamoDB capacity errors that the client should retry later.
func IsThrottle(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.ErrorCode() {
	case "ProvisionedThroughputExceededException", "ThrottlingException", "RequestLimitExceeded":
		return true
	}
	return false
}

func isConditionFailed(err error) bool {
	var c *types.ConditionalCheckFailedException
	return errors.As(err, &c)
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": str(pk), "sk": str(sk)}
}

func str(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }

func numI(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}

func numF(v float64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatFloat(v, 'f', -1, 64)}
}

func boolV(v bool) types.AttributeValue { return &types.AttributeValueMemberBOOL{Value: v} }

func (s *Store) get(ctx context.Context, pk, sk string, out any) error {
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: &s.table, Key: key(pk, sk)})
	if err != nil {
		return err
	}
	if len(res.Item) == 0 {
		return ErrNotFound
	}
	return attributevalue.UnmarshalMap(res.Item, out)
}

func (s *Store) putItem(ctx context.Context, item any) error {
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: av})
	return err
}

// update runs one UpdateItem; a failed condition becomes ErrConditionFailed.
func (s *Store) update(ctx context.Context, pk, sk, expr, cond string, names map[string]string, values map[string]types.AttributeValue) error {
	in := &dynamodb.UpdateItemInput{TableName: &s.table, Key: key(pk, sk), UpdateExpression: aws.String(expr)}
	if len(names) > 0 {
		in.ExpressionAttributeNames = names
	}
	if len(values) > 0 {
		in.ExpressionAttributeValues = values
	}
	if cond != "" {
		in.ConditionExpression = aws.String(cond)
	}
	_, err := s.db.UpdateItem(ctx, in)
	if isConditionFailed(err) {
		return ErrConditionFailed
	}
	return err
}

// queryAll pages through a query and returns every item.
func (s *Store) queryAll(ctx context.Context, in *dynamodb.QueryInput) ([]map[string]types.AttributeValue, error) {
	in.TableName = &s.table
	var items []map[string]types.AttributeValue
	for {
		res, err := s.db.Query(ctx, in)
		if err != nil {
			return nil, err
		}
		items = append(items, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return items, nil
		}
		in.ExclusiveStartKey = res.LastEvaluatedKey
	}
}

// queryPrefix returns every item in partition pk whose sk starts with prefix.
func (s *Store) queryPrefix(ctx context.Context, pk, prefix string) ([]map[string]types.AttributeValue, error) {
	return s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression:    aws.String("pk = :pk AND begins_with(sk, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": str(pk), ":prefix": str(prefix)},
	})
}

const maxBatchAttempts = 8

func backoff(attempt int) time.Duration {
	return time.Duration(min(1<<attempt, 64)) * 25 * time.Millisecond
}
