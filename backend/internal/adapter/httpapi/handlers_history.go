package httpapi

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

type historyArticle struct {
	CanonicalURL string  `json:"canonical_url"`
	Source       string  `json:"source"`
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	SiteName     string  `json:"site_name"`
	WordCount    int     `json:"word_count"`
	PublishedAt  string  `json:"published_at"`
	TotalSeconds int64   `json:"total_seconds"`
	MaxProgress  float64 `json:"max_progress"`
	LastReadAt   string  `json:"last_read_at"`
	Completed    bool    `json:"completed"`
	NotionURL    *string `json:"notion_url"`
}

type historyToday struct {
	Date    string `json:"date"`
	Seconds int64  `json:"seconds"`
}

type historyResponse struct {
	Articles []historyArticle `json:"articles"`
	Today    historyToday     `json:"today"`
}

// getHistory serves GET /v1/history?tz=&limit= (design §History endpoint). It needs only a device.
func (h *handlers) getHistory(c echo.Context) error {
	limit := 0
	if raw, present := c.QueryParams()["limit"]; present {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 {
			return errInvalid("limit must be an integer of at least 1")
		}
		limit = n
	}
	got, err := h.Library.History(c.Request().Context(), principal(c).UserID, c.QueryParam("tz"), limit)
	if err != nil {
		return err
	}
	out := historyResponse{Articles: make([]historyArticle, 0, len(got.Articles)),
		Today: historyToday{Date: got.TodayDate, Seconds: got.TodaySeconds}}
	for _, a := range got.Articles {
		item := historyArticle{CanonicalURL: a.CanonicalURL, Source: a.Source, Title: a.Title, Author: a.Author,
			SiteName: a.SiteName, WordCount: a.WordCount, PublishedAt: a.PublishedAt, TotalSeconds: a.TotalSeconds,
			MaxProgress: a.MaxProgress, LastReadAt: a.LastReadAt, Completed: a.Completed}
		if a.NotionURL != "" {
			n := a.NotionURL
			item.NotionURL = &n
		}
		out.Articles = append(out.Articles, item)
	}
	return c.JSON(http.StatusOK, out)
}
