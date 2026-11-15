// Package notionsync writes reading data to the user's Notion (spec §2.13–§2.17). It is
// the only code that writes pages and the only code that refreshes tokens (C3, C8). The
// queue runs one job per installation at a time (C4), so a token refresh cannot race.
package notionsync

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Installations port.Installations
	Reading       port.Reading
	Users         port.Users
	Devices       port.Devices
	Purger        port.DataPurger
	Pages         port.NotionPages
	Auth          port.NotionAuth
	Sealer        port.Sealer
	Queue         port.SyncQueue
	Policy        domain.SyncPolicy
	Now           func() time.Time
	Jitter        func() time.Duration // added to Retry-After; default rand 0..3 s
	Sleep         func(time.Duration)  // in-process retry back-off; default time.Sleep
	Log           *slog.Logger
}

var _ port.JobHandler = (*Service)(nil)

// errAck stops a job without failing it (revoked, needs_setup, …).
var errAck = errors.New("notionsync: ack")

const defaultBackoff = 30 * time.Second

// HandleJob runs one job and classifies its error (spec §2.15).
func (s *Service) HandleJob(ctx context.Context, job port.Job) error {
	var err error
	switch job.Type {
	case port.JobArticle:
		err = s.syncArticle(ctx, job)
	case port.JobDay:
		err = s.syncDay(ctx, job)
	case port.JobPurge:
		err = s.purge(ctx, job)
	default:
		s.Log.ErrorContext(ctx, "notionsync: unknown job type dropped", "type", job.Type)
		return nil
	}
	return s.classify(ctx, err)
}

// classify maps a job error to ack (nil), back-off (*port.RetryAfterError) or retry.
// Retried (the poller re-runs the job with back-off and marks it dead after 10 attempts,
// so it stays visible): storage and network
// errors, Notion 5xx, 409 conflict_error, and a 401 that escaped session.call (the
// refresh endpoint rejected us, e.g. invalid_client). Acked and logged: every other
// Notion 4xx (400 validation_error poison, 403, untranslated 404s).
func (s *Service) classify(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, errAck) {
		return nil
	}
	e, ok := port.NotionErrorOf(err)
	if !ok {
		return err
	}
	switch e.Kind {
	case port.NotionRateLimited:
		d := e.RetryAfter
		if d <= 0 {
			d = defaultBackoff
		}
		return &port.RetryAfterError{Delay: d + s.jitter(), Err: err}
	case port.NotionTransient, port.NotionConflict, port.NotionUnauthorized:
		return err
	default:
		s.Log.ErrorContext(ctx, "notionsync: notion rejected request; dropping job",
			"kind", e.Kind.String(), "status", e.Status, "code", e.Code, "message", e.Message)
		return nil
	}
}

func (s *Service) jitter() time.Duration {
	if s.Jitter != nil {
		return s.Jitter()
	}
	return time.Duration(rand.IntN(4)) * time.Second
}

func (s *Service) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

// activeInstallation resolves the user's active installation at run time. It returns nil
// (→ ack) when the user is gone or deleted, has no active installation, or that
// installation is missing, not active, or someone else's; the reading data stays stored
// and is resynced on activation (spec §2.13 step 1).
func (s *Service) activeInstallation(ctx context.Context, uid string) (*domain.Installation, error) {
	user, err := s.Users.GetProfile(ctx, uid)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if user.Deleted() || user.ActiveInstallationID == "" {
		return nil, nil
	}
	inst, err := s.Installations.GetInstallation(ctx, user.ActiveInstallationID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inst.Status != domain.StatusActive || inst.UserID != uid {
		return nil, nil
	}
	return inst, nil
}

// dataSourceError: the database was deleted, so the installation needs setup again
// and the job is acked (spec §2.15). Other errors pass through.
func (s *Service) dataSourceError(ctx context.Context, inst *domain.Installation, name string, err error) error {
	if !port.IsNotionKind(err, port.NotionDataSourceMissing) {
		return err
	}
	if serr := s.Installations.SetInstallationStatus(ctx, inst.ID, domain.StatusNeedsSetup, "database_missing:"+name, s.Now()); serr != nil {
		return serr
	}
	s.Log.WarnContext(ctx, "notionsync: database missing; installation needs setup", "installation_id", inst.ID, "database", name)
	return errAck
}
