package notion

import (
	"context"
	"net/http"
	"net/url"
)

// TokenResponse is the /v1/oauth/token body. ExpiresIn is kept if Notion ever sends it (🔶 §7).
type TokenResponse struct {
	AccessToken          string     `json:"access_token"`
	RefreshToken         string     `json:"refresh_token"`
	BotID                string     `json:"bot_id"`
	WorkspaceID          string     `json:"workspace_id"`
	WorkspaceName        string     `json:"workspace_name"`
	WorkspaceIcon        string     `json:"workspace_icon"`
	DuplicatedTemplateID string     `json:"duplicated_template_id"`
	ExpiresIn            int        `json:"expires_in"`
	Owner                TokenOwner `json:"owner"`
}

type TokenOwner struct {
	User struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Person struct {
			Email string `json:"email"`
		} `json:"person"`
	} `json:"user"`
}

// AuthorizeURL is where /v1/oauth/notion/start redirects (spec §2.1 step 3).
func (c *Client) AuthorizeURL(redirectURI, state string) string {
	q := url.Values{
		"client_id": {c.ClientID}, "response_type": {"code"}, "owner": {"user"},
		"redirect_uri": {redirectURI}, "state": {state},
	}
	return c.BaseURL + "/v1/oauth/authorize?" + q.Encode()
}

func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	return c.token(ctx, map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": redirectURI})
}

// RefreshToken exchanges a refresh token. Notion rotates refresh tokens, so callers
// must persist the returned one (C7).
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	return c.token(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken})
}

func (c *Client) token(ctx context.Context, body map[string]string) (*TokenResponse, error) {
	var out TokenResponse
	if err := c.do(ctx, http.MethodPost, "/v1/oauth/token", basicAuth, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
