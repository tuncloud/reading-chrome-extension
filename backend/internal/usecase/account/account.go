// Package account serves the signed-in user's own account (spec §2.5, §2.17).
package account

import (
	"context"
	"errors"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/auth"
	"reading-tracker/backend/internal/usecase/port"
)

type Service struct {
	Users         port.Users
	Devices       port.Devices
	Installations port.Installations
	Queue         port.SyncQueue
	Now           func() time.Time
}

// Me is the caller's user, device and active installation (nil if none).
type Me struct {
	User         *domain.User
	Device       *domain.Device
	Installation *domain.Installation
}

func (s *Service) Me(ctx context.Context, p *auth.Principal) (*Me, error) {
	me := &Me{User: p.User, Device: p.Device}
	if id := p.InstallationID(); id != "" {
		inst, err := s.Installations.GetInstallation(ctx, id)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		me.Installation = inst
	}
	return me, nil
}

// Delete enqueues the purge, marks the profile deleted, and revokes every device.
// Notion content is never deleted (spec §2.17).
//
// The order keeps every failure retryable. Nothing irreversible happens before the
// purge is queued, so a failed enqueue leaves the account untouched. If marking fails
// after the enqueue, the worker ignores the purge (live user) and the retry re-sends
// and marks. Once marked, auth rejects the user, so a failed revoke still locks them
// out and the purge removes the devices anyway. This relies on the queue's delivery
// delay (≥ the 120 s debounce bucket) exceeding this call's runtime.
func (s *Service) Delete(ctx context.Context, p *auth.Principal) error {
	devices, err := s.Devices.ListDevices(ctx, p.UserID)
	if err != nil {
		return err
	}
	if err := s.Queue.EnqueuePurge(ctx, p.UserID, p.InstallationID()); err != nil {
		return err
	}
	now := s.Now()
	if err := s.Users.MarkProfileDeleted(ctx, p.UserID, now); err != nil {
		return err
	}
	for _, d := range devices {
		if d.Revoked() {
			continue
		}
		if err := s.Devices.RevokeDevice(ctx, p.UserID, d.ID, d.TokenHMAC, now); err != nil {
			return err
		}
	}
	return nil
}
