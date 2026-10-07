package domain

import "time"

// Article is the per-installation aggregate of one canonical URL (spec §3.1 ART).
type Article struct {
	Hash                string
	CanonicalURL        string
	Source              string
	Title               string
	Author              string
	SiteName            string
	WordCount           int
	PublishedAt         string
	FirstReadAt         string
	LastReadAt          string
	TotalSeconds        int64
	Sessions            int64
	MaxProgress         float64
	Completed           bool
	Version             int64
	NotionPageID        string
	NotionURL           string
	NotionDataSourceID  string // data source the page was written into (R1); "" for pages written before it was recorded
	NotionSyncedVersion int64
	NotionSyncedAt      string
}

// Day is the per-installation aggregate of one local date (spec §3.1 DAY).
type Day struct {
	Date                string
	Seconds             int64
	ArticleHashes       []string
	Version             int64
	NotionPageID        string
	NotionDataSourceID  string
	NotionSyncedVersion int64
}

// ArticleMeta is the descriptive part of an article, written once.
type ArticleMeta struct {
	CanonicalURL string
	Source       string
	Title        string
	Author       string
	SiteName     string
	WordCount    int
	PublishedAt  string
}

// SegmentWrite is one validated segment ready to be applied exactly once (C2).
type SegmentWrite struct {
	InstallationID string
	DeviceID       string
	SegmentID      string
	SessionID      string
	Seq            int
	URLHash        string
	Date           string // YYYY-MM-DD of StartedAt in the request tz
	StartedAt      time.Time
	EndedAt        time.Time
	ActiveSeconds  int
	MaxProgress    float64
	Article        ArticleMeta
}

// PushOutcome is how one segment was applied.
type PushOutcome int

const (
	PushAccepted PushOutcome = iota + 1
	PushDuplicate
	PushRetry
)
