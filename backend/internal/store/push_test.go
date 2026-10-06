package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/store/storetest"
)

const articleURL = "https://medium.com/p/8f3c2a1b9d4e"

func segment(id, url string, seq, secs int, progress float64, started time.Time) store.SegmentWrite {
	return store.SegmentWrite{
		InstallationID: "bot-1", DeviceID: "dev-1", SegmentID: id, SessionID: "sess-1", Seq: seq,
		URLHash: store.URLHash(url), Date: started.UTC().Format("2006-01-02"),
		StartedAt: started, EndedAt: started.Add(time.Duration(secs) * time.Second),
		ActiveSeconds: secs, MaxProgress: progress,
		Article: store.ArticleMeta{CanonicalURL: url, Source: "medium", Title: "Designing Idempotent APIs", WordCount: 1820},
	}
}

func push(t *testing.T, s *store.Store, w store.SegmentWrite) store.PushOutcome {
	t.Helper()
	out, err := s.PushSegment(ctx, w)
	must(t, err)
	return out
}

// T10 AC + scenario 2: the same segment twice increments aggregates once (C2).
func TestPushSegmentTwiceCountsOnce(t *testing.T) {
	s := storetest.New(t).Store
	w := segment("6f1d2c9e-6a43-4b1e-9a0e-2f7c1d5b8e11", articleURL, 0, 58, 0.42, testNow)

	if out := push(t, s, w); out != store.PushAccepted {
		t.Fatalf("first push = %v", out)
	}
	if out := push(t, s, w); out != store.PushDuplicate {
		t.Fatalf("second push = %v, want duplicate", out)
	}
	a, err := s.GetArticle(ctx, "bot-1", store.URLHash(articleURL))
	must(t, err)
	if a.TotalSeconds != 58 || a.Sessions != 1 || a.Version != 1 || a.MaxProgress != 0.42 || a.CanonicalURL != articleURL || a.Title != "Designing Idempotent APIs" {
		t.Fatalf("aggregate wrong: %+v", a)
	}
	if a.FirstReadAt != store.Timestamp(testNow) || a.LastReadAt != store.Timestamp(testNow.Add(58*time.Second)) {
		t.Fatalf("read times wrong: first=%s last=%s", a.FirstReadAt, a.LastReadAt)
	}
	d, err := s.GetDay(ctx, "bot-1", "2026-10-06")
	must(t, err)
	if d.Seconds != 58 || d.Version != 1 || len(d.ArticleHashes) != 1 || d.ArticleHashes[0] != store.URLHash(articleURL) {
		t.Fatalf("day wrong: %+v", d)
	}
}

// T10 AC: max_progress (and last_read_at) never decrease.
func TestMaxProgressNeverDecreases(t *testing.T) {
	s := storetest.New(t).Store
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.6, testNow.Add(time.Hour)))
	push(t, s, segment("00000000-0000-4000-8000-000000000002", articleURL, 1, 60, 0.3, testNow))
	a, _ := s.GetArticle(ctx, "bot-1", store.URLHash(articleURL))
	if a.MaxProgress != 0.6 {
		t.Fatalf("max_progress = %v, want 0.6", a.MaxProgress)
	}
	if a.LastReadAt != store.Timestamp(testNow.Add(time.Hour+60*time.Second)) {
		t.Fatalf("last_read_at went backwards: %s", a.LastReadAt)
	}
	if a.TotalSeconds != 120 || a.Version != 2 {
		t.Fatalf("totals wrong: %+v", a)
	}
}

// Scenario 3: seq 1 before seq 0 still counts one session.
func TestOutOfOrderSessionCountedOnce(t *testing.T) {
	s := storetest.New(t).Store
	push(t, s, segment("00000000-0000-4000-8000-000000000002", articleURL, 1, 60, 0.5, testNow.Add(time.Minute)))
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.2, testNow))
	a, _ := s.GetArticle(ctx, "bot-1", store.URLHash(articleURL))
	if a.Sessions != 1 || a.TotalSeconds != 120 {
		t.Fatalf("sessions=%d total=%d", a.Sessions, a.TotalSeconds)
	}
}

type throttlingDB struct{ store.API }

func (throttlingDB) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return nil, &types.ProvisionedThroughputExceededException{Message: aws.String("slow down")}
}

// Scenario 14: throttling → retry, nothing written.
func TestPushThrottleIsRetry(t *testing.T) {
	env := storetest.New(t)
	s := store.New(throttlingDB{env.DB}, env.Table)
	if out := push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)); out != store.PushRetry {
		t.Fatalf("got %v, want retry", out)
	}
	if items := env.Scan(t); len(items) != 0 {
		t.Fatalf("throttled push wrote %d items", len(items))
	}
}

type conflictOnceDB struct {
	store.API
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
	env := storetest.New(t)
	s := store.New(&conflictOnceDB{API: env.DB}, env.Table, store.WithSleep(func(time.Duration) {}))
	if out := push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow)); out != store.PushAccepted {
		t.Fatalf("got %v, want accepted after conflict retry", out)
	}
}

type countingDB struct {
	store.API
	batchGets int
}

func (c *countingDB) BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	c.batchGets++
	return c.API.BatchGetItem(ctx, in, opts...)
}

// T17 AC: status for 100 URLs uses one BatchGet round trip (plus unprocessed retries).
func TestBatchGetArticlesOneRoundTripFor100(t *testing.T) {
	env := storetest.New(t)
	db := &countingDB{API: env.DB}
	s := store.New(db, env.Table)
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow))

	hashes := []string{store.URLHash(articleURL)}
	for i := len(hashes); i < 100; i++ {
		hashes = append(hashes, store.URLHash(articleURL+"?n="+string(rune('a'+i%26))+string(rune('a'+i/26))))
	}
	got, err := s.BatchGetArticles(ctx, "bot-1", hashes)
	must(t, err)
	if db.batchGets != 1 || len(got) != 1 || got[store.URLHash(articleURL)].TotalSeconds != 60 {
		t.Fatalf("batchGets=%d got=%d", db.batchGets, len(got))
	}
}

func TestMarkArticleSyncedIsMonotonic(t *testing.T) {
	s := storetest.New(t).Store
	h := store.URLHash(articleURL)
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow))
	must(t, s.MarkArticleSynced(ctx, "bot-1", h, 2, true, testNow))
	must(t, s.MarkArticleSynced(ctx, "bot-1", h, 1, false, testNow))
	a, _ := s.GetArticle(ctx, "bot-1", h)
	if a.NotionSyncedVersion != 2 || !a.Completed {
		t.Fatalf("older version overwrote newer: %+v", a)
	}
}

func TestArticleAndDayPageLinks(t *testing.T) {
	s := storetest.New(t).Store
	h := store.URLHash(articleURL)
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow))
	must(t, s.SetArticlePage(ctx, "bot-1", h, "page-1", "https://notion.so/page-1"))
	must(t, s.SetDayPage(ctx, "bot-1", "2026-10-06", "day-page-1"))
	a, _ := s.GetArticle(ctx, "bot-1", h)
	d, _ := s.GetDay(ctx, "bot-1", "2026-10-06")
	if a.NotionPageID != "page-1" || a.NotionURL != "https://notion.so/page-1" || d.NotionPageID != "day-page-1" {
		t.Fatalf("links not saved: %+v %+v", a, d)
	}
	must(t, s.ClearArticlePage(ctx, "bot-1", h))
	must(t, s.ClearDayPage(ctx, "bot-1", "2026-10-06"))
	a, _ = s.GetArticle(ctx, "bot-1", h)
	d, _ = s.GetDay(ctx, "bot-1", "2026-10-06")
	if a.NotionPageID != "" || a.NotionURL != "" || d.NotionPageID != "" {
		t.Fatalf("links not cleared: %+v %+v", a, d)
	}
}
