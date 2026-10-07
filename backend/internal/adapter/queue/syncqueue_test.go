package queue

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/usecase/port"
)

func newSyncQueue(rec *Recorder, now time.Time) *SyncQueue {
	return &SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return now }}
}

func TestEnqueueSyncDebouncesArticlesThenDays(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(1000*120+5, 0))
	if err := q.EnqueueSync(context.Background(), bot, []string{hash}, []string{"2026-10-06"}); err != nil {
		t.Fatal(err)
	}
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Article(bot, hash) || sent[1].Message != Day(bot, "2026-10-06") ||
		sent[0].DedupID != "article#"+bot+"#"+hash+"#1000" {
		t.Fatalf("sent %+v", sent)
	}
	if err := q.EnqueueSync(context.Background(), bot, nil, nil); err != nil || len(rec.Drain()) != 0 {
		t.Fatal("nothing to send must send nothing")
	}
}

// Review focus 5 (adapter half) / R2: resync ids never equal a push's bucket ids.
func TestResyncIDsNeverCollideWithPushIDs(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(1000*120+5, 0))
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(q.EnqueueSync(ctx, bot, []string{hash}, []string{"2026-10-06"}))
	must(q.EnqueueResync(ctx, bot, []string{hash}, []string{"2026-10-06"}))
	sent := rec.Drain()
	if len(sent) != 4 {
		t.Fatalf("sent %d", len(sent))
	}
	seen := map[string]bool{}
	for _, o := range sent {
		if seen[o.DedupID] {
			t.Fatalf("duplicate dedup id %q", o.DedupID)
		}
		seen[o.DedupID] = true
		if len(o.DedupID) > 128 {
			t.Fatalf("dedup id too long: %q", o.DedupID)
		}
	}
}

func TestResyncAfterPushIsDeliveredByLocalQueue(t *testing.T) {
	var delivered atomic.Int32
	done := make(chan struct{}, 4)
	l := &Local{Delay: time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handle: func(context.Context, events.SQSEvent) (events.SQSEventResponse, error) {
			delivered.Add(1)
			done <- struct{}{}
			return events.SQSEventResponse{}, nil
		}}
	q := &SyncQueue{Sender: l, Bucket: 120 * time.Second, Now: time.Now}
	ctx := context.Background()
	q.EnqueueSync(ctx, bot, []string{hash}, nil)
	q.EnqueueResync(ctx, bot, []string{hash}, nil)
	<-done
	<-done
	if delivered.Load() != 2 {
		t.Fatalf("delivered %d, want 2", delivered.Load())
	}
}

func TestRequeueAndPurge(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(42, 0))
	q.Requeue(context.Background(), port.Job{Type: port.JobDay, InstallationID: bot, Date: "2026-10-06"})
	q.EnqueuePurge(context.Background(), "u1", bot)
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Day(bot, "2026-10-06") || !strings.Contains(sent[0].DedupID, "#r") ||
		sent[1].Message != Purge("u1", bot) || sent[1].DedupID != "purge#u1#42" {
		t.Fatalf("sent %+v", sent)
	}
}
