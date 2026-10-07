// Package setup re-enqueues an installation's backlog when it becomes active (T22).
// It is replaced by usecase/setup in Task 6.
package setup

import (
	"context"
	"time"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/queue"
)

type Service struct {
	Store  *dynamo.Store
	Queue  queue.Sender
	Bucket time.Duration
	Now    func() time.Time
}

// Resync enqueues every ART/DAY whose version is ahead of Notion; full re-sends every
// ART/DAY (used when the installation's data sources changed, scenario 10).
func (s *Service) Resync(ctx context.Context, installationID string, full bool) error {
	hashes, dates, err := s.Store.PendingSync(ctx, installationID, full)
	if err != nil {
		return err
	}
	msgs := make([]queue.Message, 0, len(hashes)+len(dates))
	for _, h := range hashes {
		msgs = append(msgs, queue.Article(installationID, h))
	}
	for _, d := range dates {
		msgs = append(msgs, queue.Day(installationID, d))
	}
	if len(msgs) == 0 {
		return nil
	}
	return s.Queue.Send(ctx, queue.Bucketed(s.Now(), s.Bucket, msgs...)...)
}
