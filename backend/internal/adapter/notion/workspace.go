package notion

import (
	"context"
	"fmt"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// ResolveTemplate finds "Reading Library" and "Daily Log" inside a duplicated
// template page and resolves their data sources and property ids (spec §2.11).
func (g *Gateway) ResolveTemplate(ctx context.Context, token, templateID string) (domain.DBConfig, error) {
	db, err := g.resolveTemplate(ctx, token, templateID)
	return db, translate(err, port.NotionNotFound)
}

// ProvisionDatabases creates both databases under parentPageID (spec §2.7). It is
// idempotent: a Library already under the parent (left by an earlier partial attempt)
// is reused, and so is the first Daily Log whose Articles relation targets that
// Library; only the missing ones are created. A Daily Log related to another Library
// (e.g. one the user deleted, scenario 10) is left alone and a new one is created.
// Library goes first because the Daily Log relation needs its data source id.
func (g *Gateway) ProvisionDatabases(ctx context.Context, token, parentPageID string) (domain.DBConfig, error) {
	db, err := g.provision(ctx, token, parentPageID)
	return db, translate(err, port.NotionNotFound)
}

func (g *Gateway) resolveTemplate(ctx context.Context, token, templateID string) (domain.DBConfig, error) {
	blocks, err := g.Client.ListBlockChildren(ctx, token, templateID)
	if isNotFound(err) {
		return domain.DBConfig{}, &domain.SetupError{Reason: "template_not_found"}
	}
	if err != nil {
		return domain.DBConfig{}, err
	}
	return g.resolve(ctx, token, first(childDatabaseIDs(blocks, LibraryTitle)), first(childDatabaseIDs(blocks, DailyTitle)))
}

func (g *Gateway) provision(ctx context.Context, token, parentPageID string) (domain.DBConfig, error) {
	blocks, err := g.Client.ListBlockChildren(ctx, token, parentPageID)
	if err != nil && !isNotFound(err) {
		return domain.DBConfig{}, err
	}

	libID, libDSID := first(childDatabaseIDs(blocks, LibraryTitle)), ""
	if libID == "" {
		lib, err := g.Client.CreateDatabase(ctx, token, parentPageID, LibraryTitle, libraryCreateSchema())
		if err != nil {
			return domain.DBConfig{}, err
		}
		if len(lib.DataSources) == 0 {
			return domain.DBConfig{}, fmt.Errorf("notion: created %s has no data source", LibraryTitle)
		}
		libID, libDSID = lib.ID, lib.DataSources[0].ID
	} else {
		lib, err := g.Client.GetDatabase(ctx, token, libID)
		if isNotFound(err) || (err == nil && len(lib.DataSources) == 0) {
			return domain.DBConfig{}, &domain.SetupError{Reason: "database_missing:" + LibraryTitle}
		}
		if err != nil {
			return domain.DBConfig{}, err
		}
		libDSID = lib.DataSources[0].ID
	}

	dailyID := ""
	for _, id := range childDatabaseIDs(blocks, DailyTitle) {
		ok, err := g.dailyLogRelatesTo(ctx, token, id, libDSID)
		if err != nil {
			return domain.DBConfig{}, err
		}
		if ok {
			dailyID = id
			break
		}
	}
	if dailyID == "" {
		daily, err := g.Client.CreateDatabase(ctx, token, parentPageID, DailyTitle, dailyCreateSchema(libDSID))
		if err != nil {
			return domain.DBConfig{}, err
		}
		dailyID = daily.ID
	}
	return g.resolve(ctx, token, libID, dailyID)
}

// childDatabaseIDs lists the ids of child_database blocks titled title, in order.
func childDatabaseIDs(blocks []Block, title string) []string {
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
func (g *Gateway) dailyLogRelatesTo(ctx context.Context, token, dbID, libDSID string) (bool, error) {
	db, err := g.Client.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ds, err := g.Client.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return relatesTo(ds, libDSID), nil
}

// relatesTo reports whether daily's Articles relation targets data source libDSID.
func relatesTo(daily *DataSource, libDSID string) bool {
	rel := daily.Properties[articlesProp].Relation
	return rel != nil && domain.SameID(rel.DataSourceID, libDSID)
}

func (g *Gateway) resolve(ctx context.Context, token, libDBID, dailyDBID string) (domain.DBConfig, error) {
	if libDBID == "" {
		return domain.DBConfig{}, &domain.SetupError{Reason: "database_missing:" + LibraryTitle}
	}
	if dailyDBID == "" {
		return domain.DBConfig{}, &domain.SetupError{Reason: "database_missing:" + DailyTitle}
	}
	libDS, libURL, libProps, err := g.resolveDatabase(ctx, token, libDBID, LibraryTitle, LibraryProps)
	if err != nil {
		return domain.DBConfig{}, err
	}
	dailyDS, _, dailyProps, err := g.resolveDatabase(ctx, token, dailyDBID, DailyTitle, DailyProps)
	if err != nil {
		return domain.DBConfig{}, err
	}
	// The Articles relation must point at this Library, not at one the user deleted.
	if !relatesTo(dailyDS, libDS.ID) {
		return domain.DBConfig{}, &domain.SetupError{Reason: "property_type:" + articlesProp}
	}
	for k, v := range dailyProps {
		libProps[k] = v
	}
	return domain.DBConfig{LibraryDBID: libDBID, LibraryDSID: libDS.ID, LibraryURL: libURL,
		DailyDBID: dailyDBID, DailyDSID: dailyDS.ID, Props: libProps}, nil
}

func (g *Gateway) resolveDatabase(ctx context.Context, token, dbID, title string, specs []PropSpec) (ds *DataSource, url string, props map[string]string, err error) {
	db, err := g.Client.GetDatabase(ctx, token, dbID)
	if isNotFound(err) || (err == nil && len(db.DataSources) == 0) {
		return nil, "", nil, &domain.SetupError{Reason: "database_missing:" + title}
	}
	if err != nil {
		return nil, "", nil, err
	}
	ds, err = g.Client.GetDataSource(ctx, token, db.DataSources[0].ID)
	if isNotFound(err) {
		return nil, "", nil, &domain.SetupError{Reason: "database_missing:" + title}
	}
	if err != nil {
		return nil, "", nil, err
	}
	props = map[string]string{}
	for _, spec := range specs {
		p, ok := ds.Properties[spec.Name]
		if !ok {
			return nil, "", nil, &domain.SetupError{Reason: "property_missing:" + spec.Name}
		}
		if p.Type != spec.Type {
			return nil, "", nil, &domain.SetupError{Reason: "property_type:" + spec.Name}
		}
		props[spec.Key] = p.ID
	}
	return ds, db.URL, props, nil
}
