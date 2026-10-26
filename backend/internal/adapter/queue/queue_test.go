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
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	bot  = "b7c1e0f2-1111-2222-3333-444455556666"
	hash = "0123456789abcdef0123456789abcdef"
)

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
	if err := q.EnqueueSync(ctx, bot, []string{hash}, nil); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueResync(ctx, bot, []string{hash}, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out: delivered %d of 2 messages", delivered.Load())
		}
	}
	if delivered.Load() != 2 {
		t.Fatalf("delivered %d, want 2", delivered.Load())
	}
}
