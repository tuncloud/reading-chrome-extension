package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"reading-tracker/backend/internal/domain"
)

const articleColumns = `url_hash, canonical_url, source, title, author, site_name, word_count, published_at,
	first_read_at, last_read_at, total_seconds, sessions, max_progress, completed, version,
	notion_page_id, notion_url, notion_ds_id, notion_synced_version, notion_synced_at`

func scanArticle(row pgx.Row) (*domain.Article, error) {
	var (
		a                               domain.Article
		firstRead, lastRead, syncedAt   *time.Time
		maxProgress                     *float64
		pageID, notionURL, dataSourceID *string
		syncedVersion                   *int64
	)
	if err := row.Scan(&a.Hash, &a.CanonicalURL, &a.Source, &a.Title, &a.Author, &a.SiteName, &a.WordCount,
		&a.PublishedAt, &firstRead, &lastRead, &a.TotalSeconds, &a.Sessions, &maxProgress, &a.Completed, &a.Version,
		&pageID, &notionURL, &dataSourceID, &syncedVersion, &syncedAt); err != nil {
		return nil, err
	}
	a.FirstReadAt, a.LastReadAt, a.NotionSyncedAt = tsOut(firstRead), tsOut(lastRead), tsOut(syncedAt)
	a.NotionPageID, a.NotionURL, a.NotionDataSourceID = strOut(pageID), strOut(notionURL), strOut(dataSourceID)
	if maxProgress != nil {
		a.MaxProgress = *maxProgress
	}
	if syncedVersion != nil {
		a.NotionSyncedVersion = *syncedVersion
	}
	return &a, nil
}

func (s *Store) GetArticle(ctx context.Context, inst, hash string) (*domain.Article, error) {
	a, err := scanArticle(s.pool.QueryRow(ctx, `SELECT `+articleColumns+` FROM articles
		WHERE installation_id = $1 AND url_hash = $2`, inst, hash))
	if err != nil {
		return nil, notFound(err)
	}
	return a, nil
}

// BatchGetArticles loads articles keyed by hash; missing ones are absent from the map.
func (s *Store) BatchGetArticles(ctx context.Context, inst string, hashes []string) (map[string]*domain.Article, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+articleColumns+` FROM articles
		WHERE installation_id = $1 AND url_hash = ANY($2)`, inst, hashes)
	if err != nil {
		return nil, tagged(err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*domain.Article, error) { return scanArticle(r) })
	if err != nil {
		return nil, tagged(err)
	}
	out := make(map[string]*domain.Article, len(list))
	for _, a := range list {
		out[a.Hash] = a
	}
	return out, nil
}

func (s *Store) SetArticlePage(ctx context.Context, inst, hash, pageID, url, dataSourceID string) error {
	return s.guarded(ctx, `UPDATE articles SET notion_page_id = $3, notion_url = $4, notion_ds_id = $5
		WHERE installation_id = $1 AND url_hash = $2`, inst, hash, pageID, url, dataSourceID)
}

func (s *Store) ClearArticlePage(ctx context.Context, inst, hash string) error {
	return s.exec(ctx, `UPDATE articles SET notion_page_id = NULL, notion_url = NULL, notion_ds_id = NULL
		WHERE installation_id = $1 AND url_hash = $2`, inst, hash)
}

// MarkArticleSynced records the version written to Notion (spec §2.13 step 5).
// An older version never overwrites a newer one; a write that loses is a no-op.
func (s *Store) MarkArticleSynced(ctx context.Context, inst, hash string, version int64, completed bool, now time.Time) error {
	return s.exec(ctx, `UPDATE articles SET notion_synced_version = $3, notion_synced_at = $4, completed = $5
		WHERE installation_id = $1 AND url_hash = $2 AND (notion_synced_version IS NULL OR notion_synced_version < $3)`,
		inst, hash, version, now, completed)
}
