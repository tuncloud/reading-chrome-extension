package notionsync

import (
	"context"
	"errors"
	"slices"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// purge deletes every stored item of a deleted user (spec §2.17). It never touches
// Notion, skips users not marked deleted, and skips installations that now belong to
// someone else.
func (s *Service) purge(ctx context.Context, job port.Job) error {
	uid := job.UserID
	if uid == "" {
		return nil
	}
	prof, err := s.Users.GetProfile(ctx, uid)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		prof = nil
	case err != nil:
		return err
	case !prof.Deleted():
		s.Log.WarnContext(ctx, "notionsync: purge for a live user ignored", "user_id", uid)
		return nil
	}

	insts, err := s.Installations.ListUserInstallationIDs(ctx, uid)
	if err != nil {
		return err
	}
	if job.InstallationID != "" && !slices.Contains(insts, job.InstallationID) {
		insts = append(insts, job.InstallationID)
	}
	for _, id := range insts {
		inst, err := s.Installations.GetInstallation(ctx, id)
		if err == nil && inst.UserID != uid {
			continue
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err := s.Purger.DeleteInstallationData(ctx, id); err != nil {
			return err
		}
	}

	devices, err := s.Devices.ListDevices(ctx, uid)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if err := s.Devices.DeleteDeviceToken(ctx, d.TokenHMAC); err != nil {
			return err
		}
	}
	if prof != nil {
		if err := s.Users.DeleteNotionUserLink(ctx, prof.NotionUserID, uid); err != nil {
			return err
		}
	}
	return s.Purger.DeleteUserData(ctx, uid)
}
