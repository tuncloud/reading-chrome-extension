package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) GetDay(ctx context.Context, inst, date string) (*domain.Day, error) {
	var (
		d                    = domain.Day{Date: date}
		pageID, dataSourceID *string
		syncedVersion        *int64
	)
	err := s.pool.QueryRow(ctx, `SELECT seconds, article_hashes, version, notion_page_id, notion_ds_id,
		notion_synced_version FROM days WHERE installation_id = $1 AND date = $2`, inst, date).
		Scan(&d.Seconds, &d.ArticleHashes, &d.Version, &pageID, &dataSourceID, &syncedVersion)
	if err != nil {
		return nil, notFound(err)
	}
	d.NotionPageID, d.NotionDataSourceID = strOut(pageID), strOut(dataSourceID)
	if syncedVersion != nil {
		d.NotionSyncedVersion = *syncedVersion
	}
	return &d, nil
}

func (s *Store) SetDayPage(ctx context.Context, inst, date, pageID, dataSourceID string) error {
	return s.guarded(ctx, `UPDATE days SET notion_page_id = $3, notion_ds_id = $4
		WHERE installation_id = $1 AND date = $2`, inst, date, pageID, dataSourceID)
}

func (s *Store) ClearDayPage(ctx context.Context, inst, date string) error {
	return s.exec(ctx, `UPDATE days SET notion_page_id = NULL, notion_ds_id = NULL
		WHERE installation_id = $1 AND date = $2`, inst, date)
}

// MarkDaySynced records the version written to Notion; an older version never wins.
func (s *Store) MarkDaySynced(ctx context.Context, inst, date string, version int64) error {
	return s.exec(ctx, `UPDATE days SET notion_synced_version = $3
		WHERE installation_id = $1 AND date = $2 AND (notion_synced_version IS NULL OR notion_synced_version < $3)`,
		inst, date, version)
}

// PendingSync lists the article hashes and dates whose version is ahead of Notion,
// or all of them when all is set (I1), each ascending.
func (s *Store) PendingSync(ctx context.Context, inst string, all bool) (hashes, dates []string, err error) {
	if hashes, err = s.column(ctx, `SELECT url_hash FROM articles WHERE installation_id = $1
		AND ($2 OR version > COALESCE(notion_synced_version, 0)) ORDER BY url_hash`, inst, all); err != nil {
		return nil, nil, err
	}
	if dates, err = s.column(ctx, `SELECT date FROM days WHERE installation_id = $1
		AND ($2 OR version > COALESCE(notion_synced_version, 0)) ORDER BY date`, inst, all); err != nil {
		return nil, nil, err
	}
	return hashes, dates, nil
}

// column runs a query returning one text column.
func (s *Store) column(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, tagged(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, tagged(err)
	}
	return out, nil
}
