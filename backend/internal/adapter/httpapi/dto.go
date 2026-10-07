package httpapi

import "reading-tracker/backend/internal/usecase/readsync"

// The JSON shapes of spec §2.8; the use case works on tag-free types.

type articleDTO struct {
	CanonicalURL string `json:"canonical_url"`
	Source       string `json:"source"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	SiteName     string `json:"site_name"`
	WordCount    int    `json:"word_count"`
	PublishedAt  string `json:"published_at"`
	OriginalURL  string `json:"original_url"`
}

type segmentDTO struct {
	ID            string  `json:"id"`
	SessionID     string  `json:"session_id"`
	Seq           int     `json:"seq"`
	CanonicalURL  string  `json:"canonical_url"`
	StartedAt     string  `json:"started_at"`
	EndedAt       string  `json:"ended_at"`
	ActiveSeconds int     `json:"active_seconds"`
	MaxProgress   float64 `json:"max_progress"`
}

type pushRequestDTO struct {
	ClientTime string       `json:"client_time"`
	TZ         string       `json:"tz"`
	Articles   []articleDTO `json:"articles"`
	Segments   []segmentDTO `json:"segments"`
}

func (d pushRequestDTO) toInput() readsync.PushRequest {
	req := readsync.PushRequest{ClientTime: d.ClientTime, TZ: d.TZ,
		Articles: make([]readsync.Article, len(d.Articles)), Segments: make([]readsync.Segment, len(d.Segments))}
	for i, a := range d.Articles {
		req.Articles[i] = readsync.Article(a)
	}
	for i, s := range d.Segments {
		req.Segments[i] = readsync.Segment(s)
	}
	return req
}

type rejectionDTO struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type pushResponseDTO struct {
	Accepted  []string       `json:"accepted"`
	Duplicate []string       `json:"duplicate"`
	Retry     []string       `json:"retry"`
	Rejected  []rejectionDTO `json:"rejected"`
}

func pushResponse(r *readsync.PushResponse) pushResponseDTO {
	out := pushResponseDTO{Accepted: r.Accepted, Duplicate: r.Duplicate, Retry: r.Retry, Rejected: make([]rejectionDTO, len(r.Rejected))}
	for i, x := range r.Rejected {
		out.Rejected[i] = rejectionDTO(x)
	}
	return out
}
