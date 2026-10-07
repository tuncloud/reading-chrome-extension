package queue

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

// Local emulates the FIFO queue in-process for cmd/local: a 5-minute dedup window,
// a delivery delay, one message at a time, and redelivery after failures.
type Local struct {
	Delay  time.Duration
	Handle func(context.Context, events.SQSEvent) (events.SQSEventResponse, error)
	Log    *slog.Logger

	mu   sync.Mutex
	seen map[string]time.Time
	run  sync.Mutex
	seq  atomic.Int64
}

func (l *Local) Send(_ context.Context, msgs ...Outgoing) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]time.Time{}
	}
	now := time.Now()
	for _, o := range msgs {
		if t, ok := l.seen[o.DedupID]; ok && now.Sub(t) < 5*time.Minute {
			continue
		}
		l.seen[o.DedupID] = now
		body, err := json.Marshal(o.Message)
		if err != nil {
			return err
		}
		l.schedule(string(body), o.Message.GroupID(), l.Delay, 1)
	}
	return nil
}

func (l *Local) schedule(body, group string, delay time.Duration, attempt int) {
	id := strconv.FormatInt(l.seq.Add(1), 10)
	time.AfterFunc(delay, func() {
		l.run.Lock()
		defer l.run.Unlock()
		ev := events.SQSEvent{Records: []events.SQSMessage{{
			MessageId: id, ReceiptHandle: id, Body: body, Attributes: map[string]string{"MessageGroupId": group},
		}}}
		resp, err := l.Handle(context.Background(), ev)
		if err == nil && len(resp.BatchItemFailures) == 0 {
			return
		}
		if attempt >= 10 {
			l.Log.Error("local queue: dropping message after 10 attempts", "body", body)
			return
		}
		l.schedule(body, group, 30*time.Second, attempt+1)
	})
}

// ChangeVisibility is a no-op locally; failed messages are retried after 30 s.
func (l *Local) ChangeVisibility(context.Context, string, time.Duration) error { return nil }
