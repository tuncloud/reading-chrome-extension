package notion

import (
	"context"

	"reading-tracker/backend/internal/usecase/port"
)

// Gateway implements the use cases' Notion ports on top of Client. Every Notion
// request and response shape lives in this adapter.
type Gateway struct {
	Client *Client
}

var (
	_ port.NotionAuth      = (*Gateway)(nil)
	_ port.NotionWorkspace = (*Gateway)(nil)
)

func (g *Gateway) AuthorizeURL(redirectURI, state string) string {
	return g.Client.AuthorizeURL(redirectURI, state)
}

func (g *Gateway) ExchangeCode(ctx context.Context, code, redirectURI string) (*port.NotionGrant, error) {
	tok, err := g.Client.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		return nil, translate(err, port.NotionNotFound)
	}
	return grant(tok), nil
}

func (g *Gateway) RefreshToken(ctx context.Context, refreshToken string) (*port.NotionGrant, error) {
	tok, err := g.Client.RefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, translate(err, port.NotionNotFound)
	}
	return grant(tok), nil
}

func grant(t *TokenResponse) *port.NotionGrant {
	return &port.NotionGrant{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, BotID: t.BotID,
		WorkspaceID: t.WorkspaceID, WorkspaceName: t.WorkspaceName, WorkspaceIcon: t.WorkspaceIcon,
		DuplicatedTemplateID: t.DuplicatedTemplateID, ExpiresIn: t.ExpiresIn, OwnerUserID: t.Owner.User.ID,
		OwnerName: t.Owner.User.Name, OwnerEmail: t.Owner.User.Person.Email}
}

func (g *Gateway) SearchPages(ctx context.Context, token, query string) ([]port.PageRef, error) {
	pages, err := g.Client.SearchPages(ctx, token, query)
	if err != nil {
		return nil, translate(err, port.NotionNotFound)
	}
	out := make([]port.PageRef, len(pages))
	for i, p := range pages {
		out[i] = port.PageRef{ID: p.ID, Title: p.Title, URL: p.URL}
	}
	return out, nil
}
