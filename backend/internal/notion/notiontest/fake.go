// Package notiontest is an in-memory fake of the Notion endpoints the backend uses.
// It enforces Notion-Version, rejects page writes (400 validation_error, nothing stored)
// whose property keys are neither a property id nor a property name in the data source
// schema, and can inject failures, rejected tokens, archived pages and deleted data
// sources. Accepted properties are stored and returned keyed as sent; real Notion keys
// response properties by name, but the client never reads response properties.
package notiontest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
)

type Request struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   map[string]any
}

type Fail struct {
	Status     int
	Code       string
	Message    string
	RetryAfter int // seconds; 0 = no header
	Times      int // matching calls to fail; 0 means 1
}

type Page struct {
	ID         string
	URL        string
	DataSource string
	Properties map[string]any // keyed by property id
	Children   []any
	Archived   bool
}

func (p *Page) json() map[string]any {
	return map[string]any{"object": "page", "id": p.ID, "url": p.URL, "archived": p.Archived, "in_trash": p.Archived,
		"parent": map[string]any{"type": "data_source_id", "data_source_id": p.DataSource}, "properties": p.Properties}
}

// LibrarySchema and DailySchema are the template's columns (name → type) per spec §3.3.
var LibrarySchema = map[string]string{
	"Name": "title", "URL": "url", "Source": "select", "Author": "rich_text", "Site": "select",
	"Word count": "number", "First read": "date", "Last read": "date", "Reading time (min)": "number",
	"Progress": "number", "Sessions": "number", "Completed": "checkbox",
	"Tags": "multi_select", "Status": "status", "Rating": "select", "Notes": "rich_text",
}

var DailySchema = map[string]string{"Name": "title", "Date": "date", "Minutes": "number", "Articles": "relation"}

// PropID is the fake's property id for a column name.
func PropID(name string) string { return "id-" + strings.ReplaceAll(strings.ToLower(name), " ", "-") }

type Template struct{ ID, LibraryDB, LibraryDS, DailyDB, DailyDS string }

// DBConfig is what template resolution should produce for this template.
func (t Template) DBConfig() store.DBConfig {
	props := map[string]string{}
	for _, p := range setup.LibraryProps {
		props[p.Key] = PropID(p.Name)
	}
	for _, p := range setup.DailyProps {
		props[p.Key] = PropID(p.Name)
	}
	return store.DBConfig{LibraryDBID: t.LibraryDB, LibraryDSID: t.LibraryDS, LibraryURL: "https://www.notion.so/" + t.LibraryDB,
		DailyDBID: t.DailyDB, DailyDSID: t.DailyDS, Props: props}
}

type Server struct {
	*httptest.Server

	mu              sync.Mutex
	reqs            []Request
	fails           map[string]*Fail
	rejected        map[string]bool
	tokenResponse   map[string]any
	refreshResponse map[string]any
	searchResults   []map[string]any
	blocks          map[string][]map[string]any
	databases       map[string]map[string]any
	dataSources     map[string]map[string]any
	pages           map[string]*Page
	order           []string
	seq             int
}

func New(t testing.TB) *Server {
	s := &Server{
		fails: map[string]*Fail{}, rejected: map[string]bool{}, blocks: map[string][]map[string]any{},
		databases: map[string]map[string]any{}, dataSources: map[string]map[string]any{}, pages: map[string]*Page{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// ChildDatabase builds a child_database block.
func ChildDatabase(id, title string) map[string]any {
	return map[string]any{"object": "block", "id": id, "type": "child_database", "child_database": map[string]any{"title": title}}
}

// AddTemplate registers a duplicated template page holding both databases.
func (s *Server) AddTemplate(id string) Template {
	tpl := Template{ID: id, LibraryDB: id + "-lib-db", LibraryDS: id + "-lib-ds", DailyDB: id + "-daily-db", DailyDS: id + "-daily-ds"}
	s.AddDatabase(tpl.LibraryDB, tpl.LibraryDS, LibrarySchema)
	s.AddDatabase(tpl.DailyDB, tpl.DailyDS, DailySchema)
	s.SetBlocks(id, map[string]any{"object": "block", "id": "intro", "type": "paragraph"},
		ChildDatabase(tpl.LibraryDB, setup.LibraryTitle), ChildDatabase(tpl.DailyDB, setup.DailyTitle))
	return tpl
}

func (s *Server) AddDatabase(dbID, dsID string, schema map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addDatabaseLocked(dbID, dsID, schema)
}

func (s *Server) addDatabaseLocked(dbID, dsID string, schema map[string]string) map[string]any {
	props := map[string]any{}
	for name, typ := range schema {
		props[name] = map[string]any{"id": PropID(name), "name": name, "type": typ}
	}
	s.dataSources[dsID] = map[string]any{"object": "data_source", "id": dsID, "properties": props}
	db := map[string]any{"object": "database", "id": dbID, "url": "https://www.notion.so/" + dbID,
		"data_sources": []any{map[string]any{"id": dsID, "name": "Default"}}}
	s.databases[dbID] = db
	return db
}

func (s *Server) SetBlocks(parentID string, blocks ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocks[parentID] = blocks
}

func (s *Server) RemoveProperty(dsID, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.dataSources[dsID]["properties"].(map[string]any), name)
}

func (s *Server) SetPropertyType(dsID, name, typ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dataSources[dsID]["properties"].(map[string]any)[name] = map[string]any{"id": PropID(name), "name": name, "type": typ}
}

// DeleteDataSource simulates the user deleting a database (scenario 10).
func (s *Server) DeleteDataSource(dsID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.dataSources, dsID)
}

// AddPage inserts a page as if a user (or an earlier attempt) had created it.
func (s *Server) AddPage(dsID string, props map[string]any) *Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newPageLocked(dsID, props)
}

func (s *Server) Page(id string) *Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pages[id]
}

// Pages lists pages of a data source in creation order.
func (s *Server) Pages(dsID string) []*Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Page
	for _, id := range s.order {
		if p := s.pages[id]; p.DataSource == dsID {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) ArchivePage(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[id].Archived = true
}

// SetPageProperty simulates a user editing a column in Notion.
func (s *Server) SetPageProperty(pageID, propID string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[pageID].Properties[propID] = v
}

// RejectToken makes every request bearing tok fail with 401.
func (s *Server) RejectToken(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected[tok] = true
}

// FailNext makes the next f.Times calls to method+path return f.
func (s *Server) FailNext(method, path string, f Fail) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Times == 0 {
		f.Times = 1
	}
	s.fails[method+" "+path] = &f
}

func (s *Server) SetTokenResponse(r map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenResponse = r
}

// SetRefreshResponse sets the refresh_token grant response; nil → 400 invalid_grant.
func (s *Server) SetRefreshResponse(r map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshResponse = r
}

func (s *Server) SetSearchResults(results ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchResults = results
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

// Calls filters Requests by method and path prefix.
func (s *Server) Calls(method, pathPrefix string) []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, pathPrefix) {
			out = append(out, r)
		}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), Body: body})

	if r.Header.Get("Notion-Version") != "2025-09-03" {
		writeErr(w, Fail{Status: 400, Code: "missing_version", Message: "Notion-Version must be 2025-09-03"})
		return
	}
	if f := s.takeFail(r.Method + " " + r.URL.Path); f != nil {
		writeErr(w, *f)
		return
	}
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.rejected[tok] {
		writeErr(w, Fail{Status: 401, Code: "unauthorized", Message: "API token is invalid."})
		return
	}
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/oauth/token":
		s.token(w, body)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/search":
		writeJSON(w, map[string]any{"object": "list", "results": s.searchResults, "has_more": false})
	case r.Method == http.MethodGet && len(p) == 4 && p[1] == "blocks" && p[3] == "children":
		blocks, ok := s.blocks[p[2]]
		if !ok {
			writeErr(w, notFound("block"))
			return
		}
		writeJSON(w, map[string]any{"object": "list", "results": blocks, "has_more": false, "next_cursor": nil})
	case r.Method == http.MethodGet && len(p) == 3 && p[1] == "databases":
		db, ok := s.databases[p[2]]
		if !ok {
			writeErr(w, notFound("database"))
			return
		}
		writeJSON(w, db)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/databases":
		s.createDatabase(w, body)
	case r.Method == http.MethodGet && len(p) == 3 && p[1] == "data_sources":
		ds, ok := s.dataSources[p[2]]
		if !ok {
			writeErr(w, notFound("data source"))
			return
		}
		writeJSON(w, ds)
	case r.Method == http.MethodPost && len(p) == 4 && p[1] == "data_sources" && p[3] == "query":
		s.query(w, p[2], body)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/pages":
		s.createPage(w, body)
	case r.Method == http.MethodPatch && len(p) == 3 && p[1] == "pages":
		s.updatePage(w, p[2], body)
	default:
		writeErr(w, Fail{Status: 400, Code: "fake_unhandled", Message: r.Method + " " + r.URL.Path})
	}
}

func (s *Server) takeFail(k string) *Fail {
	f := s.fails[k]
	if f == nil {
		return nil
	}
	if f.Times--; f.Times <= 0 {
		delete(s.fails, k)
	}
	return f
}

func (s *Server) token(w http.ResponseWriter, body map[string]any) {
	var resp map[string]any
	switch body["grant_type"] {
	case "authorization_code":
		resp = s.tokenResponse
	case "refresh_token":
		resp = s.refreshResponse
	}
	if resp == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "grant is invalid"})
		return
	}
	writeJSON(w, resp)
}

func (s *Server) createDatabase(w http.ResponseWriter, body map[string]any) {
	parent, _ := body["parent"].(map[string]any)
	if _, ok := parent["page_id"].(string); !ok {
		writeErr(w, Fail{Status: 400, Code: "validation_error", Message: "parent.page_id is required"})
		return
	}
	s.seq++
	dbID, dsID := fmt.Sprintf("db-%d", s.seq), fmt.Sprintf("ds-%d", s.seq)
	ids, _ := body["initial_data_source"].(map[string]any)
	props, _ := ids["properties"].(map[string]any)
	schema := map[string]string{}
	for name, cfg := range props {
		for k := range cfg.(map[string]any) {
			if k != "type" && k != "name" {
				schema[name] = k
			}
		}
	}
	db := s.addDatabaseLocked(dbID, dsID, schema)
	// Like real Notion, the new database is listed among its parent page's children.
	parentID, _ := parent["page_id"].(string)
	s.blocks[parentID] = append(s.blocks[parentID], ChildDatabase(dbID, plainTitle(body["title"])))
	writeJSON(w, db)
}

// plainTitle joins the text content of a rich-text title array.
func plainTitle(v any) string {
	var b strings.Builder
	items, _ := v.([]any)
	for _, it := range items {
		item, _ := it.(map[string]any)
		text, _ := item["text"].(map[string]any)
		content, _ := text["content"].(string)
		b.WriteString(content)
	}
	return b.String()
}

func (s *Server) query(w http.ResponseWriter, dsID string, body map[string]any) {
	if _, ok := s.dataSources[dsID]; !ok {
		writeErr(w, notFound("data source"))
		return
	}
	filter, _ := body["filter"].(map[string]any)
	prop, _ := filter["property"].(string)
	results := []any{}
	for _, id := range s.order {
		pg := s.pages[id]
		if pg.DataSource == dsID && !pg.Archived && matches(pg.Properties[prop], filter) {
			results = append(results, pg.json())
		}
	}
	writeJSON(w, map[string]any{"object": "list", "results": results, "has_more": false})
}

func matches(val any, filter map[string]any) bool {
	v, _ := val.(map[string]any)
	if v == nil {
		return false
	}
	if f, ok := filter["url"].(map[string]any); ok {
		return v["url"] == f["equals"]
	}
	if f, ok := filter["date"].(map[string]any); ok {
		d, _ := v["date"].(map[string]any)
		return d != nil && d["start"] == f["equals"]
	}
	return false
}

func (s *Server) createPage(w http.ResponseWriter, body map[string]any) {
	parent, _ := body["parent"].(map[string]any)
	dsID, _ := parent["data_source_id"].(string)
	if parent["type"] != "data_source_id" {
		writeErr(w, Fail{Status: 400, Code: "validation_error", Message: "parent.type must be data_source_id"})
		return
	}
	if _, ok := s.dataSources[dsID]; !ok {
		writeErr(w, notFound("data source"))
		return
	}
	props, _ := body["properties"].(map[string]any)
	if !s.validPropertiesLocked(w, dsID, props) {
		return
	}
	pg := s.newPageLocked(dsID, props)
	pg.Children, _ = body["children"].([]any)
	writeJSON(w, pg.json())
}

func (s *Server) newPageLocked(dsID string, props map[string]any) *Page {
	s.seq++
	id := fmt.Sprintf("page-%d", s.seq)
	if props == nil {
		props = map[string]any{}
	}
	pg := &Page{ID: id, URL: "https://www.notion.so/" + id, DataSource: dsID, Properties: props}
	s.pages[id] = pg
	s.order = append(s.order, id)
	return pg
}

func (s *Server) updatePage(w http.ResponseWriter, id string, body map[string]any) {
	pg, ok := s.pages[id]
	if !ok {
		writeErr(w, notFound("page"))
		return
	}
	if pg.Archived {
		writeErr(w, Fail{Status: 400, Code: "validation_error", Message: "Can't edit block that is archived. You must unarchive the block before editing."})
		return
	}
	props, _ := body["properties"].(map[string]any)
	if !s.validPropertiesLocked(w, pg.DataSource, props) {
		return
	}
	for k, v := range props {
		pg.Properties[k] = v
	}
	writeJSON(w, pg.json())
}

// validPropertiesLocked writes a 400 validation_error and returns false when props has
// a key that is neither a property id nor a property name of the data source. A data
// source that no longer exists (user deleted it) is not validated against.
func (s *Server) validPropertiesLocked(w http.ResponseWriter, dsID string, props map[string]any) bool {
	ds, ok := s.dataSources[dsID]
	if !ok {
		return true
	}
	schema, _ := ds["properties"].(map[string]any)
	known := map[string]bool{}
	for name, p := range schema {
		known[name] = true
		if prop, ok := p.(map[string]any); ok {
			if id, ok := prop["id"].(string); ok {
				known[id] = true
			}
		}
	}
	var unknown []string
	for k := range props {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return true
	}
	sort.Strings(unknown)
	writeErr(w, Fail{Status: 400, Code: "validation_error",
		Message: fmt.Sprintf("%s is not a property that exists.", strings.Join(unknown, ", "))})
	return false
}

func notFound(what string) Fail {
	return Fail{Status: 404, Code: "object_not_found", Message: "Could not find " + what}
}

func writeErr(w http.ResponseWriter, f Fail) {
	if f.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(f.RetryAfter))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.Status)
	json.NewEncoder(w).Encode(map[string]any{"object": "error", "status": f.Status, "code": f.Code, "message": f.Message})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
