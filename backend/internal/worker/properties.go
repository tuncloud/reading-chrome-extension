package worker

import (
	"math"
	"strings"

	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/textutil"
)

// Notion request limits (appendix §8 + request-limits reference).
const (
	notionMaxURL    = 2000
	notionMaxText   = 2000
	notionMaxSelect = 100
	maxRelations    = 100
)

// isCompleted implements spec §2.13 step 2; once true it stays true.
func isCompleted(a *store.Article, t config.Tracking) bool {
	if a.Completed {
		return true
	}
	if a.MaxProgress < t.CompleteProgress {
		return false
	}
	if a.WordCount == 0 {
		return true
	}
	est := float64(a.WordCount) / float64(t.WordsPerMinute) * 60
	return float64(a.TotalSeconds) >= t.CompleteTimeRatio*est
}

// articleMetrics are the server-owned properties written on every update (§3.3).
func articleMetrics(p map[string]string, a *store.Article, completed bool) map[string]any {
	return map[string]any{
		p["last_read"]: dateValue(a.LastReadAt),
		p["minutes"]:   map[string]any{"number": roundMinutes(a.TotalSeconds)},
		p["progress"]:  map[string]any{"number": a.MaxProgress},
		p["sessions"]:  map[string]any{"number": a.Sessions},
		p["completed"]: map[string]any{"checkbox": completed},
	}
}

// articleCreateBody adds the write-once descriptive properties to the metrics.
// It never includes Tags, Status, Rating or Notes (C1).
func articleCreateBody(dsID string, p map[string]string, a *store.Article, completed bool) map[string]any {
	props := articleMetrics(p, a, completed)
	title := a.Title
	if title == "" {
		title = a.CanonicalURL
	}
	props[p["title"]] = map[string]any{"title": richText(title)}
	props[p["first_read"]] = dateValue(a.FirstReadAt)
	if a.WordCount > 0 {
		props[p["word_count"]] = map[string]any{"number": a.WordCount}
	}
	if a.Author != "" {
		props[p["author"]] = map[string]any{"rich_text": richText(a.Author)}
	}
	if s := selectName(a.Source); s != "" {
		props[p["source"]] = map[string]any{"select": map[string]any{"name": s}}
	}
	if s := selectName(a.SiteName); s != "" {
		props[p["site"]] = map[string]any{"select": map[string]any{"name": s}}
	}
	body := map[string]any{
		"parent":     map[string]any{"type": "data_source_id", "data_source_id": dsID},
		"properties": props,
	}
	if len(a.CanonicalURL) <= notionMaxURL {
		props[p["url"]] = map[string]any{"url": a.CanonicalURL}
		body["children"] = []any{map[string]any{"object": "block", "type": "bookmark", "bookmark": map[string]any{"url": a.CanonicalURL}}}
	}
	return body
}

func richText(s string) []any {
	return []any{map[string]any{"type": "text", "text": map[string]any{"content": textutil.Truncate(s, notionMaxText)}}}
}

func dateValue(ts string) map[string]any {
	if ts == "" {
		return map[string]any{"date": nil}
	}
	return map[string]any{"date": map[string]any{"start": ts}}
}

func roundMinutes(secs int64) int64 { return int64(math.Round(float64(secs) / 60)) }

// selectName makes s a valid select option: Notion forbids commas and caps length.
func selectName(s string) string {
	return textutil.Truncate(strings.Join(strings.Fields(strings.ReplaceAll(s, ",", " ")), " "), notionMaxSelect)
}
