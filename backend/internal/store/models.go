package store

import "strings"

// Installation statuses (spec §3.1).
const (
	StatusActive     = "active"
	StatusNeedsSetup = "needs_setup"
	StatusRevoked    = "revoked"
)

type Profile struct {
	PK                   string `dynamodbav:"pk"`
	SK                   string `dynamodbav:"sk"`
	NotionUserID         string `dynamodbav:"notion_user_id"`
	Name                 string `dynamodbav:"name"`
	Email                string `dynamodbav:"email,omitempty"`
	ActiveInstallationID string `dynamodbav:"active_installation_id,omitempty"`
	CreatedAt            string `dynamodbav:"created_at"`
	DeletedAt            string `dynamodbav:"deleted_at,omitempty"`
}

func (p Profile) UserID() string { return strings.TrimPrefix(p.PK, "USER#") }

type Device struct {
	PK         string `dynamodbav:"pk"`
	SK         string `dynamodbav:"sk"`
	Name       string `dynamodbav:"name"`
	TokenHMAC  string `dynamodbav:"token_hmac"`
	CreatedAt  string `dynamodbav:"created_at"`
	LastSeenAt string `dynamodbav:"last_seen_at"`
	RevokedAt  string `dynamodbav:"revoked_at,omitempty"`
}

func (d Device) ID() string { return strings.TrimPrefix(d.SK, "DEVICE#") }

type DeviceToken struct {
	PK       string `dynamodbav:"pk"`
	SK       string `dynamodbav:"sk"`
	UserID   string `dynamodbav:"user_id"`
	DeviceID string `dynamodbav:"device_id"`
}

type OAuthState struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	ExtRedirect   string `dynamodbav:"ext_redirect"`
	CodeChallenge string `dynamodbav:"code_challenge"`
	TTL           int64  `dynamodbav:"ttl"`
}

type AuthCode struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	UserID         string `dynamodbav:"user_id"`
	InstallationID string `dynamodbav:"installation_id"`
	CodeChallenge  string `dynamodbav:"code_challenge"`
	ExtRedirect    string `dynamodbav:"ext_redirect"`
	TTL            int64  `dynamodbav:"ttl"`
}

// DBConfig is the resolved Notion target of an installation (spec §2.11).
type DBConfig struct {
	LibraryDBID string
	LibraryDSID string
	LibraryURL  string
	DailyDBID   string
	DailyDSID   string
	Props       map[string]string // logical key → Notion property id (C9)
}

type Installation struct {
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

func (i Installation) ID() string { return strings.TrimPrefix(i.PK, "INST#") }

// HasDatabases reports whether both data sources and their property ids are known.
func (i Installation) HasDatabases() bool {
	return i.LibraryDSID != "" && i.DailyDSID != "" && len(i.Props) > 0
}

func (i *Installation) ApplyDatabases(db DBConfig) {
	i.LibraryDBID, i.LibraryDSID, i.LibraryURL = db.LibraryDBID, db.LibraryDSID, db.LibraryURL
	i.DailyDBID, i.DailyDSID, i.Props = db.DailyDBID, db.DailyDSID, db.Props
}

type Article struct {
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
	NotionSyncedVersion int64   `dynamodbav:"notion_synced_version,omitempty"`
	NotionSyncedAt      string  `dynamodbav:"notion_synced_at,omitempty"`
}

func (a Article) Hash() string { return strings.TrimPrefix(a.SK, "ART#") }

type Day struct {
	PK                  string   `dynamodbav:"pk"`
	SK                  string   `dynamodbav:"sk"`
	Date                string   `dynamodbav:"date"`
	Seconds             int64    `dynamodbav:"seconds"`
	ArticleHashes       []string `dynamodbav:"article_hashes,stringset"`
	Version             int64    `dynamodbav:"version"`
	NotionPageID        string   `dynamodbav:"notion_page_id,omitempty"`
	NotionSyncedVersion int64    `dynamodbav:"notion_synced_version,omitempty"`
}
