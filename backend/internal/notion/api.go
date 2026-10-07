package notion

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

type PageRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// SearchPages lists pages the integration can see (spec §2.6).
func (c *Client) SearchPages(ctx context.Context, token, query string) ([]PageRef, error) {
	body := map[string]any{
		"query":     query,
		"filter":    map[string]string{"property": "object", "value": "page"},
		"page_size": 25,
	}
	var res struct {
		Results []struct {
			ID         string `json:"id"`
			URL        string `json:"url"`
			Properties map[string]struct {
				Type  string `json:"type"`
				Title []struct {
					PlainText string `json:"plain_text"`
				} `json:"title"`
			} `json:"properties"`
		} `json:"results"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/search", bearer(token), body, &res); err != nil {
		return nil, err
	}
	pages := make([]PageRef, 0, len(res.Results))
	for _, r := range res.Results {
		var title strings.Builder
		for _, p := range r.Properties {
			if p.Type == "title" {
				for _, t := range p.Title {
					title.WriteString(t.PlainText)
				}
			}
		}
		pages = append(pages, PageRef{ID: r.ID, Title: title.String(), URL: r.URL})
	}
	return pages, nil
}

type Block struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	ChildDatabase *ChildDatabase `json:"child_database,omitempty"`
}

type ChildDatabase struct {
	Title string `json:"title"`
}

// ListBlockChildren returns every child block, following start_cursor pagination.
func (c *Client) ListBlockChildren(ctx context.Context, token, blockID string) ([]Block, error) {
	var all []Block
	cursor := ""
	for {
		path := "/v1/blocks/" + url.PathEscape(blockID) + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		var res struct {
			Results    []Block `json:"results"`
			HasMore    bool    `json:"has_more"`
			NextCursor string  `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, path, bearer(token), nil, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Results...)
		if !res.HasMore || res.NextCursor == "" {
			return all, nil
		}
		cursor = res.NextCursor
	}
}

type DataSourceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Database struct {
	ID          string          `json:"id"`
	URL         string          `json:"url"`
	DataSources []DataSourceRef `json:"data_sources"`
}

func (c *Client) GetDatabase(ctx context.Context, token, id string) (*Database, error) {
	var db Database
	if err := c.do(ctx, http.MethodGet, "/v1/databases/"+url.PathEscape(id), bearer(token), nil, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// CreateDatabase creates a database with one initial data source (2025-09-03 shape).
func (c *Client) CreateDatabase(ctx context.Context, token, parentPageID, title string, properties map[string]any) (*Database, error) {
	body := map[string]any{
		"parent":              map[string]any{"type": "page_id", "page_id": parentPageID},
		"title":               []any{map[string]any{"type": "text", "text": map[string]any{"content": title}}},
		"initial_data_source": map[string]any{"properties": properties},
	}
	var db Database
	if err := c.do(ctx, http.MethodPost, "/v1/databases", bearer(token), body, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

type Property struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Relation *RelationConfig `json:"relation,omitempty"`
}

// RelationConfig is a relation property's target data source.
type RelationConfig struct {
	DataSourceID string `json:"data_source_id"`
}

// DataSource.Properties is keyed by property name.
type DataSource struct {
	ID         string              `json:"id"`
	Properties map[string]Property `json:"properties"`
}

func (c *Client) GetDataSource(ctx context.Context, token, id string) (*DataSource, error) {
	var ds DataSource
	if err := c.do(ctx, http.MethodGet, "/v1/data_sources/"+url.PathEscape(id), bearer(token), nil, &ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

type Page struct {
	ID       string     `json:"id"`
	URL      string     `json:"url"`
	Archived bool       `json:"archived"`
	InTrash  bool       `json:"in_trash"`
	Parent   PageParent `json:"parent"`
}

// PageParent is where a page lives; DataSourceID is set for database rows.
type PageParent struct {
	Type         string `json:"type"`
	DataSourceID string `json:"data_source_id"`
}

// SameID reports whether two Notion ids are equal, ignoring dashes and case
// (Notion accepts and returns both forms).
func SameID(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	return norm(a) == norm(b)
}

type QueryResult struct {
	Results []Page `json:"results"`
}

func (c *Client) QueryDataSource(ctx context.Context, token, id string, body map[string]any) (*QueryResult, error) {
	var res QueryResult
	if err := c.do(ctx, http.MethodPost, "/v1/data_sources/"+url.PathEscape(id)+"/query", bearer(token), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *Client) CreatePage(ctx context.Context, token string, body map[string]any) (*Page, error) {
	var p Page
	if err := c.do(ctx, http.MethodPost, "/v1/pages", bearer(token), body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdatePage PATCHes only the given properties (keyed by property id).
func (c *Client) UpdatePage(ctx context.Context, token, pageID string, properties map[string]any) (*Page, error) {
	var p Page
	body := map[string]any{"properties": properties}
	if err := c.do(ctx, http.MethodPatch, "/v1/pages/"+url.PathEscape(pageID), bearer(token), body, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
