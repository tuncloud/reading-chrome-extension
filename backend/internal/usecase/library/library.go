// Package library answers "what do we know about these articles" (spec §2.9).
package library

import (
	"context"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

const maxStatusURLs = 100

type Service struct {
	Reading port.Reading
}

// ArticleStatus is one known article; NotionURL is "" until a page exists.
type ArticleStatus struct {
	TotalSeconds int64
	MaxProgress  float64
	Sessions     int64
	Completed    bool
	LastReadAt   string
	NotionURL    string
}

// Status returns the known articles keyed by the caller's URL strings. Unknown URLs
// are omitted.
func (s *Service) Status(ctx context.Context, instID string, urls []string) (map[string]ArticleStatus, error) {
	if len(urls) > maxStatusURLs {
		return nil, apperr.NewTooLarge("canonical_urls exceeds 100")
	}
	if instID == "" {
		return nil, apperr.NewNoInstallation()
	}
	urlByHash := map[string]string{}
	hashes := make([]string, 0, len(urls))
	for _, u := range urls {
		h := domain.URLHash(u)
		if _, dup := urlByHash[h]; !dup {
			urlByHash[h] = u
			hashes = append(hashes, h)
		}
	}
	arts, err := s.Reading.BatchGetArticles(ctx, instID, hashes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ArticleStatus, len(arts))
	for hash, a := range arts {
		out[urlByHash[hash]] = ArticleStatus{TotalSeconds: a.TotalSeconds, MaxProgress: a.MaxProgress, Sessions: a.Sessions,
			Completed: a.Completed, LastReadAt: a.LastReadAt, NotionURL: a.NotionURL}
	}
	return out, nil
}
