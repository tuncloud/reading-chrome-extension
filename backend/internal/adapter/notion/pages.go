package notion

import (
	"context"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

var _ port.NotionPages = (*Gateway)(nil)

// FindArticlePage looks a Library row up by URL. URLs Notion cannot store (> 2000
// chars) are never queried.
func (g *Gateway) FindArticlePage(ctx context.Context, token string, db domain.DBConfig, canonicalURL string) (*port.Page, error) {
	if len(canonicalURL) > notionMaxURL {
		return nil, nil
	}
	return g.findOne(ctx, token, db.LibraryDSID, map[string]any{
		"filter":    map[string]any{"property": db.Props["url"], "url": map[string]any{"equals": canonicalURL}},
		"page_size": 1,
	})
}

func (g *Gateway) CreateArticlePage(ctx context.Context, token string, db domain.DBConfig, a domain.Article, completed bool) (*port.Page, error) {
	p, err := g.Client.CreatePage(ctx, token, articleCreateBody(db.LibraryDSID, db.Props, a, completed))
	if err != nil {
		return nil, translate(err, port.NotionDataSourceMissing)
	}
	return &port.Page{ID: p.ID, URL: p.URL}, nil
}

// UpdateArticlePage PATCHes the server-owned metric properties only (§2.13 step 4).
func (g *Gateway) UpdateArticlePage(ctx context.Context, token string, db domain.DBConfig, pageID string, a domain.Article, completed bool) error {
	return g.updatePage(ctx, token, pageID, db.LibraryDSID, articleMetrics(db.Props, a, completed))
}

func (g *Gateway) FindDayPage(ctx context.Context, token string, db domain.DBConfig, date string) (*port.Page, error) {
	return g.findOne(ctx, token, db.DailyDSID, map[string]any{
		"filter":    map[string]any{"property": db.Props["day_date"], "date": map[string]any{"equals": date}},
		"page_size": 1,
	})
}

func (g *Gateway) CreateDayPage(ctx context.Context, token string, db domain.DBConfig, d domain.Day, articlePageIDs []string) (*port.Page, error) {
	props := map[string]any{
		db.Props["day_title"]: map[string]any{"title": richText(d.Date)},
		db.Props["day_date"]:  dateValue(d.Date),
	}
	for k, v := range dayMetrics(db.Props, d, articlePageIDs) {
		props[k] = v
	}
	p, err := g.Client.CreatePage(ctx, token, map[string]any{
		"parent":     map[string]any{"type": "data_source_id", "data_source_id": db.DailyDSID},
		"properties": props,
	})
	if err != nil {
		return nil, translate(err, port.NotionDataSourceMissing)
	}
	return &port.Page{ID: p.ID, URL: p.URL}, nil
}

func (g *Gateway) UpdateDayPage(ctx context.Context, token string, db domain.DBConfig, pageID string, d domain.Day, articlePageIDs []string) error {
	return g.updatePage(ctx, token, pageID, db.DailyDSID, dayMetrics(db.Props, d, articlePageIDs))
}

func (g *Gateway) findOne(ctx context.Context, token, dsID string, body map[string]any) (*port.Page, error) {
	res, err := g.Client.QueryDataSource(ctx, token, dsID, body)
	if err != nil {
		return nil, translate(err, port.NotionDataSourceMissing)
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return &port.Page{ID: res.Results[0].ID, URL: res.Results[0].URL}, nil
}

// updatePage PATCHes a row of data source dsID. A page that comes back archived or
// trashed, or whose parent is another data source (the installation moved to new
// databases), is gone: the caller re-creates it in dsID (scenario 10).
func (g *Gateway) updatePage(ctx context.Context, token, pageID, dsID string, props map[string]any) error {
	p, err := g.Client.UpdatePage(ctx, token, pageID, props)
	if err != nil {
		return translate(err, port.NotionPageGone)
	}
	if p.Archived || p.InTrash || (p.Parent.DataSourceID != "" && !domain.SameID(p.Parent.DataSourceID, dsID)) {
		return &port.NotionError{Kind: port.NotionPageGone, Message: "page archived or moved"}
	}
	return nil
}

// dayMetrics are the Daily Log's server-owned properties (§2.14).
func dayMetrics(p map[string]string, d domain.Day, pageIDs []string) map[string]any {
	rel := make([]any, len(pageIDs))
	for i, id := range pageIDs {
		rel[i] = map[string]any{"id": id}
	}
	return map[string]any{
		p["day_minutes"]:  map[string]any{"number": domain.RoundMinutes(d.Seconds)},
		p["day_articles"]: map[string]any{"relation": rel},
	}
}
