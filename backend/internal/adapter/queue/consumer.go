package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/usecase/port"
)

// VisibilityChanger hides a message for a back-off period (Notion 429, spec §2.15).
type VisibilityChanger interface {
	ChangeVisibility(ctx context.Context, receiptHandle string, d time.Duration) error
}

var (
	_ VisibilityChanger = (*SQS)(nil)
	_ VisibilityChanger = (*Local)(nil)
)

// Consumer turns an SQS FIFO batch into jobs, in order. On the first failure it
// reports that message and every later one as failed (C21), so group order holds.
type Consumer struct {
	Handler    port.JobHandler
	Visibility VisibilityChanger
	Log        *slog.Logger
}

func (c *Consumer) HandleSQS(ctx context.Context, ev events.SQSEvent) (events.SQSEventResponse, error) {
	var resp events.SQSEventResponse
	for i, rec := range ev.Records {
		var m Message
		if err := json.Unmarshal([]byte(rec.Body), &m); err != nil {
			c.Log.ErrorContext(ctx, "queue: undecodable message dropped", "message_id", rec.MessageId)
			continue
		}
		err := c.Handler.HandleJob(ctx, m.Job())
		if err == nil {
			continue
		}
		c.Log.WarnContext(ctx, "queue: message failed", "message_id", rec.MessageId, "err", err)
		var ra *port.RetryAfterError
		if errors.As(err, &ra) {
			if verr := c.Visibility.ChangeVisibility(ctx, rec.ReceiptHandle, ra.Delay); verr != nil {
				c.Log.WarnContext(ctx, "queue: change visibility failed", "err", verr)
			}
		}
		for _, r := range ev.Records[i:] {
			resp.BatchItemFailures = append(resp.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: r.MessageId})
		}
		break
	}
	return resp, nil
}
