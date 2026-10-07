package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/usecase/port"
)

type handlerFunc func(context.Context, port.Job) error

func (f handlerFunc) HandleJob(ctx context.Context, j port.Job) error { return f(ctx, j) }

type fakeVisibility struct{ calls []time.Duration }

func (f *fakeVisibility) ChangeVisibility(_ context.Context, _ string, d time.Duration) error {
	f.calls = append(f.calls, d)
	return nil
}

func event(t *testing.T, msgs ...Message) events.SQSEvent {
	var ev events.SQSEvent
	for i, m := range msgs {
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		ev.Records = append(ev.Records, events.SQSMessage{MessageId: fmt.Sprintf("m%d", i), ReceiptHandle: fmt.Sprintf("rh%d", i), Body: string(body)})
	}
	return ev
}

// C21: after the first failure, it and every later message are reported failed.
func TestConsumerFailsRestOfBatch(t *testing.T) {
	var ran []string
	c := &Consumer{Visibility: &fakeVisibility{}, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Handler: handlerFunc(func(_ context.Context, j port.Job) error {
			ran = append(ran, j.URLHash)
			if j.URLHash == "bad" {
				return errors.New("boom")
			}
			return nil
		})}
	resp, err := c.HandleSQS(context.Background(), event(t, Article(bot, "ok"), Article(bot, "bad"), Article(bot, "later")))
	if err != nil || len(resp.BatchItemFailures) != 2 || resp.BatchItemFailures[0].ItemIdentifier != "m1" ||
		resp.BatchItemFailures[1].ItemIdentifier != "m2" || len(ran) != 2 {
		t.Fatalf("resp %+v ran %v", resp, ran)
	}
}

func TestConsumerBacksOffRetryAfter(t *testing.T) {
	vis := &fakeVisibility{}
	c := &Consumer{Visibility: vis, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Handler: handlerFunc(func(context.Context, port.Job) error {
			return &port.RetryAfterError{Delay: 7 * time.Second, Err: errors.New("rate limited")}
		})}
	resp, _ := c.HandleSQS(context.Background(), event(t, Day(bot, "2026-10-06")))
	if len(resp.BatchItemFailures) != 1 || len(vis.calls) != 1 || vis.calls[0] != 7*time.Second {
		t.Fatalf("resp %+v vis %v", resp, vis.calls)
	}
}

func TestConsumerPassesJobFields(t *testing.T) {
	var got port.Job
	c := &Consumer{Visibility: &fakeVisibility{}, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Handler: handlerFunc(func(_ context.Context, j port.Job) error { got = j; return nil })}
	c.HandleSQS(context.Background(), event(t, Purge("u1", bot)))
	if got != (port.Job{Type: port.JobPurge, InstallationID: bot, UserID: "u1"}) {
		t.Fatalf("job %+v", got)
	}
}

func TestConsumerAcksUndecodable(t *testing.T) {
	logs := &bytes.Buffer{}
	c := &Consumer{Visibility: &fakeVisibility{}, Log: slog.New(slog.NewTextHandler(logs, nil)),
		Handler: handlerFunc(func(context.Context, port.Job) error { t.Fatal("must not run"); return nil })}
	resp, err := c.HandleSQS(context.Background(), events.SQSEvent{Records: []events.SQSMessage{{MessageId: "m0", Body: "not json"}}})
	if err != nil || len(resp.BatchItemFailures) != 0 || !bytes.Contains(logs.Bytes(), []byte("undecodable")) {
		t.Fatalf("resp %+v logs %s", resp, logs)
	}
}
