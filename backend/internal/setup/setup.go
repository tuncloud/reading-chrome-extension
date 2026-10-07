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
	return s.resolve(ctx, token, first(childDatabaseIDs(blocks, LibraryTitle)), first(childDatabaseIDs(blocks, DailyTitle)))
}

// CreateDatabases creates both databases under parentPageID (spec §2.7). It is
// idempotent: a Library already under the parent (left by an earlier partial attempt)
// is reused, and so is the first Daily Log whose Articles relation targets that
// Library; only the missing ones are created. A Daily Log related to another Library
// (e.g. one the user deleted, scenario 10) is left alone and a new one is created.
// Library goes first because the Daily Log relation needs its data source id.
func (s *Service) CreateDatabases(ctx context.Context, token, parentPageID string) (store.DBConfig, error) {
	blocks, err := s.Notion.ListBlockChildren(ctx, token, parentPageID)
	if err != nil && !isNotFound(err) {
		return store.DBConfig{}, err
	}

	libID, libDSID := first(childDatabaseIDs(blocks, LibraryTitle)), ""
	if libID == "" {
		lib, err := s.Notion.CreateDatabase(ctx, token, parentPageID, LibraryTitle, libraryCreateSchema())
		if err != nil {
			return store.DBConfig{}, err
		}
		if len(lib.DataSources) == 0 {
			return store.DBConfig{}, fmt.Errorf("setup: created %s has no data source", LibraryTitle)
		}
		libID, libDSID = lib.ID, lib.DataSources[0].ID
	} else {
		lib, err := s.Notion.GetDatabase(ctx, token, libID)
		if isNotFound(err) || (err == nil && len(lib.DataSources) == 0) {
			return store.DBConfig{}, &Error{"database_missing:" + LibraryTitle}
		}
		if err != nil {
			return store.DBConfig{}, err
		}
		libDSID = lib.DataSources[0].ID
	}

	dailyID := ""
	for _, id := range childDatabaseIDs(blocks, DailyTitle) {
		ok, err := s.dailyLogRelatesTo(ctx, token, id, libDSID)
		if err != nil {
			return store.DBConfig{}, err
		}
		if ok {
			dailyID = id
			break
		}
	}
	if dailyID == "" {
		daily, err := s.Notion.CreateDatabase(ctx, token, parentPageID, DailyTitle, dailyCreateSchema(libDSID))
		if err != nil {
			return store.DBConfig{}, err
		}
		dailyID = daily.ID
	}
	return s.resolve(ctx, token, libID, dailyID)
}

// childDatabaseIDs lists the ids of child_database blocks titled title, in order.
func childDatabaseIDs(blocks []notion.Block, title string) []string {
	var ids []string
	for _, b := range blocks {
		if b.Type == "child_database" && b.ChildDatabase != nil && b.ChildDatabase.Title == title {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

func first(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// dailyLogRelatesTo reports whether Daily Log dbID's Articles relation targets
// libDSID. A database or data source that no longer exists does not qualify.
func (s *Service) dailyLogRelatesTo(ctx context.Context, token, dbID, libDSID string) (bool, error) {
	db, err := s.Notion.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ds, err := s.Notion.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return relatesTo(ds, libDSID), nil
}

// relatesTo reports whether daily's Articles relation targets data source libDSID.
func relatesTo(daily *notion.DataSource, libDSID string) bool {
	rel := daily.Properties[articlesProp].Relation
	return rel != nil && notion.SameID(rel.DataSourceID, libDSID)
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
	// The Articles relation must point at this Library, not at one the user deleted.
	if !relatesTo(dailyDS, libDS.ID) {
		return store.DBConfig{}, &Error{"property_type:" + articlesProp}
	}
	for k, v := range dailyProps {
		libProps[k] = v
	}
	return store.DBConfig{LibraryDBID: libDBID, LibraryDSID: libDS.ID, LibraryURL: libURL,
		DailyDBID: dailyDBID, DailyDSID: dailyDS.ID, Props: libProps}, nil
}

func (s *Service) resolveDatabase(ctx context.Context, token, dbID, title string, specs []PropSpec) (ds *notion.DataSource, url string, props map[string]string, err error) {
	db, err := s.Notion.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return nil, "", nil, &Error{"database_missing:" + title}
	}
	if err != nil {
		return nil, "", nil, err
	}
	ds, err = s.Notion.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return nil, "", nil, &Error{"database_missing:" + title}
	}
	if err != nil {
		return nil, "", nil, err
	}
	props = map[string]string{}
	for _, spec := range specs {
		p, ok := ds.Properties[spec.Name]
		if !ok {
			return nil, "", nil, &Error{"property_missing:" + spec.Name}
		}
		if p.Type != spec.Type {
			return nil, "", nil, &Error{"property_type:" + spec.Name}
		}
		props[spec.Key] = p.ID
	}
	return ds, db.URL, props, nil
}

// Resync enqueues every ART/DAY whose version is ahead of Notion (T22). Call it
// whenever an installation becomes active. full re-sends every ART/DAY: use it when
// the installation's data sources changed, so pages left in the old databases are
// re-created in the new ones (scenario 10).
func (s *Service) Resync(ctx context.Context, installationID string, full bool) error {
	hashes, dates, err := s.Store.PendingSync(ctx, installationID, full)
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
