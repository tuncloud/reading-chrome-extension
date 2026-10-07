// Package syncsvc validates POST /v1/sync/push bodies and applies them (spec §2.8).
package syncsvc

type Article struct {
	CanonicalURL string `json:"canonical_url"`
	Source       string `json:"source"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	SiteName     string `json:"site_name"`
	WordCount    int    `json:"word_count"`
	PublishedAt  string `json:"published_at"`
	OriginalURL  string `json:"original_url"`
}

type Segment struct {
	ID            string  `json:"id"`
	SessionID     string  `json:"session_id"`
	Seq           int     `json:"seq"`
	CanonicalURL  string  `json:"canonical_url"`
	StartedAt     string  `json:"started_at"`
	EndedAt       string  `json:"ended_at"`
	ActiveSeconds int     `json:"active_seconds"`
	MaxProgress   float64 `json:"max_progress"`
}

type PushRequest struct {
	ClientTime string    `json:"client_time"`
	TZ         string    `json:"tz"`
	Articles   []Article `json:"articles"`
	Segments   []Segment `json:"segments"`
}

type Rejection struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// PushResponse lists segment ids by outcome. The client marks accepted ∪ duplicate
// as synced, keeps retry, and drops rejected.
type PushResponse struct {
	Accepted  []string    `json:"accepted"`
	Duplicate []string    `json:"duplicate"`
	Retry     []string    `json:"retry"`
	Rejected  []Rejection `json:"rejected"`
}

func newResponse() *PushResponse {
	return &PushResponse{Accepted: []string{}, Duplicate: []string{}, Retry: []string{}, Rejected: []Rejection{}}
}
