package worker

import (
	"context"
	"errors"
	"slices"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
)

// handlePurge deletes every DynamoDB item of a deleted user (spec §2.17). It never
// touches Notion, skips users not marked deleted, and skips installations that now
// belong to someone else (interpretation 5).
func (w *Worker) handlePurge(ctx context.Context, m queue.Message) error {
	uid := m.UserID
	if uid == "" {
		return nil
	}
	prof, err := w.Store.GetProfile(ctx, uid)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		prof = nil
	case err != nil:
		return err
	case prof.DeletedAt == "":
		w.Log.WarnContext(ctx, "worker: purge for a live user ignored", "user_id", uid)
		return nil
	}

	insts, err := w.Store.ListUserInstallationIDs(ctx, uid)
	if err != nil {
		return err
	}
	if m.InstallationID != "" && !slices.Contains(insts, m.InstallationID) {
		insts = append(insts, m.InstallationID)
	}
	for _, id := range insts {
		inst, err := w.Store.GetInstallation(ctx, id)
		if err == nil && inst.UserID != uid {
			continue
		}
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err := w.purgePartition(ctx, dynamo.InstPK(id)); err != nil {
			return err
		}
	}

	devices, err := w.Store.ListDevices(ctx, uid)
	if err != nil {
		return err
	}
	for _, d := range devices {
		if err := w.Store.DeleteDeviceToken(ctx, d.TokenHMAC); err != nil {
			return err
		}
	}
	if prof != nil {
		if err := w.Store.DeleteNotionUserLink(ctx, prof.NotionUserID, uid); err != nil {
			return err
		}
	}
	return w.purgePartition(ctx, dynamo.UserPK(uid))
}

func (w *Worker) purgePartition(ctx context.Context, pk string) error {
	keys, err := w.Store.PartitionKeys(ctx, pk)
	if err != nil {
		return err
	}
	return w.Store.BatchDelete(ctx, keys)
}
