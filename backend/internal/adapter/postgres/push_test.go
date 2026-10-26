package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/postgres"
	"reading-tracker/backend/internal/adapter/storetest"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/pgtest"
)

// C2: concurrent pushes to the same article and day count every segment exactly once.
func TestConcurrentPushesCountEverySegmentOnce(t *testing.T) {
	ctx := context.Background()
	s := postgres.New(pgtest.New(t).Pool, postgres.WithSleep(func(time.Duration) {}))
	const n = 20
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := storetest.Segment(fmt.Sprintf("00000000-0000-4000-8000-%012d", i), storetest.ArticleURL, i, 60, 0.5,
				storetest.TestNow.Add(time.Duration(i)*time.Minute))
			for {
				out, err := s.PushSegment(ctx, w)
				switch {
				case err != nil:
					errs <- err
					return
				case out == domain.PushAccepted:
					return
				case out != domain.PushRetry:
					errs <- fmt.Errorf("segment %d: outcome %v", i, out)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	a, err := s.GetArticle(ctx, "bot-1", domain.URLHash(storetest.ArticleURL))
	storetest.Must(t, err)
	if a.TotalSeconds != n*60 || a.Version != n || a.Sessions != 1 {
		t.Fatalf("article = total %d version %d sessions %d", a.TotalSeconds, a.Version, a.Sessions)
	}
	d, err := s.GetDay(ctx, "bot-1", "2026-10-06")
	storetest.Must(t, err)
	if d.Seconds != n*60 || d.Version != n || len(d.ArticleHashes) != 1 {
		t.Fatalf("day = %+v", d)
	}
}

func TestSweepRemovesOnlyExpired(t *testing.T) {
	ctx := context.Background()
	env := pgtest.New(t)
	s := postgres.New(env.Pool)
	now := storetest.TestNow
	storetest.Push(t, s, storetest.Segment("00000000-0000-4000-8000-000000000001", storetest.ArticleURL, 0, 60, 0.5, now.Add(-91*24*time.Hour)))
	storetest.Push(t, s, storetest.Segment("00000000-0000-4000-8000-000000000002", storetest.ArticleURL, 1, 60, 0.5, now))
	storetest.Must(t, s.PutOAuthState(ctx, "st-1", domain.OAuthState{ExtRedirect: "https://x.chromiumapp.org/", CodeChallenge: "c"}, now.Add(-time.Second)))
	storetest.Must(t, s.PutAuthCode(ctx, "code-1", domain.AuthCode{UserID: "u1", InstallationID: "bot-1", CodeChallenge: "c",
		ExtRedirect: "https://x.chromiumapp.org/"}, now.Add(2*time.Minute)))

	storetest.Must(t, s.Sweep(ctx, now))

	var count int
	var id string
	storetest.Must(t, env.Pool.QueryRow(ctx, `SELECT count(*), max(id) FROM segments`).Scan(&count, &id))
	if count != 1 || id != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("segments after sweep: count %d, id %q", count, id)
	}
	// An earlier clock would still accept the state, so ErrNotFound proves the row is gone.
	if _, err := s.ConsumeOAuthState(ctx, "st-1", now.Add(-time.Hour)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired oauth state survived sweep: %v", err)
	}
	if _, err := s.ConsumeAuthCode(ctx, "code-1", now); err != nil {
		t.Fatalf("live auth code removed: %v", err)
	}
}
