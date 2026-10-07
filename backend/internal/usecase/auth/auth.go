// Package auth authenticates extension devices and issues their tokens (spec §2.3, §2.4, §2.17).
package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/platform/textutil"
	"reading-tracker/backend/internal/usecase/apperr"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Users         port.Users
	Devices       port.Devices
	Grants        port.OAuthGrants
	Installations port.Installations
	Pepper        []byte
	Now           func() time.Time
	Log           *slog.Logger
}

// Principal is the authenticated device and its user.
type Principal struct {
	UserID    string
	TokenHMAC string
	Device    *domain.Device
	User      *domain.User
}

// InstallationID is the user's active installation (one per user in v1), or "".
func (p *Principal) InstallationID() string { return p.User.ActiveInstallationID }

// Authenticate resolves a device bearer token (spec §2.4). Unknown, revoked or
// deleted-account tokens are apperr Unauthorized.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if !crypto.ValidDeviceTokenFormat(token) {
		return nil, apperr.NewUnauthorized()
	}
	mac := crypto.TokenHMAC(s.Pepper, token)
	ref, err := s.Devices.GetDeviceToken(ctx, mac)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NewUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	dev, err := s.Devices.GetDevice(ctx, ref.UserID, ref.DeviceID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && dev.Revoked()) {
		return nil, apperr.NewUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	user, err := s.Users.GetProfile(ctx, ref.UserID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && user.Deleted()) {
		return nil, apperr.NewUnauthorized()
	}
	if err != nil {
		return nil, err
	}
	s.touch(ctx, ref.UserID, dev)
	return &Principal{UserID: ref.UserID, TokenHMAC: mac, Device: dev, User: user}, nil
}

// touch updates last_seen_at at most hourly. The in-memory check skips the write
// (and its WCU) on most requests; the conditional update closes races.
func (s *Service) touch(ctx context.Context, uid string, dev *domain.Device) {
	now := s.Now()
	if seen, err := time.Parse(time.RFC3339, dev.LastSeenAt); err == nil && now.Sub(seen) < time.Hour {
		return
	}
	if err := s.Devices.TouchDevice(ctx, uid, dev.ID, now); err != nil {
		s.Log.WarnContext(ctx, "auth: touch device failed", "err", err)
	}
}

// Grant is a freshly issued device token.
type Grant struct {
	DeviceToken  string
	DeviceID     string
	User         *domain.User
	Installation *domain.Installation
}

// RedeemCode exchanges a one-time code for a device token (spec §2.3). The code is
// consumed before the PKCE check, so a wrong verifier burns it.
func (s *Service) RedeemCode(ctx context.Context, code, verifier, deviceName string) (*Grant, error) {
	if !domain.PlausibleOneTimeCode(code) {
		return nil, apperr.NewInvalidGrant()
	}
	now := s.Now()
	ac, err := s.Grants.ConsumeAuthCode(ctx, code, now)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, apperr.NewInvalidGrant()
	}
	if err != nil {
		return nil, err
	}
	if !crypto.VerifyPKCE(verifier, ac.CodeChallenge) {
		return nil, apperr.NewInvalidGrant()
	}
	token, err := crypto.NewDeviceToken()
	if err != nil {
		return nil, err
	}
	deviceID := ulid.Make().String()
	dev := domain.Device{Name: cleanDeviceName(deviceName), TokenHMAC: crypto.TokenHMAC(s.Pepper, token),
		CreatedAt: domain.Timestamp(now), LastSeenAt: domain.Timestamp(now)}
	if err := s.Devices.CreateDevice(ctx, ac.UserID, deviceID, dev); err != nil {
		return nil, err
	}
	user, err := s.Users.GetProfile(ctx, ac.UserID)
	if err != nil {
		return nil, err
	}
	inst, err := s.Installations.GetInstallation(ctx, ac.InstallationID)
	if errors.Is(err, domain.ErrNotFound) {
		inst, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Grant{DeviceToken: token, DeviceID: deviceID, User: user, Installation: inst}, nil
}

// Logout revokes the calling device (spec §2.17).
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return s.Devices.RevokeDevice(ctx, p.UserID, p.Device.ID, p.TokenHMAC, s.Now())
}

func cleanDeviceName(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return "Unknown device"
	}
	return textutil.Truncate(s, 100)
}
