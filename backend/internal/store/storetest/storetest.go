// Package storetest provisions throwaway tables on DynamoDB Local (`make ddb-up`).
package storetest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/store"
)

// Env is one fresh table plus a Store bound to it.
type Env struct {
	Store *store.Store
	DB    *dynamodb.Client
	Table string
}

// Endpoint is DYNAMODB_ENDPOINT or http://localhost:8000.
func Endpoint() string {
	if v := os.Getenv("DYNAMODB_ENDPOINT"); v != "" {
		return v
	}
	return "http://localhost:8000"
}

// Client returns a DynamoDB Local client with static dummy credentials.
func Client() *dynamodb.Client {
	return dynamodb.New(dynamodb.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("local", "local", ""),
		BaseEndpoint: aws.String(Endpoint()),
	})
}

// New creates a uniquely named table, dropped when the test ends. It fails the
// test (never skips) when DynamoDB Local is unreachable.
func New(t testing.TB) *Env {
	t.Helper()
	db := Client()
	name := fmt.Sprintf("reading-test-%d", rand.Int64())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.CreateTable(ctx, db, name); err != nil {
		t.Fatalf("DynamoDB Local not reachable at %s (run `make ddb-up`): %v", Endpoint(), err)
	}
	t.Cleanup(func() {
		_, _ = db.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(name)})
	})
	return &Env{Store: store.New(db, name), DB: db, Table: name}
}

// Scan returns every item in the table.
func (e *Env) Scan(t testing.TB) []map[string]types.AttributeValue {
	t.Helper()
	var items []map[string]types.AttributeValue
	in := &dynamodb.ScanInput{TableName: aws.String(e.Table)}
	for {
		res, err := e.DB.Scan(context.Background(), in)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		items = append(items, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return items
		}
		in.ExclusiveStartKey = res.LastEvaluatedKey
	}
}
