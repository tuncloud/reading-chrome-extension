// Package readsync validates POST /v1/sync/push bodies and applies them (spec §2.8).
package readsync

type Article struct {
	CanonicalURL string
	Source       string
	Title        string
	Author       string
	SiteName     string
	WordCount    int
	PublishedAt  string
	OriginalURL  string
}

type Segment struct {
	ID            string
	SessionID     string
	Seq           int
	CanonicalURL  string
	StartedAt     string
	EndedAt       string
	ActiveSeconds int
	MaxProgress   float64
}

type PushRequest struct {
	ClientTime string
	TZ         string
	Articles   []Article
	Segments   []Segment
}

type Rejection struct {
	ID     string
	Reason string
}

// PushResponse lists segment ids by outcome. The client marks accepted ∪ duplicate
// as synced, keeps retry, and drops rejected.
type PushResponse struct {
	Accepted  []string
	Duplicate []string
	Retry     []string
	Rejected  []Rejection
}

func newResponse() *PushResponse {
	return &PushResponse{Accepted: []string{}, Duplicate: []string{}, Retry: []string{}, Rejected: []Rejection{}}
}
