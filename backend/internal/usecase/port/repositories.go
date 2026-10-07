// Package port declares what the use cases need from the outside world. Adapters
// implement these interfaces; use cases never import an adapter.
package port

import (
	"context"
	"time"

	"reading-tracker/backend/internal/domain"
)

// Users is the PROFILE and NUSER store.
type Users interface {
	GetProfile(ctx context.Context, uid string) (*domain.User, error)
	UserIDForNotionUser(ctx context.Context, notionUserID string) (string, error)
	CreateUser(ctx context.Context, uid string, u domain.User) error
	SetActiveInstallation(ctx context.Context, uid, botID string) error
	MarkProfileDeleted(ctx context.Context, uid string, now time.Time) error
	DeleteNotionUserLink(ctx context.Context, notionUserID, uid string) error
}

// Devices is the DEVICE and DEVTOKEN store.
type Devices interface {
	CreateDevice(ctx context.Context, uid, deviceID string, d domain.Device) error
	GetDeviceToken(ctx context.Context, tokenHMAC string) (*domain.DeviceToken, error)
	GetDevice(ctx context.Context, uid, deviceID string) (*domain.Device, error)
	TouchDevice(ctx context.Context, uid, deviceID string, now time.Time) error
	RevokeDevice(ctx context.Context, uid, deviceID, tokenHMAC string, now time.Time) error
	ListDevices(ctx context.Context, uid string) ([]domain.Device, error)
	DeleteDeviceToken(ctx context.Context, tokenHMAC string) error
}

// OAuthGrants is the single-use OAUTHSTATE and AUTHCODE store (C12).
type OAuthGrants interface {
	PutOAuthState(ctx context.Context, state string, st domain.OAuthState, expires time.Time) error
	ConsumeOAuthState(ctx context.Context, state string, now time.Time) (*domain.OAuthState, error)
	PutAuthCode(ctx context.Context, code string, ac domain.AuthCode, expires time.Time) error
	ConsumeAuthCode(ctx context.Context, code string, now time.Time) (*domain.AuthCode, error)
}

// Installations is the INST META store.
type Installations interface {
	GetInstallation(ctx context.Context, botID string) (*domain.Installation, error)
	PutInstallation(ctx context.Context, botID string, inst *domain.Installation) error
	PutUserInstallation(ctx context.Context, uid, botID string, now time.Time) error
	ListUserInstallationIDs(ctx context.Context, uid string) ([]string, error)
	SetInstallationStatus(ctx context.Context, botID, status, setupError string, now time.Time) error
	SaveInstallationDatabases(ctx context.Context, botID string, db domain.DBConfig, now time.Time) error
	UpdateInstallationTokens(ctx context.Context, botID, accessEnc, refreshEnc string, oldVersion int, now time.Time) error
}

// Reading is the SEG, ART and DAY store.
type Reading interface {
	PushSegment(ctx context.Context, w domain.SegmentWrite) (domain.PushOutcome, error)
	GetArticle(ctx context.Context, inst, hash string) (*domain.Article, error)
	BatchGetArticles(ctx context.Context, inst string, hashes []string) (map[string]*domain.Article, error)
	SetArticlePage(ctx context.Context, inst, hash, pageID, url string) error
	ClearArticlePage(ctx context.Context, inst, hash string) error
	MarkArticleSynced(ctx context.Context, inst, hash string, version int64, completed bool, now time.Time) error
	GetDay(ctx context.Context, inst, date string) (*domain.Day, error)
	SetDayPage(ctx context.Context, inst, date, pageID string) error
	ClearDayPage(ctx context.Context, inst, date string) error
	MarkDaySynced(ctx context.Context, inst, date string, version int64) error
	PendingSync(ctx context.Context, inst string, all bool) (hashes, dates []string, err error)
}
