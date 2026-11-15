package postgres_test

import (
	"context"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/postgres"
	"reading-tracker/backend/internal/adapter/storetest"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/pgtest"
)

// RecentArticles puts an article without last_read_at after every dated one. ReadingStore cannot
// produce a NULL last_read_at (every push sets it), so the row is made with SQL here.
func TestRecentArticlesNullLastReadComesLast(t *testing.T) {
	ctx := context.Background()
	db := pgtest.New(t)
	s := postgres.New(db.Pool)
	const (
		urlNull = "https://medium.com/p/000000000000"
		urlOld  = "https://medium.com/p/aaaaaaaaaaaa"
		urlNew  = "https://medium.com/p/bbbbbbbbbbbb"
	)
	ids := []string{"00000000-0000-4000-8000-0000000000f1", "00000000-0000-4000-8000-0000000000f2", "00000000-0000-4000-8000-0000000000f3"}
	for i, u := range []string{urlNull, urlOld, urlNew} {
		w := storetest.Segment(ids[i], u, 0, 60, 0.5,
			storetest.TestNow.Add(time.Duration(i)*time.Hour))
		if out, err := s.PushSegment(ctx, w); err != nil || out != domain.PushAccepted {
			t.Fatalf("push %s = %v, %v", u, out, err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE articles SET last_read_at = NULL WHERE user_id = $1 AND url_hash = $2`,
		storetest.UserID, domain.URLHash(urlNull)); err != nil {
		t.Fatal(err)
	}

	got, err := s.RecentArticles(ctx, storetest.UserID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].CanonicalURL != urlNew || got[1].CanonicalURL != urlOld || got[2].CanonicalURL != urlNull {
		t.Fatalf("RecentArticles = %+v, want new, old, then the NULL last_read_at", got)
	}
}
