// Package setup resolves or creates the user's Notion databases and resyncs
// pending data when an installation becomes active (spec §2.7, §2.11, T22).
package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

// Error is a setup failure whose Reason is stored in INST.setup_error.
type Error struct{ Reason string }

func (e *Error) Error() string { return "setup: " + e.Reason }

// Reason returns the setup_error value for err. Unexpected errors map to "notion_error".
func Reason(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return "notion_error"
}

type Service struct {
	Notion *notion.Client
	Store  *store.Store
	Queue  queue.Sender
	Bucket time.Duration
	Now    func() time.Time
}

// ResolveTemplate finds "Reading Library" and "Daily Log" inside a duplicated
// template page and resolves their data sources and property ids (spec §2.11).
func (s *Service) ResolveTemplate(ctx context.Context, token, templateID string) (store.DBConfig, error) {
	blocks, err := s.Notion.ListBlockChildren(ctx, token, templateID)
	if isNotFound(err) {
		return store.DBConfig{}, &Error{"template_not_found"}
	}
	if err != nil {
		return store.DBConfig{}, err
	}
	ids := map[string]string{}
	for _, b := range blocks {
		if b.Type == "child_database" && b.ChildDatabase != nil {
			if _, seen := ids[b.ChildDatabase.Title]; !seen {
				ids[b.ChildDatabase.Title] = b.ID
			}
		}
	}
	return s.resolve(ctx, token, ids[LibraryTitle], ids[DailyTitle])
}

// CreateDatabases creates both databases under parentPageID (spec §2.7). Library
// goes first because the Daily Log relation needs its data source id.
func (s *Service) CreateDatabases(ctx context.Context, token, parentPageID string) (store.DBConfig, error) {
	lib, err := s.Notion.CreateDatabase(ctx, token, parentPageID, LibraryTitle, libraryCreateSchema())
	if err != nil {
		return store.DBConfig{}, err
	}
	if len(lib.DataSources) == 0 {
		return store.DBConfig{}, fmt.Errorf("setup: created %s has no data source", LibraryTitle)
	}
	daily, err := s.Notion.CreateDatabase(ctx, token, parentPageID, DailyTitle, dailyCreateSchema(lib.DataSources[0].ID))
	if err != nil {
		return store.DBConfig{}, err
	}
	return s.resolve(ctx, token, lib.ID, daily.ID)
}

func (s *Service) resolve(ctx context.Context, token, libDBID, dailyDBID string) (store.DBConfig, error) {
	if libDBID == "" {
		return store.DBConfig{}, &Error{"database_missing:" + LibraryTitle}
	}
	if dailyDBID == "" {
		return store.DBConfig{}, &Error{"database_missing:" + DailyTitle}
	}
	libDS, libURL, libProps, err := s.resolveDatabase(ctx, token, libDBID, LibraryTitle, LibraryProps)
	if err != nil {
		return store.DBConfig{}, err
	}
	dailyDS, _, dailyProps, err := s.resolveDatabase(ctx, token, dailyDBID, DailyTitle, DailyProps)
	if err != nil {
		return store.DBConfig{}, err
	}
	for k, v := range dailyProps {
		libProps[k] = v
	}
	return store.DBConfig{LibraryDBID: libDBID, LibraryDSID: libDS, LibraryURL: libURL,
		DailyDBID: dailyDBID, DailyDSID: dailyDS, Props: libProps}, nil
}

func (s *Service) resolveDatabase(ctx context.Context, token, dbID, title string, specs []PropSpec) (dsID, url string, props map[string]string, err error) {
	db, err := s.Notion.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return "", "", nil, &Error{"database_missing:" + title}
	}
	if err != nil {
		return "", "", nil, err
	}
	ds, err := s.Notion.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return "", "", nil, &Error{"database_missing:" + title}
	}
	if err != nil {
		return "", "", nil, err
	}
	props = map[string]string{}
	for _, spec := range specs {
		p, ok := ds.Properties[spec.Name]
		if !ok {
			return "", "", nil, &Error{"property_missing:" + spec.Name}
		}
		if p.Type != spec.Type {
			return "", "", nil, &Error{"property_type:" + spec.Name}
		}
		props[spec.Key] = p.ID
	}
	return ds.ID, db.URL, props, nil
}

// Resync enqueues every ART/DAY whose version is ahead of Notion (T22). Call it
// whenever an installation becomes active.
func (s *Service) Resync(ctx context.Context, installationID string) error {
	hashes, dates, err := s.Store.PendingSync(ctx, installationID)
	if err != nil {
		return err
	}
	msgs := make([]queue.Message, 0, len(hashes)+len(dates))
	for _, h := range hashes {
		msgs = append(msgs, queue.Article(installationID, h))
	}
	for _, d := range dates {
		msgs = append(msgs, queue.Day(installationID, d))
	}
	if len(msgs) == 0 {
		return nil
	}
	return s.Queue.Send(ctx, queue.Bucketed(s.Now(), s.Bucket, msgs...)...)
}

func isNotFound(err error) bool {
	e, ok := notion.AsAPIError(err)
	return ok && e.Status == http.StatusNotFound
}
