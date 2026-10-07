package setup_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/setup"
)

var (
	ctx     = context.Background()
	testNow = time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	svc *setup.Service
	db  *dynamotest.Env
	rec *queue.Recorder
}

func newFixture(t *testing.T) *fixture {
	db := dynamotest.New(t)
	rec := &queue.Recorder{}
	svc := &setup.Service{Store: db.Store, Queue: &queue.SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return testNow }}}
	return &fixture{svc: svc, db: db, rec: rec}
}

// T22: resync enqueues every article/day whose version is ahead of Notion.
func TestResyncEnqueuesPending(t *testing.T) {
	f := newFixture(t)
	for i, u := range []string{"https://a.example/1", "https://b.example/2"} {
		_, err := f.db.Store.PushSegment(ctx, domain.SegmentWrite{
			InstallationID: "bot-1", DeviceID: "d", SegmentID: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}[i],
			SessionID: "s", URLHash: domain.URLHash(u), Date: "2026-10-06", StartedAt: testNow, EndedAt: testNow.Add(time.Minute),
			ActiveSeconds: 60, MaxProgress: 0.5, Article: domain.ArticleMeta{CanonicalURL: u, Source: "generic"},
		})
		must(t, err)
	}
	must(t, f.db.Store.MarkArticleSynced(ctx, "bot-1", domain.URLHash("https://a.example/1"), 1, false, testNow))

	must(t, f.svc.Resync(ctx, "bot-1", false))
	got := f.rec.Messages()
	want := []queue.Message{queue.Article("bot-1", domain.URLHash("https://b.example/2")), queue.Day("bot-1", "2026-10-06")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// I1: a full resync (databases changed) also re-sends the synced article.
	f.rec.Drain()
	must(t, f.svc.Resync(ctx, "bot-1", true))
	if got := f.rec.Messages(); len(got) != 3 || !slices.Contains(got, queue.Article("bot-1", domain.URLHash("https://a.example/1"))) {
		t.Fatalf("full resync got %+v", got)
	}
}
