package dynamo

import (
	"strings"

	"reading-tracker/backend/internal/domain"
)

// Records are the stored item shapes (spec §3.1). They carry the dynamodbav tags so
// the domain does not have to; attribute names must never change (stored data).

type userRecord struct {
	PK                   string `dynamodbav:"pk"`
	SK                   string `dynamodbav:"sk"`
	NotionUserID         string `dynamodbav:"notion_user_id"`
	Name                 string `dynamodbav:"name"`
	Email                string `dynamodbav:"email,omitempty"`
	ActiveInstallationID string `dynamodbav:"active_installation_id,omitempty"`
	CreatedAt            string `dynamodbav:"created_at"`
	DeletedAt            string `dynamodbav:"deleted_at,omitempty"`
}

func newUserRecord(uid string, u domain.User) userRecord {
	return userRecord{PK: UserPK(uid), SK: SKProfile, NotionUserID: u.NotionUserID, Name: u.Name, Email: u.Email,
		ActiveInstallationID: u.ActiveInstallationID, CreatedAt: u.CreatedAt, DeletedAt: u.DeletedAt}
}

func (r userRecord) toDomain() *domain.User {
	return &domain.User{ID: strings.TrimPrefix(r.PK, "USER#"), NotionUserID: r.NotionUserID, Name: r.Name, Email: r.Email,
		ActiveInstallationID: r.ActiveInstallationID, CreatedAt: r.CreatedAt, DeletedAt: r.DeletedAt}
}

type deviceRecord struct {
	PK         string `dynamodbav:"pk"`
	SK         string `dynamodbav:"sk"`
	Name       string `dynamodbav:"name"`
	TokenHMAC  string `dynamodbav:"token_hmac"`
	CreatedAt  string `dynamodbav:"created_at"`
	LastSeenAt string `dynamodbav:"last_seen_at"`
	RevokedAt  string `dynamodbav:"revoked_at,omitempty"`
}

func newDeviceRecord(uid, deviceID string, d domain.Device) deviceRecord {
	return deviceRecord{PK: UserPK(uid), SK: DeviceSK(deviceID), Name: d.Name, TokenHMAC: d.TokenHMAC,
		CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, RevokedAt: d.RevokedAt}
}

func (r deviceRecord) toDomain() domain.Device {
	return domain.Device{ID: strings.TrimPrefix(r.SK, "DEVICE#"), UserID: strings.TrimPrefix(r.PK, "USER#"), Name: r.Name,
		TokenHMAC: r.TokenHMAC, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, RevokedAt: r.RevokedAt}
}

type deviceTokenRecord struct {
	PK       string `dynamodbav:"pk"`
	SK       string `dynamodbav:"sk"`
	UserID   string `dynamodbav:"user_id"`
	DeviceID string `dynamodbav:"device_id"`
}

type oauthStateRecord struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	ExtRedirect   string `dynamodbav:"ext_redirect"`
	CodeChallenge string `dynamodbav:"code_challenge"`
	TTL           int64  `dynamodbav:"ttl"`
}

type authCodeRecord struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	UserID         string `dynamodbav:"user_id"`
	InstallationID string `dynamodbav:"installation_id"`
	CodeChallenge  string `dynamodbav:"code_challenge"`
	ExtRedirect    string `dynamodbav:"ext_redirect"`
	TTL            int64  `dynamodbav:"ttl"`
}

type installationRecord struct {
	PK                   string            `dynamodbav:"pk"`
	SK                   string            `dynamodbav:"sk"`
	UserID               string            `dynamodbav:"user_id"`
	WorkspaceID          string            `dynamodbav:"workspace_id"`
	WorkspaceName        string            `dynamodbav:"workspace_name"`
	WorkspaceIcon        string            `dynamodbav:"workspace_icon,omitempty"`
	Status               string            `dynamodbav:"status"`
	SetupError           string            `dynamodbav:"setup_error,omitempty"`
	AccessTokenEnc       string            `dynamodbav:"access_token_enc"`
	RefreshTokenEnc      string            `dynamodbav:"refresh_token_enc"`
	TokenExpiresAt       string            `dynamodbav:"token_expires_at,omitempty"`
	TokenVersion         int               `dynamodbav:"token_version"`
	DuplicatedTemplateID string            `dynamodbav:"duplicated_template_id,omitempty"`
	LibraryDBID          string            `dynamodbav:"library_db_id,omitempty"`
	LibraryDSID          string            `dynamodbav:"library_ds_id,omitempty"`
	LibraryURL           string            `dynamodbav:"library_url,omitempty"`
	DailyDBID            string            `dynamodbav:"daily_db_id,omitempty"`
	DailyDSID            string            `dynamodbav:"daily_ds_id,omitempty"`
	Props                map[string]string `dynamodbav:"props,omitempty"`
	CreatedAt            string            `dynamodbav:"created_at"`
	UpdatedAt            string            `dynamodbav:"updated_at"`
}

func newInstallationRecord(botID string, i *domain.Installation) installationRecord {
	return installationRecord{PK: InstPK(botID), SK: SKMeta, UserID: i.UserID, WorkspaceID: i.WorkspaceID,
		WorkspaceName: i.WorkspaceName, WorkspaceIcon: i.WorkspaceIcon, Status: i.Status, SetupError: i.SetupError,
		AccessTokenEnc: i.AccessTokenEnc, RefreshTokenEnc: i.RefreshTokenEnc, TokenExpiresAt: i.TokenExpiresAt,
		TokenVersion: i.TokenVersion, DuplicatedTemplateID: i.DuplicatedTemplateID, LibraryDBID: i.LibraryDBID,
		LibraryDSID: i.LibraryDSID, LibraryURL: i.LibraryURL, DailyDBID: i.DailyDBID, DailyDSID: i.DailyDSID,
		Props: i.Props, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt}
}

func (r installationRecord) toDomain() *domain.Installation {
	return &domain.Installation{ID: strings.TrimPrefix(r.PK, "INST#"), UserID: r.UserID, WorkspaceID: r.WorkspaceID,
		WorkspaceName: r.WorkspaceName, WorkspaceIcon: r.WorkspaceIcon, Status: r.Status, SetupError: r.SetupError,
		AccessTokenEnc: r.AccessTokenEnc, RefreshTokenEnc: r.RefreshTokenEnc, TokenExpiresAt: r.TokenExpiresAt,
		TokenVersion: r.TokenVersion, DuplicatedTemplateID: r.DuplicatedTemplateID, LibraryDBID: r.LibraryDBID,
		LibraryDSID: r.LibraryDSID, LibraryURL: r.LibraryURL, DailyDBID: r.DailyDBID, DailyDSID: r.DailyDSID,
		Props: r.Props, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

type articleRecord struct {
	PK                  string  `dynamodbav:"pk"`
	SK                  string  `dynamodbav:"sk"`
	CanonicalURL        string  `dynamodbav:"canonical_url"`
	Source              string  `dynamodbav:"source"`
	Title               string  `dynamodbav:"title"`
	Author              string  `dynamodbav:"author"`
	SiteName            string  `dynamodbav:"site_name"`
	WordCount           int     `dynamodbav:"word_count"`
	PublishedAt         string  `dynamodbav:"published_at"`
	FirstReadAt         string  `dynamodbav:"first_read_at"`
	LastReadAt          string  `dynamodbav:"last_read_at"`
	TotalSeconds        int64   `dynamodbav:"total_seconds"`
	Sessions            int64   `dynamodbav:"sessions"`
	MaxProgress         float64 `dynamodbav:"max_progress"`
	Completed           bool    `dynamodbav:"completed"`
	Version             int64   `dynamodbav:"version"`
	NotionPageID        string  `dynamodbav:"notion_page_id,omitempty"`
	NotionURL           string  `dynamodbav:"notion_url,omitempty"`
	NotionDataSourceID  string  `dynamodbav:"notion_ds_id,omitempty"`
	NotionSyncedVersion int64   `dynamodbav:"notion_synced_version,omitempty"`
	NotionSyncedAt      string  `dynamodbav:"notion_synced_at,omitempty"`
}

func (r articleRecord) toDomain() *domain.Article {
	return &domain.Article{Hash: strings.TrimPrefix(r.SK, "ART#"), CanonicalURL: r.CanonicalURL, Source: r.Source,
		Title: r.Title, Author: r.Author, SiteName: r.SiteName, WordCount: r.WordCount, PublishedAt: r.PublishedAt,
		FirstReadAt: r.FirstReadAt, LastReadAt: r.LastReadAt, TotalSeconds: r.TotalSeconds, Sessions: r.Sessions,
		MaxProgress: r.MaxProgress, Completed: r.Completed, Version: r.Version, NotionPageID: r.NotionPageID,
		NotionURL: r.NotionURL, NotionDataSourceID: r.NotionDataSourceID, NotionSyncedVersion: r.NotionSyncedVersion,
		NotionSyncedAt: r.NotionSyncedAt}
}

type dayRecord struct {
	PK                  string   `dynamodbav:"pk"`
	SK                  string   `dynamodbav:"sk"`
	Date                string   `dynamodbav:"date"`
	Seconds             int64    `dynamodbav:"seconds"`
	ArticleHashes       []string `dynamodbav:"article_hashes,stringset"`
	Version             int64    `dynamodbav:"version"`
	NotionPageID        string   `dynamodbav:"notion_page_id,omitempty"`
	NotionDataSourceID  string   `dynamodbav:"notion_ds_id,omitempty"`
	NotionSyncedVersion int64    `dynamodbav:"notion_synced_version,omitempty"`
}

func (r dayRecord) toDomain() *domain.Day {
	return &domain.Day{Date: r.Date, Seconds: r.Seconds, ArticleHashes: r.ArticleHashes, Version: r.Version,
		NotionPageID: r.NotionPageID, NotionDataSourceID: r.NotionDataSourceID, NotionSyncedVersion: r.NotionSyncedVersion}
}
