// Package library answers "what do we know about these articles" (spec §2.9).
package library

import (
	"context"
	"errors"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

const (
	maxStatusURLs       = 100
	defaultHistoryLimit = 50
	maxHistoryLimit     = 100
)

type Service struct {
	Reading port.Reading
	Now     func() time.Time
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

// Status returns the user's known articles keyed by the caller's URL strings. Unknown
// URLs are omitted. An empty installationID means the user has none (apperr.NoInstallation).
func (s *Service) Status(ctx context.Context, userID, installationID string, urls []string) (map[string]ArticleStatus, error) {
	if len(urls) > maxStatusURLs {
		return nil, apperr.NewTooLarge("canonical_urls exceeds 100")
	}
	if installationID == "" {
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
	arts, err := s.Reading.BatchGetArticles(ctx, userID, hashes)
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

// History is the user's most recently read articles and today's total.
type History struct {
	Articles     []domain.Article
	TodayDate    string // YYYY-MM-DD in the caller's time zone
	TodaySeconds int64
}

// History returns the user's most recently read articles and today's total (design §History endpoint).
// limit 0 means the default of 50; above 100 it is 100. It needs no installation.
func (s *Service) History(ctx context.Context, userID, tz string, limit int) (*History, error) {
	if tz == "" || tz == "Local" {
		return nil, apperr.NewInvalid("tz must be an IANA time zone name")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, apperr.NewInvalid("tz is not a known IANA time zone")
	}
	switch {
	case limit <= 0:
		limit = defaultHistoryLimit
	case limit > maxHistoryLimit:
		limit = maxHistoryLimit
	}
	arts, err := s.Reading.RecentArticles(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	date := s.Now().In(loc).Format(time.DateOnly)
	h := &History{Articles: arts, TodayDate: date}
	day, err := s.Reading.GetDay(ctx, userID, date)
	switch {
	case err == nil:
		h.TodaySeconds = day.Seconds
	case errors.Is(err, domain.ErrNotFound):
	default:
		return nil, err
	}
	return h, nil
}
