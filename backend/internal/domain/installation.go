package domain

// Installation statuses (spec §3.1).
const (
	StatusActive     = "active"
	StatusNeedsSetup = "needs_setup"
	StatusRevoked    = "revoked"
)

// SetupErrResyncFailed marks an installation whose backlog could not be enqueued.
const SetupErrResyncFailed = "resync_failed"

// DBConfig is the resolved Notion target of an installation (spec §2.11).
type DBConfig struct {
	LibraryDBID string
	LibraryDSID string
	LibraryURL  string
	DailyDBID   string
	DailyDSID   string
	Props       map[string]string // logical key → Notion property id (C9)
}

// Installation is one Notion authorization (bot) for one workspace (spec §3.1 META).
type Installation struct {
	ID                   string
	UserID               string
	WorkspaceID          string
	WorkspaceName        string
	WorkspaceIcon        string
	Status               string
	SetupError           string
	AccessTokenEnc       string
	RefreshTokenEnc      string
	TokenExpiresAt       string
	TokenVersion         int
	DuplicatedTemplateID string
	LibraryDBID          string
	LibraryDSID          string
	LibraryURL           string
	DailyDBID            string
	DailyDSID            string
	Props                map[string]string
	CreatedAt            string
	UpdatedAt            string
}

// HasDatabases reports whether both data sources and their property ids are known.
func (i Installation) HasDatabases() bool {
	return i.LibraryDSID != "" && i.DailyDSID != "" && len(i.Props) > 0
}

// ApplyDatabases points the installation at db.
func (i *Installation) ApplyDatabases(db DBConfig) {
	i.LibraryDBID, i.LibraryDSID, i.LibraryURL = db.LibraryDBID, db.LibraryDSID, db.LibraryURL
	i.DailyDBID, i.DailyDSID, i.Props = db.DailyDBID, db.DailyDSID, db.Props
}

// Database names the user sees and setup_error reasons use (spec §2.11, §3.3).
const (
	LibraryName  = "Reading Library"
	DailyLogName = "Daily Log"
)

// Databases is the installation's current Notion target.
func (i Installation) Databases() DBConfig {
	return DBConfig{LibraryDBID: i.LibraryDBID, LibraryDSID: i.LibraryDSID, LibraryURL: i.LibraryURL,
		DailyDBID: i.DailyDBID, DailyDSID: i.DailyDSID, Props: i.Props}
}
