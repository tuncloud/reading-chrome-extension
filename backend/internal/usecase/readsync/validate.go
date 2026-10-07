package readsync

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/textutil"
	"reading-tracker/backend/internal/usecase/apperr"
)

// Segment rejection reasons.
const (
	ReasonInvalidID           = "invalid_id"
	ReasonInvalidSession      = "invalid_session_id"
	ReasonInvalidSeq          = "invalid_seq"
	ReasonInvalidTimestamp    = "invalid_timestamp"
	ReasonActiveSecondsRange  = "active_seconds_out_of_range"
	ReasonEndedBeforeStarted  = "ended_before_started"
	ReasonActiveExceedsWindow = "active_seconds_exceed_window"
	ReasonProgressRange       = "max_progress_out_of_range"
	ReasonStartedAtRange      = "started_at_out_of_range"
	ReasonUnknownArticle      = "unknown_article"
	ReasonInvalidArticle      = "invalid_article"
)

const (
	MaxArticles      = 50
	MaxSegments      = 50
	maxURLLen        = 2048
	maxTitleRunes    = 500
	maxNameRunes     = 200
	maxWordCount     = 200000
	maxSessionIDLen  = 64
	maxActiveSeconds = 3600
	windowSlack      = 5 * time.Second
	maxSegmentAge    = 30 * 24 * time.Hour
	maxClockSkew     = 5 * time.Minute
)

var (
	uuidRE   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	sourceRE = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

func checkEnvelope(req *PushRequest) (*time.Location, error) {
	if len(req.Articles) > MaxArticles || len(req.Segments) > MaxSegments {
		return nil, apperr.NewTooLarge(fmt.Sprintf("at most %d articles and %d segments per push", MaxArticles, MaxSegments))
	}
	if req.TZ == "" || req.TZ == "Local" {
		return nil, apperr.NewInvalid("tz must be an IANA time zone name")
	}
	loc, err := time.LoadLocation(req.TZ)
	if err != nil {
		return nil, apperr.NewInvalid("tz is not a known IANA time zone")
	}
	return loc, nil
}

// normalizeArticle trims and bounds an article. Identity fields (url, source,
// word_count) must be valid; descriptive text is truncated (interpretation 2).
func normalizeArticle(a Article) (domain.ArticleMeta, bool) {
	u := strings.TrimSpace(a.CanonicalURL)
	src := strings.TrimSpace(a.Source)
	if !validURL(u) || !sourceRE.MatchString(src) || a.WordCount < 0 || a.WordCount > maxWordCount {
		return domain.ArticleMeta{}, false
	}
	return domain.ArticleMeta{
		CanonicalURL: u,
		Source:       src,
		Title:        textutil.Truncate(strings.TrimSpace(a.Title), maxTitleRunes),
		Author:       textutil.Truncate(strings.TrimSpace(a.Author), maxNameRunes),
		SiteName:     textutil.Truncate(strings.TrimSpace(a.SiteName), maxNameRunes),
		WordCount:    a.WordCount,
		PublishedAt:  normalizeDate(a.PublishedAt),
	}, true
}

func validURL(s string) bool {
	if s == "" || len(s) > maxURLLen {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// normalizeDate keeps YYYY-MM-DD, reduces RFC 3339 to its UTC date, and drops anything else.
func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if _, err := time.Parse(time.DateOnly, s); err == nil {
		return s
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.DateOnly)
	}
	return ""
}

// validateSegment returns the parsed times or a rejection reason (spec §2.8 validation).
func validateSegment(s Segment, now time.Time) (start, end time.Time, reason string) {
	if !uuidRE.MatchString(s.ID) {
		return start, end, ReasonInvalidID
	}
	if s.SessionID == "" || len(s.SessionID) > maxSessionIDLen {
		return start, end, ReasonInvalidSession
	}
	if s.Seq < 0 {
		return start, end, ReasonInvalidSeq
	}
	start, err1 := time.Parse(time.RFC3339, s.StartedAt)
	end, err2 := time.Parse(time.RFC3339, s.EndedAt)
	if err1 != nil || err2 != nil {
		return start, end, ReasonInvalidTimestamp
	}
	if s.ActiveSeconds < 1 || s.ActiveSeconds > maxActiveSeconds {
		return start, end, ReasonActiveSecondsRange
	}
	if end.Before(start) {
		return start, end, ReasonEndedBeforeStarted
	}
	if time.Duration(s.ActiveSeconds)*time.Second > end.Sub(start)+windowSlack {
		return start, end, ReasonActiveExceedsWindow
	}
	if s.MaxProgress < 0 || s.MaxProgress > 1 {
		return start, end, ReasonProgressRange
	}
	if start.Before(now.Add(-maxSegmentAge)) || start.After(now.Add(maxClockSkew)) {
		return start, end, ReasonStartedAtRange
	}
	return start, end, ""
}
