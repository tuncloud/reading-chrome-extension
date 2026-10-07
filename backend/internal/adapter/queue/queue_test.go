package queue

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	bot  = "b7c1e0f2-1111-2222-3333-444455556666"
	hash = "0123456789abcdef0123456789abcdef"
)

// T18 AC: same bucket → identical IDs; different bucket → different.
func TestDedupIDBuckets(t *testing.T) {
	bucket := 120 * time.Second
	start := time.Unix(1000*120, 0)
	m := Article(bot, hash)
	a := m.DedupID(start.Add(5*time.Second), bucket)
	b := m.DedupID(start.Add(119*time.Second), bucket)
	c := m.DedupID(start.Add(120*time.Second), bucket)
	if a != b {
		t.Fatalf("same bucket differs: %q %q", a, b)
	}
	if a == c {
		t.Fatal("next bucket must differ")
	}
	if a != "article#"+bot+"#"+hash+"#1000" {
		t.Fatalf("format: %q", a)
	}
	if d := Day(bot, "2026-10-06").DedupID(start, bucket); d != "day#"+bot+"#2026-10-06#1000" {
		t.Fatalf("day format: %q", d)
	}
}

// Review focus 3: a worker re-enqueue inside the same bucket must not be deduplicated away.
func TestRequeueDedupIDNeverCollides(t *testing.T) {
	now := time.Unix(1000*120+5, 0)
	m := Article(bot, hash)
	r := m.RequeueDedupID(now)
	if r == m.DedupID(now, 120*time.Second) || len(r) > 128 || r == m.RequeueDedupID(now.Add(time.Nanosecond)) {
		t.Fatalf("requeue id %q collides or is too long", r)
	}
}

func TestGroupIDs(t *testing.T) {
	if g := Article(bot, hash).GroupID(); g != bot {
		t.Fatalf("article group %q", g)
	}
	if g := Purge("u1", "").GroupID(); g != "user#u1" {
		t.Fatalf("purge group without installation %q", g)
	}
	if g := Purge("u1", bot).GroupID(); g != bot {
		t.Fatalf("purge group with installation %q", g)
	}
	if id := Purge("u1", bot).DedupID(time.Unix(42, 0), time.Minute); id != "purge#u1#42" {
		t.Fatalf("purge dedup %q", id)
	}
}

func TestMessageJSONShape(t *testing.T) {
	b, _ := json.Marshal(Article(bot, hash))
	if string(b) != `{"type":"article","installation_id":"`+bot+`","url_hash":"`+hash+`"}` {
		t.Fatalf("article json %s", b)
	}
	b, _ = json.Marshal(Purge("u1", ""))
	if string(b) != `{"type":"purge","installation_id":"","user_id":"u1"}` {
		t.Fatalf("purge json %s", b)
	}
}

type fakeSQS struct {
	batches [][]types.SendMessageBatchRequestEntry
	failIDs map[string]bool
	vis     []int32
}

func (f *fakeSQS) SendMessageBatch(_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
	f.batches = append(f.batches, in.Entries)
	out := &sqs.SendMessageBatchOutput{}
	for _, e := range in.Entries {
		if f.failIDs[aws.ToString(e.MessageDeduplicationId)] {
			out.Failed = append(out.Failed, types.BatchResultErrorEntry{Id: e.Id, Code: aws.String("InternalError")})
		}
	}
	return out, nil
}

func (f *fakeSQS) ChangeMessageVisibility(_ context.Context, in *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	f.vis = append(f.vis, in.VisibilityTimeout)
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}

func TestSQSSendBatchesOfTen(t *testing.T) {
	f := &fakeSQS{}
	q := &SQS{API: f, QueueURL: "https://sqs/q.fifo"}
	var msgs []Message
	for i := 0; i < 23; i++ {
		msgs = append(msgs, Day(bot, time.Unix(int64(i)*86400, 0).UTC().Format("2006-01-02")))
	}
	if err := q.Send(context.Background(), Bucketed(time.Unix(0, 0), time.Minute, msgs...)...); err != nil {
		t.Fatal(err)
	}
	if len(f.batches) != 3 || len(f.batches[0]) != 10 || len(f.batches[2]) != 3 {
		t.Fatalf("batches %d", len(f.batches))
	}
	e := f.batches[0][0]
	if aws.ToString(e.MessageGroupId) != bot || !strings.HasPrefix(aws.ToString(e.MessageDeduplicationId), "day#"+bot+"#") ||
		!strings.Contains(aws.ToString(e.MessageBody), `"type":"day"`) {
		t.Fatalf("bad entry %+v", e)
	}
}

func TestSQSSendReportsFailedEntries(t *testing.T) {
	m := Article(bot, hash)
	o := Outgoing{Message: m, DedupID: "x"}
	q := &SQS{API: &fakeSQS{failIDs: map[string]bool{"x": true}}, QueueURL: "q"}
	if err := q.Send(context.Background(), o); err == nil {
		t.Fatal("failed entries must surface as an error")
	}
}

func TestSQSChangeVisibilityRoundsUp(t *testing.T) {
	f := &fakeSQS{}
	q := &SQS{API: f, QueueURL: "q"}
	q.ChangeVisibility(context.Background(), "rh", 5500*time.Millisecond)
	if len(f.vis) != 1 || f.vis[0] != 6 {
		t.Fatalf("visibility %v, want [6]", f.vis)
	}
}

func TestLocalQueueDedupsAndDelivers(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{}, 4)
	l := &Local{Delay: 10 * time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handle: func(_ context.Context, ev events.SQSEvent) (events.SQSEventResponse, error) {
			calls.Add(1)
			done <- struct{}{}
			return events.SQSEventResponse{}, nil
		}}
	o := Bucketed(time.Unix(0, 0), time.Minute, Article(bot, hash))
	l.Send(context.Background(), o...)
	l.Send(context.Background(), o...)
	<-done
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("delivered %d times, want 1", calls.Load())
	}
}

func TestRecorder(t *testing.T) {
	r := &Recorder{}
	r.Send(context.Background(), Bucketed(time.Unix(0, 0), time.Minute, Article(bot, hash), Day(bot, "2026-10-06"))...)
	if got := r.Messages(); len(got) != 2 || got[1].Type != TypeDay {
		t.Fatalf("got %+v", got)
	}
	if d := r.Drain(); len(d) != 2 || len(r.Messages()) != 0 {
		t.Fatal("Drain must empty the recorder")
	}
}
