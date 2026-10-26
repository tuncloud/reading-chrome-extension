package pgqueue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/adapter/queue/msg"
	"reading-tracker/backend/internal/usecase/port"
)

const (
	maxAttempts  = 10
	backoffStep  = 30 * time.Second
	backoffMax   = 5 * time.Minute
	maxLastError = 1000
)

// Poller claims one group head at a time, runs the handler and settles the job.
// Zero Workers, Idle and HousekeepEvery default to 2, 1 s and 1 h.
type Poller struct {
	Pool           *pgxpool.Pool
	Handler        port.JobHandler
	Workers        int
	Idle           time.Duration
	Housekeeping   func(context.Context) error
	HousekeepEvery time.Duration
	Log            *slog.Logger
}

// Once claims, handles and settles at most one job. handled is false when nothing
// was claimable. Claim and settle errors are returned.
func (p *Poller) Once(ctx context.Context) (handled bool, err error) {
	var (
		id                  int64
		group, dedup, state string
		body                []byte
		attempts            int
		runAt, createdAt    time.Time
		leasedUntil         *time.Time
		lastErrPrev         *string
	)
	err = pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, sqlClaim).Scan(&id, &group, &dedup, &body, &state, &runAt,
			&attempts, &leasedUntil, &createdAt, &lastErrPrev)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Settle under WithoutCancel too: a claimed job must never be left leased by shutdown.
	ctx = context.WithoutCancel(ctx)
	log := p.log().With("job_id", id, "group", group, "attempt", attempts)

	var m msg.Message
	if err := json.Unmarshal(body, &m); err != nil {
		log.ErrorContext(ctx, "pgqueue: undecodable job dropped")
		_, err = p.Pool.Exec(ctx, sqlAck, id)
		return true, err
	}
	log = log.With("type", string(m.Type))

	herr := p.Handler.HandleJob(ctx, m.Job())
	if herr == nil {
		log.InfoContext(ctx, "pgqueue: job done", "outcome", "ack")
		_, err = p.Pool.Exec(ctx, sqlAck, id)
		return true, err
	}
	lastErr := herr.Error()
	if len(lastErr) > maxLastError {
		lastErr = lastErr[:maxLastError]
	}
	if attempts >= maxAttempts {
		log.ErrorContext(ctx, "pgqueue: job dead", "outcome", "dead", "err", herr)
		_, err = p.Pool.Exec(ctx, sqlDead, id, lastErr)
		return true, err
	}
	delay := min(backoffStep*time.Duration(attempts), backoffMax)
	outcome := "backoff"
	if ra, ok := errors.AsType[*port.RetryAfterError](herr); ok {
		delay, outcome = ra.Delay, "retry_after"
	}
	log.WarnContext(ctx, "pgqueue: job failed", "outcome", outcome, "delay", delay, "err", herr)
	_, err = p.Pool.Exec(ctx, sqlRelease, id, lastErr, delay)
	return true, err
}

// Run polls with Workers loops until ctx is done, then waits for in-flight jobs to
// settle and returns nil. Claim errors are logged, never returned.
func (p *Poller) Run(ctx context.Context) error {
	workers, idle, every := p.Workers, p.Idle, p.HousekeepEvery
	if workers <= 0 {
		workers = 2
	}
	if idle <= 0 {
		idle = time.Second
	}
	if every <= 0 {
		every = time.Hour
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for ctx.Err() == nil {
				handled, err := p.Once(ctx)
				if err != nil && ctx.Err() == nil {
					p.log().WarnContext(ctx, "pgqueue: poll failed", "err", err)
				}
				if !handled || err != nil {
					sleep(ctx, idle)
				}
			}
		})
	}
	wg.Go(func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.housekeep(ctx)
			}
		}
	})
	wg.Wait()
	return nil
}

func (p *Poller) housekeep(ctx context.Context) {
	if p.Housekeeping != nil {
		if err := p.Housekeeping(ctx); err != nil {
			p.log().WarnContext(ctx, "pgqueue: housekeeping failed", "err", err)
		}
	}
	if err := SweepDead(ctx, p.Pool); err != nil {
		p.log().WarnContext(ctx, "pgqueue: sweep dead failed", "err", err)
	}
}

func (p *Poller) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// SweepDead deletes dead jobs older than 14 days (the old DLQ retention).
func SweepDead(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, sqlSweepDead)
	return err
}
