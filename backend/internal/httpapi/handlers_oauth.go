package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/oklog/ulid/v2"

	"reading-tracker/backend/internal/crypto"
	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
)

const (
	oauthStateTTL = 10 * time.Minute
	authCodeTTL   = 2 * time.Minute
)

// oauthStart serves GET /v1/oauth/notion/start (spec §2.1).
func (h *handlers) oauthStart(c echo.Context) error {
	q := c.QueryParams()
	ext := q.Get("ext_redirect")
	if !h.allowedExtRedirect(ext) {
		return errInvalid("ext_redirect is not an allowed extension redirect")
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !crypto.ValidChallenge(challenge) {
		return errInvalid("code_challenge must be an S256 challenge")
	}
	state, err := crypto.RandomB64URL(32)
	if err != nil {
		return err
	}
	st := store.OAuthState{ExtRedirect: ext, CodeChallenge: challenge}
	if err := h.Store.PutOAuthState(c.Request().Context(), state, st, h.Now().Add(oauthStateTTL)); err != nil {
		return err
	}
	return c.Redirect(http.StatusFound, h.Notion.AuthorizeURL(h.redirectURI(c), state))
}

// allowedExtRedirect accepts only https://<id>.chromiumapp.org/… for configured ids.
func (h *handlers) allowedExtRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	id, ok := strings.CutSuffix(strings.ToLower(u.Hostname()), ".chromiumapp.org")
	return ok && h.Config.AllowedExtensionIDs[id]
}

// redirectURI is PUBLIC_BASE_URL or the request's own origin, plus the callback path (C14).
func (h *handlers) redirectURI(c echo.Context) string {
	base := h.Config.PublicBaseURL
	if base == "" {
		base = c.Scheme() + "://" + c.Request().Host
	}
	return base + "/v1/oauth/notion/callback"
}

// oauthCallback serves GET /v1/oauth/notion/callback (spec §2.2).
func (h *handlers) oauthCallback(c echo.Context) error {
	ctx := c.Request().Context()
	q := c.QueryParams()
	st, err := h.Store.ConsumeOAuthState(ctx, q.Get("state"), h.Now())
	if errors.Is(err, store.ErrNotFound) {
		return c.String(http.StatusBadRequest, "Link expired, try again")
	}
	if err != nil {
		return err
	}
	if e := q.Get("error"); e != "" {
		return redirectWith(c, st.ExtRedirect, "error", e)
	}
	tok, err := h.Notion.ExchangeCode(ctx, q.Get("code"), h.redirectURI(c))
	if err != nil {
		h.Log.WarnContext(ctx, "oauth: code exchange failed", "err", err)
		return redirectWith(c, st.ExtRedirect, "error", "exchange_failed")
	}
	if tok.BotID == "" || tok.Owner.User.ID == "" {
		return redirectWith(c, st.ExtRedirect, "error", "unsupported_owner")
	}
	uid, err := h.resolveUser(ctx, tok)
	if err != nil {
		return err
	}
	inst, err := h.upsertInstallation(ctx, uid, tok)
	if err != nil {
		return err
	}
	code, err := crypto.RandomB64URL(32)
	if err != nil {
		return err
	}
	ac := store.AuthCode{UserID: uid, InstallationID: inst.ID(), CodeChallenge: st.CodeChallenge, ExtRedirect: st.ExtRedirect}
	if err := h.Store.PutAuthCode(ctx, code, ac, h.Now().Add(authCodeTTL)); err != nil {
		return err
	}
	return redirectWith(c, st.ExtRedirect, "code", code)
}

func redirectWith(c echo.Context, base, key, val string) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return c.Redirect(http.StatusFound, u.String())
}

// resolveUser maps the Notion owner to our user, creating one if needed (§2.2 step 4).
// A user whose deletion is pending is treated as absent.
func (h *handlers) resolveUser(ctx context.Context, tok *notion.TokenResponse) (string, error) {
	nuid := tok.Owner.User.ID
	uid, err := h.Store.UserIDForNotionUser(ctx, nuid)
	switch {
	case err == nil:
		prof, perr := h.Store.GetProfile(ctx, uid)
		if perr == nil && prof.DeletedAt == "" {
			return uid, nil
		}
		if perr != nil && !errors.Is(perr, store.ErrNotFound) {
			return "", perr
		}
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	uid = ulid.Make().String()
	return uid, h.Store.CreateUser(ctx, uid, store.Profile{NotionUserID: nuid, Name: tok.Owner.User.Name,
		Email: tok.Owner.User.Person.Email, CreatedAt: store.Timestamp(h.Now())})
}

// upsertInstallation stores INST#<bot_id>/META with sealed tokens (C18) and decides
// its status (§2.2 steps 5–7).
func (h *handlers) upsertInstallation(ctx context.Context, uid string, tok *notion.TokenResponse) (*store.Installation, error) {
	now := h.Now()
	inst, err := h.Store.GetInstallation(ctx, tok.BotID)
	if errors.Is(err, store.ErrNotFound) {
		inst, err = &store.Installation{CreatedAt: store.Timestamp(now)}, nil
	}
	if err != nil {
		return nil, err
	}
	prevLibrary, prevDaily, prevSetupError := inst.LibraryDSID, inst.DailyDSID, inst.SetupError
	aad := []byte(tok.BotID)
	if inst.AccessTokenEnc, err = h.Config.Keys.Seal([]byte(tok.AccessToken), aad); err != nil {
		return nil, err
	}
	inst.RefreshTokenEnc = ""
	if tok.RefreshToken != "" {
		if inst.RefreshTokenEnc, err = h.Config.Keys.Seal([]byte(tok.RefreshToken), aad); err != nil {
			return nil, err
		}
	}
	inst.TokenExpiresAt = ""
	if tok.ExpiresIn > 0 {
		inst.TokenExpiresAt = store.Timestamp(now.Add(time.Duration(tok.ExpiresIn) * time.Second))
	}
	inst.TokenVersion++
	inst.UserID, inst.WorkspaceID, inst.WorkspaceName, inst.WorkspaceIcon = uid, tok.WorkspaceID, tok.WorkspaceName, tok.WorkspaceIcon
	inst.UpdatedAt, inst.SetupError = store.Timestamp(now), ""

	switch {
	case tok.DuplicatedTemplateID != "":
		inst.DuplicatedTemplateID = tok.DuplicatedTemplateID
		db, err := h.setup.ResolveTemplate(ctx, tok.AccessToken, tok.DuplicatedTemplateID)
		if err != nil {
			inst.Status, inst.SetupError = store.StatusNeedsSetup, setup.Reason(err)
			h.Log.WarnContext(ctx, "oauth: template resolution failed", "reason", inst.SetupError, "err", err)
		} else {
			inst.ApplyDatabases(db)
			inst.Status = store.StatusActive
		}
	case inst.HasDatabases():
		inst.Status = store.StatusActive // reconnect to an already set-up workspace
	default:
		inst.Status = store.StatusNeedsSetup
	}

	if err := h.Store.PutInstallation(ctx, tok.BotID, inst); err != nil {
		return nil, err
	}
	if err := h.Store.PutUserInstallation(ctx, uid, tok.BotID, now); err != nil {
		return nil, err
	}
	if err := h.Store.SetActiveInstallation(ctx, uid, tok.BotID); err != nil {
		return nil, err
	}
	if inst.Status == store.StatusActive {
		// New databases (e.g. a newly duplicated template) → re-send everything (scenario 10).
		// So does recovering from a failed resync, which may have been a full one.
		full := inst.LibraryDSID != prevLibrary || inst.DailyDSID != prevDaily || prevSetupError == setupErrResyncFailed
		if err := h.resync(ctx, inst, full); err != nil {
			return nil, err
		}
	}
	return inst, nil
}
