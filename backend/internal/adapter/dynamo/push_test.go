package dynamo_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/domain"
)

type throttlingDB struct{ dynamo.API }

func (throttlingDB) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return nil, &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")}
}

// Scenario 14: throttling → retry, nothing written.
func TestPushThrottleIsRetry(t *testing.T) {
	env := dynamotest.New(t)
	s := dynamo.New(throttlingDB{env.DB}, env.Table)
	if out := push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)); out != domain.PushRetry {
		t.Fatalf("got %v, want retry", out)
	}
	if items := env.Scan(t); len(items) != 0 {
		t.Fatalf("throttled push wrote %d items", len(items))
	}
}

type conflictOnceDB struct {
	dynamo.API
	conflicts int
}

func (c *conflictOnceDB) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opts ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	if c.conflicts == 0 {
		c.conflicts++
		return nil, &types.TransactionCanceledException{Message: aws.String("conflict"), CancellationReasons: []types.CancellationReason{
			{Code: aws.String("None")}, {Code: aws.String("TransactionConflict")}, {Code: aws.String("None")},
		}}
	}
	return c.API.TransactWriteItems(ctx, in, opts...)
}

// Review focus 1 / scenario 4: concurrent pushes to the same ART item conflict; retry in-server.
func TestPushRetriesTransactionConflict(t *testing.T) {
	env := dynamotest.New(t)
	s := dynamo.New(&conflictOnceDB{API: env.DB}, env.Table, dynamo.WithSleep(func(time.Duration) {}))
	if out := push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)); out != domain.PushAccepted {
		t.Fatalf("got %v, want accepted after conflict retry", out)
	}
}

// updateConflictDB fails the first `failures` UpdateItem calls with a
// TransactionConflictException, the error a plain UpdateItem gets while a
// TransactWriteItems on the same item is in flight (scenario 4).
type updateConflictDB struct {
	dynamo.API
	failures int
	calls    int
}

func (c *updateConflictDB) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	c.calls++
	if c.calls <= c.failures {
		return nil, &types.TransactionConflictException{Message: aws.String("conflict")}
	}
	return c.API.UpdateItem(ctx, in, opts...)
}

// Spec §3.2: the post-commit max_progress/last_read_at update retries a transaction conflict.
func TestPostCommitUpdateRetriesConflict(t *testing.T) {
	env := dynamotest.New(t)
	db := &updateConflictDB{API: env.DB, failures: 1}
	s := dynamo.New(db, env.Table, dynamo.WithSleep(func(time.Duration) {}))
	if out := push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)); out != domain.PushAccepted {
		t.Fatalf("got %v, want accepted after post-commit conflict retry", out)
	}
	a, err := s.GetArticle(ctx, "bot-1", domain.URLHash(articleURL))
	must(t, err)
	if a.MaxProgress != 0.5 || a.LastReadAt != domain.Timestamp(testNow.Add(60*time.Second)) {
		t.Fatalf("post-commit update lost: %+v", a)
	}
}

// A post-commit update that keeps failing must not be acknowledged: the client resends and the
// duplicate path heals max_progress/last_read_at without counting the segment twice.
func TestPostCommitFailureIsRetryAndDuplicateHeals(t *testing.T) {
	env := dynamotest.New(t)
	w := segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)
	failing := dynamo.New(&updateConflictDB{API: env.DB, failures: 1000}, env.Table, dynamo.WithSleep(func(time.Duration) {}))
	if out := push(t, failing, w); out != domain.PushRetry {
		t.Fatalf("got %v, want retry when the post-commit update cannot be applied", out)
	}
	a, err := env.Store.GetArticle(ctx, "bot-1", domain.URLHash(articleURL))
	must(t, err)
	if a.TotalSeconds != 60 || a.MaxProgress != 0 || a.LastReadAt != "" {
		t.Fatalf("expected committed totals but no post-commit values yet: %+v", a)
	}

	if out := push(t, env.Store, w); out != domain.PushDuplicate {
		t.Fatalf("resend = %v, want duplicate", out)
	}
	a, err = env.Store.GetArticle(ctx, "bot-1", domain.URLHash(articleURL))
	must(t, err)
	if a.MaxProgress != 0.5 || a.LastReadAt != domain.Timestamp(testNow.Add(60*time.Second)) {
		t.Fatalf("duplicate path did not heal: %+v", a)
	}
	if a.TotalSeconds != 60 || a.Sessions != 1 || a.Version != 1 {
		t.Fatalf("resend double-counted: %+v", a)
	}
}

// The duplicate path reports retry too when it cannot heal.
func TestDuplicateReportsRetryWhenHealFails(t *testing.T) {
	env := dynamotest.New(t)
	w := segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)
	push(t, env.Store, w)
	failing := dynamo.New(&updateConflictDB{API: env.DB, failures: 1000}, env.Table, dynamo.WithSleep(func(time.Duration) {}))
	if out := push(t, failing, w); out != domain.PushRetry {
		t.Fatalf("got %v, want retry", out)
	}
}

// inputRecordingDB mimics the SDK's idempotency middleware, which writes ClientRequestToken into
// the caller's input struct, then cancels the first attempt with a conflict.
type inputRecordingDB struct {
	dynamo.API
	inputs []*dynamodb.TransactWriteItemsInput
	tokens []*string // token on each input as it arrived, before this wrapper filled one
}

func (r *inputRecordingDB) TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opts ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	r.inputs = append(r.inputs, in)
	r.tokens = append(r.tokens, in.ClientRequestToken)
	if len(r.inputs) == 1 {
		in.ClientRequestToken = aws.String("token-of-cancelled-attempt")
		return nil, &types.TransactionCanceledException{Message: aws.String("conflict"), CancellationReasons: []types.CancellationReason{
			{Code: aws.String("None")}, {Code: aws.String("TransactionConflict")}, {Code: aws.String("None")},
		}}
	}
	return r.API.TransactWriteItems(ctx, in, opts...)
}

// Each attempt needs a fresh input: the SDK stamps an idempotency token on the struct, so
// reusing it would replay a cancelled attempt's token.
func TestPushConflictRetryBuildsFreshInput(t *testing.T) {
	env := dynamotest.New(t)
	rec := &inputRecordingDB{API: env.DB}
	s := dynamo.New(rec, env.Table, dynamo.WithSleep(func(time.Duration) {}))
	if out := push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)); out != domain.PushAccepted {
		t.Fatalf("got %v, want accepted", out)
	}
	if len(rec.inputs) != 2 {
		t.Fatalf("attempts = %d, want 2", len(rec.inputs))
	}
	if rec.inputs[0] == rec.inputs[1] {
		t.Fatal("conflict retry reused the first attempt's input struct")
	}
	if rec.tokens[1] != nil {
		t.Fatalf("retry carried the cancelled attempt's token %q", *rec.tokens[1])
	}
}

type countingDB struct {
	dynamo.API
	batchGets int
}

func (c *countingDB) BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	c.batchGets++
	return c.API.BatchGetItem(ctx, in, opts...)
}

// T17 AC: status for 100 URLs uses one BatchGet round trip (plus unprocessed retries).
func TestBatchGetArticlesOneRoundTripFor100(t *testing.T) {
	env := dynamotest.New(t)
	db := &countingDB{API: env.DB}
	s := dynamo.New(db, env.Table)
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow))

	hashes := []string{domain.URLHash(articleURL)}
	for i := len(hashes); i < 100; i++ {
		hashes = append(hashes, domain.URLHash(articleURL+"?n="+string(rune('a'+i%26))+string(rune('a'+i/26))))
	}
	got, err := s.BatchGetArticles(ctx, "bot-1", hashes)
	must(t, err)
	if db.batchGets != 1 || len(got) != 1 || got[domain.URLHash(articleURL)].TotalSeconds != 60 {
		t.Fatalf("batchGets=%d got=%d", db.batchGets, len(got))
	}
}
