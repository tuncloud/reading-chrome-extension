package pgqueue_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"reading-tracker/backend/internal/adapter/queue/msg"
	"reading-tracker/backend/internal/adapter/queue/pgqueue"
	"reading-tracker/backend/internal/platform/pgtest"
	"reading-tracker/backend/internal/usecase/port"
)

const (
	inst  = "b7c1e0f2-1111-2222-3333-444455556666"
	other = "c0ffee00-1111-2222-3333-444455556666"
)

var quiet = slog.New(slog.DiscardHandler)

// recorder records "type:key" per call and returns scripted errors (popped per call).
type recorder struct {
	mu   sync.Mutex
	got  []string
	errs map[string][]error
	all  func(key string) error
}

func jobKey(j port.Job) string {
	k := j.URLHash
	switch j.Type {
	case port.JobDay:
		k = j.Date
	case port.JobPurge:
		k = j.UserID
	}
	return string(j.Type) + ":" + k
}

func (r *recorder) HandleJob(_ context.Context, j port.Job) error {
	k := jobKey(j)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, k)
	if r.all != nil {
		return r.all(k)
	}
	if q := r.errs[k]; len(q) > 0 {
		r.errs[k] = q[1:]
		return q[0]
	}
	return nil
}

func (r *recorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

type handlerFunc func(context.Context, port.Job) error

func (f handlerFunc) HandleJob(ctx context.Context, j port.Job) error { return f(ctx, j) }

func send(t *testing.T, pool *pgxpool.Pool, delay time.Duration, ms ...msg.Message) {
	t.Helper()
	s := &pgqueue.Sender{Pool: pool, Delay: delay}
	for _, m := range ms {
		if err := s.Send(context.Background(), msg.Outgoing{Message: m, DedupID: m.RequeueDedupID(time.Now())}); err != nil {
			t.Fatal(err)
		}
	}
}

func count(t *testing.T, pool *pgxpool.Pool, where string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM sync_jobs WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func idOf(t *testing.T, pool *pgxpool.Pool, m msg.Message) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), "SELECT id FROM sync_jobs WHERE body = $1::jsonb", m).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func once(t *testing.T, p *pgqueue.Poller) bool {
	t.Helper()
	ok, err := p.Once(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestSendDedupsWithinBucket(t *testing.T) {
	env := pgtest.New(t)
	s := &pgqueue.Sender{Pool: env.Pool}
	ctx := context.Background()
	now := time.Now()
	a := msg.Article(inst, "h1")
	for range 2 {
		if err := s.Send(ctx, msg.Bucketed(now, 2*time.Minute, a)...); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Send(ctx, msg.Outgoing{Message: a, DedupID: a.RequeueDedupID(now)}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, env.Pool, "true"); n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	body := `{"type":"article","installation_id":"` + inst + `","url_hash":"h1"}`
	if n := count(t, env.Pool, "group_id = $1 AND body = $2::jsonb AND state = 'ready' AND run_at <= now()", inst, body); n != 2 {
		t.Fatalf("rows with group/body/run_at = %d, want 2", n)
	}
}

func TestSendIsAllOrNothing(t *testing.T) {
	env := pgtest.New(t)
	bg := context.Background()
	conn, err := env.Pool.Acquire(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(bg, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(bg, "LOCK TABLE sync_jobs IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bg, 200*time.Millisecond)
	defer cancel()
	err = (&pgqueue.Sender{Pool: env.Pool}).Send(ctx, msg.Bucketed(time.Now(), time.Minute,
		msg.Article(inst, "h1"), msg.Article(inst, "h2"), msg.Day(inst, "2026-10-06"))...)
	if err == nil {
		t.Fatal("Send under an exclusive lock must fail at the deadline")
	}
	if _, err := conn.Exec(bg, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, env.Pool, "true"); n != 0 {
		t.Fatalf("rows = %d, want 0", n)
	}
}

func TestDelayHidesJob(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, time.Hour, msg.Article(inst, "h1"))
	p := &pgqueue.Poller{Pool: env.Pool, Handler: &recorder{}, Log: quiet}
	if once(t, p) {
		t.Fatal("a delayed job must not be claimable")
	}
}

func TestGroupIsStrictFIFOAcrossPollers(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, 0, msg.Article(inst, "a"), msg.Day(inst, "d"), msg.Article(inst, "b"))

	var inFlight, maxInFlight atomic.Int32
	rec := &recorder{errs: map[string][]error{
		"article:a": {&port.RetryAfterError{Delay: 200 * time.Millisecond, Err: errors.New("429")}},
	}}
	h := handlerFunc(func(ctx context.Context, j port.Job) error {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for m := maxInFlight.Load(); n > m && !maxInFlight.CompareAndSwap(m, n); m = maxInFlight.Load() {
		}
		time.Sleep(20 * time.Millisecond) // widen the window for a second claim
		return rec.HandleJob(ctx, j)
	})

	deadline := time.Now().Add(10 * time.Second)
	errc := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		p := &pgqueue.Poller{Pool: env.Pool, Handler: h, Log: quiet}
		wg.Go(func() {
			for time.Now().Before(deadline) {
				var n int
				if err := env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM sync_jobs").Scan(&n); err != nil {
					errc <- err
					return
				}
				if n == 0 {
					return
				}
				ok, err := p.Once(context.Background())
				if err != nil {
					errc <- err
					return
				}
				if !ok {
					time.Sleep(10 * time.Millisecond)
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	if n := count(t, env.Pool, "true"); n != 0 {
		t.Fatalf("queue not drained: %d rows", n)
	}
	want := "article:a,article:a,day:d,article:b"
	if got := strings.Join(rec.calls(), ","); got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if m := maxInFlight.Load(); m != 1 {
		t.Fatalf("max in flight = %d, want 1", m)
	}
}

func TestOtherGroupsKeepFlowingWhileOneIsParked(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, 0, msg.Article(inst, "h1"), msg.Article(other, "h2"))
	rec := &recorder{errs: map[string][]error{"article:h1": {&port.RetryAfterError{Delay: time.Hour}}}}
	p := &pgqueue.Poller{Pool: env.Pool, Handler: rec, Log: quiet}
	if !once(t, p) || !once(t, p) {
		t.Fatal("both Once calls must handle a job")
	}
	if got := strings.Join(rec.calls(), ","); got != "article:h1,article:h2" {
		t.Fatalf("calls = %s", got)
	}
	if n := count(t, env.Pool, "state = 'ready' AND leased_until IS NULL AND run_at > now() + interval '59 minutes'"); n != 1 {
		t.Fatalf("parked rows = %d, want 1", n)
	}
	if once(t, p) {
		t.Fatal("the parked group must stay parked")
	}
}

func TestPlainErrorBacksOffAndDiesAfterTen(t *testing.T) {
	env := pgtest.New(t)
	head := msg.Article(inst, "head")
	send(t, env.Pool, 0, head, msg.Day(inst, "follow"))
	id := idOf(t, env.Pool, head)
	rec := &recorder{all: func(k string) error {
		if k == "article:head" {
			return errors.New("boom")
		}
		return nil
	}}
	p := &pgqueue.Poller{Pool: env.Pool, Handler: rec, Log: quiet}
	if !once(t, p) {
		t.Fatal("first Once must claim the head")
	}
	var secs float64
	var lastErr string
	if err := env.Pool.QueryRow(context.Background(),
		"SELECT extract(epoch FROM run_at - now())::float8, last_error FROM sync_jobs WHERE id = $1", id).Scan(&secs, &lastErr); err != nil {
		t.Fatal(err)
	}
	if secs < 28 || secs > 32 || lastErr != "boom" {
		t.Fatalf("backoff = %.1fs last_error = %q, want ≈30s and boom", secs, lastErr)
	}
	if once(t, p) {
		t.Fatal("a backed-off head must block its follower")
	}
	for range 9 {
		if _, err := env.Pool.Exec(context.Background(), "UPDATE sync_jobs SET run_at = now() WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
		if !once(t, p) {
			t.Fatal("a forced-ready head must be claimable")
		}
	}
	if n := count(t, env.Pool, "id = $1 AND state = 'dead' AND attempts = 10 AND leased_until IS NULL", id); n != 1 {
		t.Fatal("head must be dead after 10 attempts")
	}
	if !once(t, p) {
		t.Fatal("the follower must be claimable once the head is dead")
	}
	if got := rec.calls(); len(got) != 11 || got[10] != "day:follow" {
		t.Fatalf("calls = %v", got)
	}
}

func TestExpiredLeaseIsReclaimed(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, 0, msg.Article(inst, "h1"))
	entered := make(chan string, 2)
	release := make(chan struct{})
	h := handlerFunc(func(_ context.Context, j port.Job) error {
		entered <- jobKey(j)
		<-release
		return nil
	})
	p1 := &pgqueue.Poller{Pool: env.Pool, Handler: h, Log: quiet}
	p2 := &pgqueue.Poller{Pool: env.Pool, Handler: h, Log: quiet}
	res := make(chan bool, 2)
	var wg sync.WaitGroup
	defer wg.Wait()
	var closeOnce sync.Once
	free := func() { closeOnce.Do(func() { close(release) }) }
	defer free()

	wg.Go(func() { ok, _ := p1.Once(context.Background()); res <- ok })
	<-entered
	var id int64
	if err := env.Pool.QueryRow(context.Background(), "SELECT id FROM sync_jobs WHERE state = 'leased' AND attempts = 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if ok, err := p2.Once(context.Background()); ok || err != nil {
		t.Fatalf("a live lease must not be reclaimed: %v %v", ok, err)
	}
	if _, err := env.Pool.Exec(context.Background(), "UPDATE sync_jobs SET leased_until = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	wg.Go(func() { ok, _ := p2.Once(context.Background()); res <- ok })
	select {
	case k := <-entered:
		if k != "article:h1" {
			t.Fatalf("reclaimed %q", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the expired lease was not reclaimed")
	}
	if n := count(t, env.Pool, "id = $1 AND attempts = 2 AND leased_until > now()", id); n != 1 {
		t.Fatal("the reclaim must take the same id and renew its lease")
	}
	free()
	wg.Wait()
	if !<-res || !<-res {
		t.Fatal("both Once calls must report handled")
	}
	if n := count(t, env.Pool, "id = $1", id); n != 0 {
		t.Fatal("the reclaiming poller's ack must delete the row")
	}
}

// Review finding 2: a settle from an expired lease must not touch a row another
// worker has reclaimed (otherwise a stale release re-readies a running head, C4).
func TestStaleSettleIsIgnored(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, 0, msg.Article(inst, "h1"))
	var entries atomic.Int32
	entered := make(chan struct{}, 2)
	release1, release2 := make(chan struct{}), make(chan struct{})
	h := handlerFunc(func(context.Context, port.Job) error {
		if entries.Add(1) > 1 { // the reclaiming poller: acks once released
			entered <- struct{}{}
			<-release2
			return nil
		}
		entered <- struct{}{}
		<-release1
		return errors.New("stale failure")
	})
	p1 := &pgqueue.Poller{Pool: env.Pool, Handler: h, Log: quiet}
	p2 := &pgqueue.Poller{Pool: env.Pool, Handler: h, Log: quiet}
	type result struct {
		ok  bool
		err error
	}
	res1, res2 := make(chan result, 1), make(chan result, 1)
	defer func() {
		select {
		case <-release2:
		default:
			close(release2)
		}
	}()
	go func() { ok, err := p1.Once(context.Background()); res1 <- result{ok, err} }()
	<-entered
	if _, err := env.Pool.Exec(context.Background(), "UPDATE sync_jobs SET leased_until = now() - interval '1 second'"); err != nil {
		close(release1)
		t.Fatal(err)
	}
	go func() { ok, err := p2.Once(context.Background()); res2 <- result{ok, err} }()
	<-entered

	// P1's lease is gone; its failure must not re-ready the row P2 is running.
	close(release1)
	if r := <-res1; !r.ok || r.err != nil {
		t.Fatalf("p1 Once = %v, %v; want (true, nil)", r.ok, r.err)
	}
	if n := count(t, env.Pool, "state = 'leased' AND attempts = 2 AND leased_until > now() AND last_error IS NULL"); n != 1 {
		t.Fatal("a stale release must leave the reclaimed lease untouched")
	}
	close(release2)
	if r := <-res2; !r.ok || r.err != nil {
		t.Fatalf("p2 Once = %v, %v; want (true, nil)", r.ok, r.err)
	}
	if n := entries.Load(); n != 2 {
		t.Fatalf("handler entered %d times, want 2", n)
	}
	if n := count(t, env.Pool, "group_id = $1", inst); n != 0 {
		t.Fatalf("rows for group = %d, want 0 (stale release must not resurrect)", n)
	}
}

// Review finding 1: last_error must be cut on a rune boundary, or Postgres rejects it.
func TestLastErrorTruncationIsValidUTF8(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, 0, msg.Article(inst, "h1"))
	long := strings.Repeat("x", 999) + "é" + strings.Repeat("y", 50)
	p := &pgqueue.Poller{Pool: env.Pool, Handler: handlerFunc(func(context.Context, port.Job) error {
		return errors.New(long)
	}), Log: quiet}
	if !once(t, p) {
		t.Fatal("Once must claim the job")
	}
	var lastErr string
	if err := env.Pool.QueryRow(context.Background(),
		"SELECT last_error FROM sync_jobs WHERE state = 'ready' AND leased_until IS NULL").Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(lastErr) || len(lastErr) > 1000 || !strings.HasPrefix(long, lastErr) {
		t.Fatalf("last_error invalid: len %d valid %v", len(lastErr), utf8.ValidString(lastErr))
	}
}

func TestRunFinishesInFlightJobOnShutdown(t *testing.T) {
	env := pgtest.New(t)
	send(t, env.Pool, 0, msg.Article(inst, "one"), msg.Article(other, "two"))
	id1 := idOf(t, env.Pool, msg.Article(inst, "one"))
	var entries atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	h := handlerFunc(func(ctx context.Context, _ port.Job) error {
		entries.Add(1)
		entered <- struct{}{}
		<-release
		return ctx.Err() // nil only because the handler runs under WithoutCancel
	})
	p := &pgqueue.Poller{Pool: env.Pool, Handler: h, Workers: 1, Idle: 10 * time.Millisecond, Log: quiet}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	<-entered
	cancel()
	select {
	case <-done:
		close(release)
		t.Fatal("Run returned before the in-flight job settled")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if n := count(t, env.Pool, "id = $1", id1); n != 0 {
		t.Fatal("the in-flight job must be acked")
	}
	if n := count(t, env.Pool, "state = 'ready' AND attempts = 0"); n != 1 {
		t.Fatal("the second job must remain untouched")
	}
	if n := entries.Load(); n != 1 {
		t.Fatalf("handler entered %d times, want 1", n)
	}
}

func TestPollerSurvivesClaimError(t *testing.T) {
	envA, envB := pgtest.New(t), pgtest.New(t)
	u, err := url.Parse(envA.URL)
	if err != nil {
		t.Fatal(err)
	}
	idle := 50 * time.Millisecond
	p := &pgqueue.Poller{Pool: envA.Pool, Handler: &recorder{}, Idle: idle, Log: quiet}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	time.Sleep(idle / 2)
	if _, err := envB.Pool.Exec(context.Background(), "DROP DATABASE "+strings.TrimPrefix(u.Path, "/")+" WITH (FORCE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Once(context.Background()); err == nil {
		t.Fatal("a claim against a dropped database must fail")
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(3 * idle):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestSweepDeadKeepsRecentDead(t *testing.T) {
	env := pgtest.New(t)
	if _, err := env.Pool.Exec(context.Background(), `
		INSERT INTO sync_jobs (group_id, dedup_id, body, state, run_at, created_at) VALUES
		  ('g', 'old', '{}', 'dead', now(), now() - interval '15 days'),
		  ('g', 'new', '{}', 'dead', now(), now() - interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	if err := pgqueue.SweepDead(context.Background(), env.Pool); err != nil {
		t.Fatal(err)
	}
	if count(t, env.Pool, "true") != 1 || count(t, env.Pool, "dedup_id = 'new'") != 1 {
		t.Fatal("SweepDead must remove only the 15-day-old dead row")
	}
}

func TestUndecodableBodyIsDropped(t *testing.T) {
	env := pgtest.New(t)
	if _, err := env.Pool.Exec(context.Background(),
		`INSERT INTO sync_jobs (group_id, dedup_id, body, run_at) VALUES ('g', 'x', '[1]', now())`); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	p := &pgqueue.Poller{Pool: env.Pool, Handler: rec, Log: quiet}
	if !once(t, p) || len(rec.calls()) != 0 || count(t, env.Pool, "true") != 0 {
		t.Fatal("an undecodable job must be deleted without calling the handler")
	}
}
