// Package setup connects an installation to its Notion databases and enqueues its
// backlog when it becomes active (spec §2.6, §2.7, T22).
package setup

import (
	"context"
	"log/slog"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

const (
	resyncAttempts = 3
	resyncBackoff  = 100 * time.Millisecond
)

// Activator enqueues an installation's backlog when it becomes active.
type Activator struct {
	Reading       port.Reading
	Installations port.Installations
	Queue         port.SyncQueue
	Now           func() time.Time
	Sleep         func(time.Duration) // retry back-off; nil means time.Sleep
	Log           *slog.Logger
}

// Activate enqueues the backlog (full when the databases changed, scenario 10),
// retrying briefly. If it still fails, the installation goes back to needs_setup
// with setup_error "resync_failed" instead of staying active with a stranded
// backlog: re-running setup or reconnecting runs it again.
func (a *Activator) Activate(ctx context.Context, inst *domain.Installation, full bool) error {
	var err error
	for attempt := 1; attempt <= resyncAttempts; attempt++ {
		if err = a.resync(ctx, inst.ID, full); err == nil {
			return nil
		}
		if attempt < resyncAttempts {
			a.sleep(time.Duration(attempt) * resyncBackoff)
		}
	}
	a.Log.ErrorContext(ctx, "resync enqueue failed; installation needs setup", "installation_id", inst.ID, "err", err)
	if err := a.Installations.SetInstallationStatus(ctx, inst.ID, domain.StatusNeedsSetup, domain.SetupErrResyncFailed, a.Now()); err != nil {
		return err
	}
	inst.Status, inst.SetupError = domain.StatusNeedsSetup, domain.SetupErrResyncFailed
	return nil
}

func (a *Activator) resync(ctx context.Context, id string, full bool) error {
	hashes, dates, err := a.Reading.PendingSync(ctx, id, full)
	if err != nil {
		return err
	}
	return a.Queue.EnqueueResync(ctx, id, hashes, dates)
}

func (a *Activator) sleep(d time.Duration) {
	if a.Sleep != nil {
		a.Sleep(d)
		return
	}
	time.Sleep(d)
}
