package store

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func (s *Store) GetDay(ctx context.Context, inst, date string) (*Day, error) {
	var d Day
	if err := s.get(ctx, InstPK(inst), DaySK(date), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) SetDayPage(ctx context.Context, inst, date, pageID string) error {
	return s.update(ctx, InstPK(inst), DaySK(date), "SET #p = :p", "attribute_exists(pk)",
		map[string]string{"#p": "notion_page_id"}, map[string]types.AttributeValue{":p": str(pageID)})
}

func (s *Store) ClearDayPage(ctx context.Context, inst, date string) error {
	return s.update(ctx, InstPK(inst), DaySK(date), "REMOVE #p", "", map[string]string{"#p": "notion_page_id"}, nil)
}

func (s *Store) MarkDaySynced(ctx context.Context, inst, date string, version int64) error {
	err := s.update(ctx, InstPK(inst), DaySK(date), "SET #sv = :v",
		"attribute_exists(pk) AND (attribute_not_exists(#sv) OR #sv < :v)",
		map[string]string{"#sv": "notion_synced_version"}, map[string]types.AttributeValue{":v": numI(version)})
	if errors.Is(err, ErrConditionFailed) {
		return nil
	}
	return err
}
