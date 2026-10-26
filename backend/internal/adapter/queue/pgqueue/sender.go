// Package pgqueue is the Postgres-backed notion-sync queue (design §5): a sync_jobs
// table with strict per-group FIFO, replacing SQS FIFO outside Lambda.
package pgqueue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/adapter/queue/msg"
)

// Sender enqueues messages, run_at = now() + Delay (the debounce D). A batch is one
// transaction, so a send is all-or-nothing like SendMessageBatch.
type Sender struct {
	Pool  *pgxpool.Pool
	Delay time.Duration
}

var _ msg.Sender = (*Sender)(nil)

func (s *Sender) Send(ctx context.Context, msgs ...msg.Outgoing) error {
	if len(msgs) == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		for _, o := range msgs {
			body, err := json.Marshal(o.Message)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, sqlInsert, o.Message.GroupID(), o.DedupID, body, s.Delay); err != nil {
				return err
			}
		}
		return nil
	})
}
