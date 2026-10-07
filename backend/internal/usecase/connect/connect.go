// Package connect runs the Notion OAuth flow: start, callback, user and installation
// upsert (spec §2.1, §2.2).
package connect

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
	"reading-tracker/backend/internal/usecase/setup"
)

const (
	oauthStateTTL = 10 * time.Minute
	authCodeTTL   = 2 * time.Minute
)

// ErrLinkExpired: the callback's state is missing, expired or already used.
var ErrLinkExpired = errors.New("connect: link expired")

type Service struct {
	Users               port.Users
	Grants              port.OAuthGrants
	Installations       port.Installations
	Auth                port.NotionAuth
	Workspace           port.NotionWorkspace
	Sealer              port.Sealer
	Activator           *setup.Activator
	AllowedExtensionIDs map[string]bool
	Now                 func() time.Time
	Log                 *slog.Logger
}

// Redirect sends the browser back to the extension with ?<Param>=<Value>.
type Redirect struct {
	URL   string
	Param string
	Value string
}

// Start validates the extension redirect and PKCE challenge, stores a single-use
// state, and returns Notion's authorize URL (spec §2.1).
func (s *Service) Start(ctx context.Context, extRedirect, challenge, method, redirectURI string) (string, error) {
	if !s.allowedExtRedirect(extRedirect) {
		return "", apperr.NewInvalid("ext_redirect is not an allowed extension redirect")
	}
	if method != "S256" || !crypto.ValidChallenge(challenge) {
		return "", apperr.NewInvalid("code_challenge must be an S256 challenge")
	}
	state, err := crypto.RandomB64URL(32)
	if err != nil {
		return "", err
	}
	st := domain.OAuthState{ExtRedirect: extRedirect, CodeChallenge: challenge}
	if err := s.Grants.PutOAuthState(ctx, state, st, s.Now().Add(oauthStateTTL)); err != nil {
		return "", err
	}
	return s.Auth.AuthorizeURL(redirectURI, state), nil
}

// allowedExtRedirect accepts only https://<id>.chromiumapp.org/… for configured ids.
func (s *Service) allowedExtRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	id, ok := strings.CutSuffix(strings.ToLower(u.Hostname()), ".chromiumapp.org")
	return ok && s.AllowedExtensionIDs[id]
}

// Complete handles Notion's callback (spec §2.2): consume the state, exchange the
// code, upsert the user and installation, and issue a one-time code for the extension.
func (s *Service) Complete(ctx context.Context, state, code, notionError, redirectURI string) (*Redirect, error) {
	if !domain.PlausibleOneTimeCode(state) {
		return nil, ErrLinkExpired
	}
	st, err := s.Grants.ConsumeOAuthState(ctx, state, s.Now())
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrLinkExpired
	}
	if err != nil {
		return nil, err
	}
	if notionError != "" {
		return &Redirect{URL: st.ExtRedirect, Param: "error", Value: notionError}, nil
	}
	grant, err := s.Auth.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		s.Log.WarnContext(ctx, "oauth: code exchange failed", "err", err)
		return &Redirect{URL: st.ExtRedirect, Param: "error", Value: "exchange_failed"}, nil
	}
	if grant.BotID == "" || grant.OwnerUserID == "" {
		return &Redirect{URL: st.ExtRedirect, Param: "error", Value: "unsupported_owner"}, nil
	}
	uid, err := s.resolveUser(ctx, grant)
	if err != nil {
		return nil, err
	}
	inst, err := s.upsertInstallation(ctx, uid, grant)
	if err != nil {
		return nil, err
	}
	authCode, err := crypto.RandomB64URL(32)
	if err != nil {
		return nil, err
	}
	ac := domain.AuthCode{UserID: uid, InstallationID: inst.ID, CodeChallenge: st.CodeChallenge, ExtRedirect: st.ExtRedirect}
	if err := s.Grants.PutAuthCode(ctx, authCode, ac, s.Now().Add(authCodeTTL)); err != nil {
		return nil, err
	}
	return &Redirect{URL: st.ExtRedirect, Param: "code", Value: authCode}, nil
}

// resolveUser maps the Notion owner to our user, creating one if needed (§2.2 step 4).
// A user whose deletion is pending is treated as absent.
func (s *Service) resolveUser(ctx context.Context, g *port.NotionGrant) (string, error) {
	uid, err := s.Users.UserIDForNotionUser(ctx, g.OwnerUserID)
	switch {
	case err == nil:
		user, uerr := s.Users.GetProfile(ctx, uid)
		if uerr == nil && !user.Deleted() {
			return uid, nil
		}
		if uerr != nil && !errors.Is(uerr, domain.ErrNotFound) {
			return "", uerr
		}
	case !errors.Is(err, domain.ErrNotFound):
		return "", err
	}
	uid = ulid.Make().String()
	return uid, s.Users.CreateUser(ctx, uid, domain.User{NotionUserID: g.OwnerUserID, Name: g.OwnerName,
		Email: g.OwnerEmail, CreatedAt: domain.Timestamp(s.Now())})
}

// upsertInstallation stores the installation with sealed tokens (C18) and decides its
// status (§2.2 steps 5–7).
func (s *Service) upsertInstallation(ctx context.Context, uid string, g *port.NotionGrant) (*domain.Installation, error) {
	now := s.Now()
	inst, err := s.Installations.GetInstallation(ctx, g.BotID)
	if errors.Is(err, domain.ErrNotFound) {
		inst, err = &domain.Installation{CreatedAt: domain.Timestamp(now)}, nil
	}
	if err != nil {
		return nil, err
	}
	inst.ID = g.BotID
	prevLibrary, prevDaily, prevSetupError := inst.LibraryDSID, inst.DailyDSID, inst.SetupError
	aad := []byte(g.BotID)
	if inst.AccessTokenEnc, err = s.Sealer.Seal([]byte(g.AccessToken), aad); err != nil {
		return nil, err
	}
	inst.RefreshTokenEnc = ""
	if g.RefreshToken != "" {
		if inst.RefreshTokenEnc, err = s.Sealer.Seal([]byte(g.RefreshToken), aad); err != nil {
			return nil, err
		}
	}
	inst.TokenExpiresAt = ""
	if g.ExpiresIn > 0 {
		inst.TokenExpiresAt = domain.Timestamp(now.Add(time.Duration(g.ExpiresIn) * time.Second))
	}
	inst.TokenVersion++
	inst.UserID, inst.WorkspaceID, inst.WorkspaceName, inst.WorkspaceIcon = uid, g.WorkspaceID, g.WorkspaceName, g.WorkspaceIcon
	inst.UpdatedAt, inst.SetupError = domain.Timestamp(now), ""

	switch {
	case g.DuplicatedTemplateID != "":
		inst.DuplicatedTemplateID = g.DuplicatedTemplateID
		db, err := s.Workspace.ResolveTemplate(ctx, g.AccessToken, g.DuplicatedTemplateID)
		if err != nil {
			inst.Status, inst.SetupError = domain.StatusNeedsSetup, domain.SetupReason(err)
			s.Log.WarnContext(ctx, "oauth: template resolution failed", "reason", inst.SetupError, "err", err)
		} else {
			inst.ApplyDatabases(db)
			inst.Status = domain.StatusActive
		}
	case inst.HasDatabases():
		inst.Status = domain.StatusActive // reconnect to an already set-up workspace
	default:
		inst.Status = domain.StatusNeedsSetup
	}

	if err := s.Installations.PutInstallation(ctx, g.BotID, inst); err != nil {
		return nil, err
	}
	if err := s.Installations.PutUserInstallation(ctx, uid, g.BotID, now); err != nil {
		return nil, err
	}
	if err := s.Users.SetActiveInstallation(ctx, uid, g.BotID); err != nil {
		return nil, err
	}
	if inst.Status == domain.StatusActive {
		// New databases (e.g. a newly duplicated template) → re-send everything (scenario 10).
		// So does recovering from a failed resync, which may have been a full one.
		full := inst.LibraryDSID != prevLibrary || inst.DailyDSID != prevDaily || prevSetupError == domain.SetupErrResyncFailed
		if err := s.Activator.Activate(ctx, inst, full); err != nil {
			return nil, err
		}
	}
	return inst, nil
}
