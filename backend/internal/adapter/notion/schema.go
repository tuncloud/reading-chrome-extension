package notion

import "reading-tracker/backend/internal/domain"

// Database titles the template must contain (spec §2.11).
const (
	LibraryTitle = domain.LibraryName
	DailyTitle   = domain.DailyLogName
)

// articlesProp is the Daily Log relation that must target the Library's data source.
const articlesProp = "Articles"

// PropSpec is one server-owned property: logical key, column name and Notion type.
type PropSpec struct {
	Key  string
	Name string
	Type string
}

// LibraryProps are the server-owned Reading Library properties (spec §3.3).
// User-owned columns (Tags, Status, Rating, Notes) are created by setup but never
// validated or written (C1).
var LibraryProps = []PropSpec{
	{"title", "Name", "title"},
	{"url", "URL", "url"},
	{"source", "Source", "select"},
	{"author", "Author", "rich_text"},
	{"site", "Site", "select"},
	{"word_count", "Word count", "number"},
	{"first_read", "First read", "date"},
	{"last_read", "Last read", "date"},
	{"minutes", "Reading time (min)", "number"},
	{"progress", "Progress", "number"},
	{"sessions", "Sessions", "number"},
	{"completed", "Completed", "checkbox"},
}

// DailyProps are the Daily Log properties (all server-owned).
var DailyProps = []PropSpec{
	{"day_title", "Name", "title"},
	{"day_date", "Date", "date"},
	{"day_minutes", "Minutes", "number"},
	{"day_articles", articlesProp, "relation"},
}

func libraryCreateSchema() map[string]any {
	empty := map[string]any{}
	number := func(format string) map[string]any { return map[string]any{"number": map[string]any{"format": format}} }
	status := func(name, group string) map[string]any { return map[string]any{"name": name, "group": group} }
	var stars []any
	for _, s := range []string{"⭐", "⭐⭐", "⭐⭐⭐", "⭐⭐⭐⭐", "⭐⭐⭐⭐⭐"} {
		stars = append(stars, map[string]any{"name": s})
	}
	return map[string]any{
		"Name":               map[string]any{"title": empty},
		"URL":                map[string]any{"url": empty},
		"Source":             map[string]any{"select": empty},
		"Author":             map[string]any{"rich_text": empty},
		"Site":               map[string]any{"select": empty},
		"Word count":         number("number"),
		"First read":         map[string]any{"date": empty},
		"Last read":          map[string]any{"date": empty},
		"Reading time (min)": number("number"),
		"Progress":           number("percent"),
		"Sessions":           number("number"),
		"Completed":          map[string]any{"checkbox": empty},
		"Tags":               map[string]any{"multi_select": empty},
		"Status": map[string]any{"status": map[string]any{"options": []any{
			status("Not started", "To-do"), status("Read later", "To-do"), status("Reading", "In progress"),
			status("Done", "Complete"), status("Abandoned", "Complete"),
		}}},
		"Rating": map[string]any{"select": map[string]any{"options": stars}},
		"Notes":  map[string]any{"rich_text": empty},
	}
}

func dailyCreateSchema(libraryDSID string) map[string]any {
	empty := map[string]any{}
	return map[string]any{
		"Name":    map[string]any{"title": empty},
		"Date":    map[string]any{"date": empty},
		"Minutes": map[string]any{"number": map[string]any{"format": "number"}},
		articlesProp: map[string]any{"relation": map[string]any{
			"data_source_id": libraryDSID, "type": "single_property", "single_property": empty,
		}},
	}
}
