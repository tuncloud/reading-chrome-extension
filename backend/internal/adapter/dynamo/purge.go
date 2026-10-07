package dynamo

import "context"

// DeleteInstallationData deletes every INST#<botID> item (spec §2.17 purge).
func (s *Store) DeleteInstallationData(ctx context.Context, botID string) error {
	return s.deletePartition(ctx, InstPK(botID))
}

// DeleteUserData deletes every USER#<uid> item (spec §2.17 purge).
func (s *Store) DeleteUserData(ctx context.Context, uid string) error {
	return s.deletePartition(ctx, UserPK(uid))
}

func (s *Store) deletePartition(ctx context.Context, pk string) error {
	keys, err := s.PartitionKeys(ctx, pk)
	if err != nil {
		return err
	}
	return s.BatchDelete(ctx, keys)
}
