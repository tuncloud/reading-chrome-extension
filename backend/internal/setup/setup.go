// Package setup re-enqueues an installation's backlog when it becomes active (T22).
// It is replaced by usecase/setup in Task 6.
package setup

import (
	"context"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Store *dynamo.Store
	Queue port.SyncQueue
}

// Resync enqueues every ART/DAY whose version is ahead of Notion; full re-sends every
// ART/DAY (used when the installation's data sources changed, scenario 10).
func (s *Service) Resync(ctx context.Context, installationID string, full bool) error {
	hashes, dates, err := s.Store.PendingSync(ctx, installationID, full)
	if err != nil {
		return err
	}
	return s.Queue.EnqueueResync(ctx, installationID, hashes, dates)
}
