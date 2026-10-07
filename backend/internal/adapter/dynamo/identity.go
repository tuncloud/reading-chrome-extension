package dynamo

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) GetProfile(ctx context.Context, uid string) (*domain.User, error) {
	var r userRecord
	if err := s.get(ctx, UserPK(uid), SKProfile, &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}

// UserIDForNotionUser resolves NUSER#<notion_user_id> → user_id (spec §2.2 step 4).
func (s *Store) UserIDForNotionUser(ctx context.Context, notionUserID string) (string, error) {
	var link struct {
		UserID string `dynamodbav:"user_id"`
	}
	if err := s.get(ctx, NUserPK(notionUserID), SKNUser, &link); err != nil {
		return "", err
	}
	return link.UserID, nil
}

// CreateUser writes PROFILE and the NUSER pointer in one transaction.
func (s *Store) CreateUser(ctx context.Context, uid string, u domain.User) error {
	prof, err := attributevalue.MarshalMap(newUserRecord(uid, u))
	if err != nil {
		return err
	}
	link := map[string]types.AttributeValue{"pk": str(NUserPK(u.NotionUserID)), "sk": str(SKNUser), "user_id": str(uid)}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.table, Item: prof}},
		{Put: &types.Put{TableName: &s.table, Item: link}},
	}})
	return err
}

func (s *Store) SetActiveInstallation(ctx context.Context, uid, botID string) error {
	return s.update(ctx, UserPK(uid), SKProfile, "SET #a = :a", "attribute_exists(pk)",
		map[string]string{"#a": "active_installation_id"}, map[string]types.AttributeValue{":a": str(botID)})
}

func (s *Store) MarkProfileDeleted(ctx context.Context, uid string, now time.Time) error {
	return s.update(ctx, UserPK(uid), SKProfile, "SET #d = :d", "attribute_exists(pk)",
		map[string]string{"#d": "deleted_at"}, map[string]types.AttributeValue{":d": str(domain.Timestamp(now))})
}

// CreateDevice writes DEVICE and its DEVTOKEN lookup together (spec §2.3 step 4).
func (s *Store) CreateDevice(ctx context.Context, uid, deviceID string, d domain.Device) error {
	dev, err := attributevalue.MarshalMap(newDeviceRecord(uid, deviceID, d))
	if err != nil {
		return err
	}
	tok := map[string]types.AttributeValue{
		"pk": str(DevTokenPK(d.TokenHMAC)), "sk": str(SKDevToken), "user_id": str(uid), "device_id": str(deviceID),
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: &s.table, Item: dev}},
		{Put: &types.Put{TableName: &s.table, Item: tok, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	}})
	return err
}

func (s *Store) GetDeviceToken(ctx context.Context, tokenHMAC string) (*domain.DeviceToken, error) {
	var r deviceTokenRecord
	if err := s.get(ctx, DevTokenPK(tokenHMAC), SKDevToken, &r); err != nil {
		return nil, err
	}
	return &domain.DeviceToken{UserID: r.UserID, DeviceID: r.DeviceID}, nil
}

func (s *Store) GetDevice(ctx context.Context, uid, deviceID string) (*domain.Device, error) {
	var r deviceRecord
	if err := s.get(ctx, UserPK(uid), DeviceSK(deviceID), &r); err != nil {
		return nil, err
	}
	d := r.toDomain()
	return &d, nil
}

// TouchDevice bumps last_seen_at only when it is over an hour old (spec §2.4 step 3).
func (s *Store) TouchDevice(ctx context.Context, uid, deviceID string, now time.Time) error {
	err := s.update(ctx, UserPK(uid), DeviceSK(deviceID), "SET #ls = :now",
		"attribute_exists(pk) AND (attribute_not_exists(#ls) OR #ls < :cutoff)",
		map[string]string{"#ls": "last_seen_at"},
		map[string]types.AttributeValue{":now": str(domain.Timestamp(now)), ":cutoff": str(domain.Timestamp(now.Add(-time.Hour)))})
	if errors.Is(err, domain.ErrConditionFailed) {
		return nil
	}
	return err
}

// RevokeDevice sets revoked_at and deletes the token lookup atomically (spec §2.17).
func (s *Store) RevokeDevice(ctx context.Context, uid, deviceID, tokenHMAC string, now time.Time) error {
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Update: &types.Update{
			TableName:                 &s.table,
			Key:                       key(UserPK(uid), DeviceSK(deviceID)),
			UpdateExpression:          aws.String("SET #r = :r"),
			ExpressionAttributeNames:  map[string]string{"#r": "revoked_at"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":r": str(domain.Timestamp(now))},
		}},
		{Delete: &types.Delete{TableName: &s.table, Key: key(DevTokenPK(tokenHMAC), SKDevToken)}},
	}})
	return err
}

func (s *Store) ListDevices(ctx context.Context, uid string) ([]domain.Device, error) {
	items, err := s.queryPrefix(ctx, UserPK(uid), "DEVICE#")
	if err != nil {
		return nil, err
	}
	var recs []deviceRecord
	if err := attributevalue.UnmarshalListOfMaps(items, &recs); err != nil {
		return nil, err
	}
	out := make([]domain.Device, len(recs))
	for i, r := range recs {
		out[i] = r.toDomain()
	}
	return out, nil
}

func (s *Store) DeleteDeviceToken(ctx context.Context, tokenHMAC string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &s.table, Key: key(DevTokenPK(tokenHMAC), SKDevToken)})
	return err
}
