package dynamo_test

import (
	"context"
	"testing"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/adapter/storetest"
)

// Shared fixtures for the dynamo-only tests.
var (
	ctx     = context.Background()
	testNow = storetest.TestNow
	must    = storetest.Must
	segment = storetest.Segment
	push    = storetest.Push
)

const articleURL = storetest.ArticleURL

func TestConformanceIdentity(t *testing.T) {
	storetest.RunIdentity(t, func(t *testing.T) storetest.IdentityStore { return dynamotest.New(t).Store })
}

func TestConformanceReading(t *testing.T) {
	storetest.RunReading(t, func(t *testing.T) storetest.ReadingStore { return dynamotest.New(t).Store })
}
