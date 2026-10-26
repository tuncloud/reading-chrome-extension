package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"reading-tracker/backend/internal/domain"
)

const (
	segmentTTL       = 90 * 24 * time.Hour
	conflictAttempts = 3
)

// PushSegment applies one segment exactly once (C2) in one transaction: the segment
// insert, the article and day increments (only when the segment is new) and the
// monotonic max_progress/last_read_at raise (always, so a duplicate heals it).
func (s *Store) PushSegment(ctx context.Context, w domain.SegmentWrite) (domain.PushOutcome, error) {
	for attempt := 1; ; attempt++ {
		outcome, err := s.pushTx(ctx, w)
		switch {
		case err == nil:
			return outcome, nil
		case isConflict(err):
			// Another push holds the same article/day row (scenario 4).
			if attempt >= conflictAttempts {
				return domain.PushRetry, nil
			}
			s.sleep(time.Duration(attempt*50) * time.Millisecond)
		case errors.Is(err, domain.ErrThrottled):
			return domain.PushRetry, nil
		default:
			return 0, err
		}
	}
}

func (s *Store) pushTx(ctx context.Context, w domain.SegmentWrite) (domain.PushOutcome, error) {
	outcome := domain.PushDuplicate
	err := s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO segments (installation_id, id, session_id, seq, url_hash, device_id,
			started_at, ended_at, active_seconds, max_progress, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING`,
			w.InstallationID, w.SegmentID, w.SessionID, w.Seq, w.URLHash, w.DeviceID,
			w.StartedAt, w.EndedAt, w.ActiveSeconds, w.MaxProgress, w.StartedAt.Add(segmentTTL))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			outcome = domain.PushAccepted
			if err := applySegment(ctx, tx, w); err != nil {
				return err
			}
		}
		return raiseArticleMax(ctx, tx, w)
	})
	return outcome, err
}

// applySegment adds a new segment to its article and day. The article meta and
// first_read_at are written only when the row is created (if_not_exists).
func applySegment(ctx context.Context, tx pgx.Tx, w domain.SegmentWrite) error {
	first, a := 0, w.Article
	if w.Seq == 0 {
		first = 1
	}
	if _, err := tx.Exec(ctx, `INSERT INTO articles (installation_id, url_hash, canonical_url, source, title, author,
		site_name, word_count, published_at, first_read_at, total_seconds, sessions, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 1)
		ON CONFLICT (installation_id, url_hash) DO UPDATE SET total_seconds = articles.total_seconds + EXCLUDED.total_seconds,
		sessions = articles.sessions + EXCLUDED.sessions, version = articles.version + 1`,
		w.InstallationID, w.URLHash, a.CanonicalURL, a.Source, a.Title, a.Author, a.SiteName, a.WordCount,
		a.PublishedAt, w.StartedAt, w.ActiveSeconds, first); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO days (installation_id, date, seconds, article_hashes, version)
		VALUES ($1, $2, $3, ARRAY[$4::text], 1)
		ON CONFLICT (installation_id, date) DO UPDATE SET seconds = days.seconds + EXCLUDED.seconds,
		version = days.version + 1, article_hashes = CASE WHEN $4 = ANY(days.article_hashes)
		THEN days.article_hashes ELSE array_append(days.article_hashes, $4) END`,
		w.InstallationID, w.Date, w.ActiveSeconds, w.URLHash)
	return err
}

// raiseArticleMax applies the monotonic updates (spec §3.2). A zero progress cannot
// win, so max_progress is left out of the update.
func raiseArticleMax(ctx context.Context, tx pgx.Tx, w domain.SegmentWrite) error {
	if w.MaxProgress == 0 {
		_, err := tx.Exec(ctx, `UPDATE articles SET last_read_at = GREATEST(COALESCE(last_read_at, 'epoch'::timestamptz), $3)
			WHERE installation_id = $1 AND url_hash = $2`, w.InstallationID, w.URLHash, w.EndedAt)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE articles SET max_progress = GREATEST(COALESCE(max_progress, 0), $4),
		last_read_at = GREATEST(COALESCE(last_read_at, 'epoch'::timestamptz), $3)
		WHERE installation_id = $1 AND url_hash = $2`, w.InstallationID, w.URLHash, w.EndedAt, w.MaxProgress)
	return err
}

// isConflict reports a serialization failure or deadlock, which a retry can clear.
func isConflict(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "40001" || pe.Code == "40P01")
}
