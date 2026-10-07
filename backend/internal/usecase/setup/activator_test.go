package setup_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/setup"
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

func newActivator(t *testing.T) (*setup.Activator, *dynamotest.Env, *queue.Recorder) {
	db, rec := dynamotest.New(t), &queue.Recorder{}
	a := &setup.Activator{Reading: db.Store, Installations: db.Store,
		Queue: &queue.SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return testNow }},
		Now:   func() time.Time { return testNow }, Sleep: func(time.Duration) {}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return a, db, rec
}

func pushTwo(t *testing.T, db *dynamotest.Env) {
	for i, u := range []string{"https://a.example/1", "https://b.example/2"} {
		_, err := db.Store.PushSegment(ctx, domain.SegmentWrite{
			InstallationID: "bot-1", DeviceID: "d", SegmentID: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}[i],
			SessionID: "s", URLHash: domain.URLHash(u), Date: "2026-10-06", StartedAt: testNow, EndedAt: testNow.Add(time.Minute),
			ActiveSeconds: 60, MaxProgress: 0.5, Article: domain.ArticleMeta{CanonicalURL: u, Source: "generic"},
		})
		must(t, err)
	}
	must(t, db.Store.MarkArticleSynced(ctx, "bot-1", domain.URLHash("https://a.example/1"), 1, false, testNow))
}

// T22: activation enqueues every article/day whose version is ahead of Notion.
func TestActivateEnqueuesPending(t *testing.T) {
	a, db, rec := newActivator(t)
	pushTwo(t, db)
	inst := &domain.Installation{ID: "bot-1", Status: domain.StatusActive}
	must(t, a.Activate(ctx, inst, false))
	got := rec.Messages()
	want := []queue.Message{queue.Article("bot-1", domain.URLHash("https://b.example/2")), queue.Day("bot-1", "2026-10-06")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestActivateFullResendsSyncedItems(t *testing.T) {
	a, db, rec := newActivator(t)
	pushTwo(t, db)
	must(t, a.Activate(ctx, &domain.Installation{ID: "bot-1", Status: domain.StatusActive}, true))
	if got := rec.Messages(); len(got) != 3 {
		t.Fatalf("full resync enqueued %+v", got)
	}
}
