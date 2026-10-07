package port

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reading-tracker/backend/internal/domain"
)

// NotionGrant is the result of an OAuth code exchange or token refresh.
type NotionGrant struct {
	AccessToken          string
	RefreshToken         string
	BotID                string
	WorkspaceID          string
	WorkspaceName        string
	WorkspaceIcon        string
	DuplicatedTemplateID string
	ExpiresIn            int
	OwnerUserID          string
	OwnerName            string
	OwnerEmail           string
}

// PageRef is a page the integration can see (setup page picker).
type PageRef struct {
	ID    string
	Title string
	URL   string
}

// NotionAuth is Notion's OAuth endpoints (spec §2.1–§2.2, §2.16).
type NotionAuth interface {
	AuthorizeURL(redirectURI, state string) string
	ExchangeCode(ctx context.Context, code, redirectURI string) (*NotionGrant, error)
	RefreshToken(ctx context.Context, refreshToken string) (*NotionGrant, error)
}

// NotionWorkspace finds and builds the user's databases (spec §2.6, §2.7, §2.11).
// Resolution failures are *domain.SetupError; everything else is *NotionError.
type NotionWorkspace interface {
	SearchPages(ctx context.Context, token, query string) ([]PageRef, error)
	ResolveTemplate(ctx context.Context, token, templateID string) (domain.DBConfig, error)
	ProvisionDatabases(ctx context.Context, token, parentPageID string) (domain.DBConfig, error)
}

// NotionErrorKind is what a failed Notion call means to the caller (spec §2.15).
type NotionErrorKind int

const (
	NotionTransient         NotionErrorKind = iota // 5xx or network: retry
	NotionRateLimited                              // 429/529: back off RetryAfter
	NotionUnauthorized                             // 401: refresh the token (worker) or reconnect (API)
	NotionInvalidGrant                             // refresh token rejected: revoked
	NotionConflict                                 // 409 conflict_error: retry
	NotionNotFound                                 // 404 on an object that isn't a page or data source
	NotionPageGone                                 // page 404, archived, trashed or moved to another data source
	NotionDataSourceMissing                        // data source 404: the database was deleted
	NotionRejected                                 // any other 4xx: poison
)

func (k NotionErrorKind) String() string {
	names := [...]string{"transient", "rate_limited", "unauthorized", "invalid_grant", "conflict", "not_found",
		"page_gone", "data_source_missing", "rejected"}
	if k < 0 || int(k) >= len(names) {
		return fmt.Sprintf("NotionErrorKind(%d)", int(k))
	}
	return names[k]
}

// NotionError is a classified Notion failure.
type NotionError struct {
	Kind       NotionErrorKind
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Err        error
}

func (e *NotionError) Error() string {
	return fmt.Sprintf("notion %s: %d %s: %s", e.Kind, e.Status, e.Code, e.Message)
}

func (e *NotionError) Unwrap() error { return e.Err }

// NotionErrorOf unwraps err into *NotionError.
func NotionErrorOf(err error) (*NotionError, bool) {
	var e *NotionError
	ok := errors.As(err, &e)
	return e, ok
}

// IsNotionKind reports whether err is a NotionError of kind k.
func IsNotionKind(err error, k NotionErrorKind) bool {
	e, ok := NotionErrorOf(err)
	return ok && e.Kind == k
}

// Page is a Notion page the sync wrote or adopted.
type Page struct {
	ID  string
	URL string
}

// NotionPages writes the Reading Library and Daily Log rows (spec §2.13, §2.14).
// It writes only server-owned properties, by stored property id (C1, C9). Find and
// Create return NotionDataSourceMissing on a 404. Update returns NotionPageGone when
// the page is missing, archived, trashed, or now lives in another data source.
type NotionPages interface {
	FindArticlePage(ctx context.Context, token string, db domain.DBConfig, canonicalURL string) (*Page, error)
	CreateArticlePage(ctx context.Context, token string, db domain.DBConfig, a domain.Article, completed bool) (*Page, error)
	UpdateArticlePage(ctx context.Context, token string, db domain.DBConfig, pageID string, a domain.Article, completed bool) error
	FindDayPage(ctx context.Context, token string, db domain.DBConfig, date string) (*Page, error)
	CreateDayPage(ctx context.Context, token string, db domain.DBConfig, d domain.Day, articlePageIDs []string) (*Page, error)
	UpdateDayPage(ctx context.Context, token string, db domain.DBConfig, pageID string, d domain.Day, articlePageIDs []string) error
}
