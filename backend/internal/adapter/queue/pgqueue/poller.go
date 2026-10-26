package pgqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	// handlerTimeout matches the Lambda worker's 60 s timeout. It must stay below the
	// 6-minute lease (a job settles before it can be reclaimed) and below the poller
	// pod's terminationGracePeriodSeconds minus settleTimeout (90 s - 10 s), so a job
	// in flight at SIGTERM still settles before SIGKILL. settleTimeout keeps a hung DB
	// from blocking Run's return.
	handlerTimeout = 60 * time.Second
	settleTimeout  = 10 * time.Second
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
// was claimable. Claim and settle errors are returned; a settle that lost its lease to
// another worker is not an error.
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
	log := p.log().With("job_id", id, "group", group, "attempt", attempts)

	var m msg.Message
	if err := json.Unmarshal(body, &m); err != nil {
		log.ErrorContext(ctx, "pgqueue: undecodable job dropped", "err", err)
		return p.settle(ctx, log, sqlAck, id, attempts)
	}
	log = log.With("type", string(m.Type))

	// Shutdown never aborts a job mid-write, but the handler must finish inside its lease.
	herr := p.handle(ctx, log, m)
	if herr == nil {
		log.InfoContext(ctx, "pgqueue: job done", "outcome", "ack")
		return p.settle(ctx, log, sqlAck, id, attempts)
	}
	lastErr := truncate(herr.Error(), maxLastError)
	if attempts >= maxAttempts {
		log.ErrorContext(ctx, "pgqueue: job dead", "outcome", "dead", "err", herr)
		return p.settle(ctx, log, sqlDead, id, attempts, lastErr)
	}
	delay := min(backoffStep*time.Duration(attempts), backoffMax)
	outcome := "backoff"
	if ra, ok := errors.AsType[*port.RetryAfterError](herr); ok {
		delay, outcome = ra.Delay, "retry_after"
	}
	log.WarnContext(ctx, "pgqueue: job failed", "outcome", outcome, "delay", delay, "err", herr)
	return p.settle(ctx, log, sqlRelease, id, attempts, lastErr, delay)
}

// handle runs the handler under handlerTimeout. A panic is recovered and returned as
// a plain handler error, so it backs off and dies like any other failure.
func (p *Poller) handle(ctx context.Context, log *slog.Logger, m msg.Message) (err error) {
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handlerTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			log.ErrorContext(ctx, "pgqueue: handler panic", "panic", fmt.Sprint(r))
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return p.Handler.HandleJob(hctx, m.Job())
}

// settle runs a settle statement fenced to this claim's (id, attempts). Zero rows means
// the lease expired and another worker owns the row: ignored, and the job counts as handled.
func (p *Poller) settle(ctx context.Context, log *slog.Logger, sql string, id int64, attempts int, args ...any) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	tag, err := p.Pool.Exec(ctx, sql, append([]any{id, attempts}, args...)...)
	if err != nil {
		return true, err
	}
	if tag.RowsAffected() == 0 {
		log.WarnContext(ctx, "queue: stale settle ignored")
	}
	return true, nil
}

// truncate cuts s to at most n bytes on a rune boundary, so Postgres accepts it as UTF-8.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.ToValidUTF8(s[:n], "")
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
		p.housekeep(ctx)
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
