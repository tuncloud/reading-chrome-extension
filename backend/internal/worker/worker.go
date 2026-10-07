// Package worker is the notion-writer Lambda: the only component that writes
// pages to Notion and refreshes Notion tokens (C3, C8).
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/crypto"
	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

// VisibilityChanger hides a message for a back-off period.
type VisibilityChanger interface {
	ChangeVisibility(ctx context.Context, receiptHandle string, d time.Duration) error
}

type Deps struct {
	Store      *store.Store
	Notion     *notion.Client
	Keys       *crypto.Keyring
	Queue      queue.Sender
	Visibility VisibilityChanger
	Tracking   config.Tracking
	Now        func() time.Time
	Jitter     func() time.Duration // added to Retry-After; default rand 0..3 s
	Log        *slog.Logger
}

type handlerFunc func(ctx context.Context, m queue.Message) error

type Worker struct {
	Deps
	handlers map[queue.Type]handlerFunc
}

// errAck stops processing a message without failing it (revoked, needs_setup, …).
var errAck = errors.New("worker: ack")

const defaultBackoff = 30 * time.Second

type retryAfterError struct {
	delay time.Duration
	err   error
}

func (e *retryAfterError) Error() string { return fmt.Sprintf("retry after %s: %v", e.delay, e.err) }
func (e *retryAfterError) Unwrap() error { return e.err }

func New(d Deps) *Worker {
	if d.Jitter == nil {
		d.Jitter = func() time.Duration { return time.Duration(rand.IntN(4)) * time.Second }
	}
	w := &Worker{Deps: d}
	w.handlers = map[queue.Type]handlerFunc{
		queue.TypeArticle: w.handleArticle,
	}
	return w
}

// HandleSQS processes a FIFO batch in order. On the first failure it reports that
// message and every later one as failed (C21), so group order is preserved.
func (w *Worker) HandleSQS(ctx context.Context, ev events.SQSEvent) (events.SQSEventResponse, error) {
	var resp events.SQSEventResponse
	for i, rec := range ev.Records {
		err := w.handleRecord(ctx, rec)
		if err == nil {
			continue
		}
		w.Log.WarnContext(ctx, "worker: message failed", "message_id", rec.MessageId, "err", err)
		var ra *retryAfterError
		if errors.As(err, &ra) {
			if verr := w.Visibility.ChangeVisibility(ctx, rec.ReceiptHandle, ra.delay); verr != nil {
				w.Log.WarnContext(ctx, "worker: change visibility failed", "err", verr)
			}
		}
		for _, r := range ev.Records[i:] {
			resp.BatchItemFailures = append(resp.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: r.MessageId})
		}
		break
	}
	return resp, nil
}

func (w *Worker) handleRecord(ctx context.Context, rec events.SQSMessage) error {
	var m queue.Message
	if err := json.Unmarshal([]byte(rec.Body), &m); err != nil {
		w.Log.ErrorContext(ctx, "worker: undecodable message dropped", "message_id", rec.MessageId)
		return nil
	}
	h, ok := w.handlers[m.Type]
	if !ok {
		w.Log.ErrorContext(ctx, "worker: unknown message type dropped", "type", m.Type)
		return nil
	}
	return w.classify(ctx, h(ctx, m))
}

// classify maps a handler error to ack (nil), a back-off, or a plain failure (spec §2.15).
// Retried (failed, redelivered, DLQ after maxReceive so it stays visible): 5xx, 409
// conflict_error (retryable per Notion) and a 401 that escaped session.call, which
// means the refresh endpoint rejected us (e.g. invalid_client), not a revoked token.
// Acked and logged: every other 4xx (400 validation_error poison, 403
// restricted_resource, 404s the handlers did not translate).
func (w *Worker) classify(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, errAck) {
		return nil
	}
	e, ok := notion.AsAPIError(err)
	if !ok {
		return err // network or DynamoDB: normal visibility timeout
	}
	switch {
	case e.Status == http.StatusTooManyRequests || e.Status == 529:
		d := e.RetryAfter
		if d <= 0 {
			d = defaultBackoff
		}
		return &retryAfterError{delay: d + w.Jitter(), err: err}
	case e.Status >= 500, e.Status == http.StatusConflict, e.Status == http.StatusUnauthorized:
		return err
	default:
		w.Log.ErrorContext(ctx, "worker: notion rejected request; dropping message",
			"status", e.Status, "code", e.Code, "message", e.Message)
		return nil
	}
}

// activeInstallation returns nil (→ ack) when the installation is gone or not active.
// Its data stays in DynamoDB and is resynced on reactivation (spec §2.13 step 1).
func (w *Worker) activeInstallation(ctx context.Context, id string) (*store.Installation, error) {
	inst, err := w.Store.GetInstallation(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inst.Status != store.StatusActive {
		return nil, nil
	}
	return inst, nil
}

func isStatus(err error, status int) bool {
	e, ok := notion.AsAPIError(err)
	return ok && e.Status == status
}
