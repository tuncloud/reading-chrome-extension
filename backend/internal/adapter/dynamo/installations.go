package dynamo

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/domain"
)

func (s *Store) GetInstallation(ctx context.Context, botID string) (*domain.Installation, error) {
	var r installationRecord
	if err := s.get(ctx, InstPK(botID), SKMeta, &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}

// PutInstallation writes the whole META item (used by the OAuth callback upsert).
func (s *Store) PutInstallation(ctx context.Context, botID string, inst *domain.Installation) error {
	return s.putItem(ctx, newInstallationRecord(botID, inst))
}

// PutUserInstallation records USER#<uid>/INST#<bot_id>, keeping the first created_at.
func (s *Store) PutUserInstallation(ctx context.Context, uid, botID string, now time.Time) error {
	item := map[string]types.AttributeValue{"pk": str(UserPK(uid)), "sk": str(InstPK(botID)), "created_at": str(domain.Timestamp(now))}
	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: &s.table, Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	if isConditionFailed(err) {
		return nil
	}
	return err
}

func (s *Store) ListUserInstallationIDs(ctx context.Context, uid string) ([]string, error) {
	items, err := s.queryPrefix(ctx, UserPK(uid), "INST#")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		var k struct {
			SK string `dynamodbav:"sk"`
		}
		if err := attributevalue.UnmarshalMap(it, &k); err != nil {
			return nil, err
		}
		ids = append(ids, strings.TrimPrefix(k.SK, "INST#"))
	}
	return ids, nil
}

// SetInstallationStatus sets status; an empty setupError removes setup_error.
func (s *Store) SetInstallationStatus(ctx context.Context, botID, status, setupError string, now time.Time) error {
	names := map[string]string{"#s": "status", "#u": "updated_at", "#e": "setup_error"}
	values := map[string]types.AttributeValue{":s": str(status), ":u": str(domain.Timestamp(now))}
	expr := "SET #s = :s, #u = :u REMOVE #e"
	if setupError != "" {
		expr = "SET #s = :s, #u = :u, #e = :e"
		values[":e"] = str(setupError)
	}
	return s.update(ctx, InstPK(botID), SKMeta, expr, "attribute_exists(pk)", names, values)
}

// SaveInstallationDatabases stores the resolved Notion IDs and marks the installation active.
func (s *Store) SaveInstallationDatabases(ctx context.Context, botID string, db domain.DBConfig, now time.Time) error {
	props, err := attributevalue.Marshal(db.Props)
	if err != nil {
		return err
	}
	return s.update(ctx, InstPK(botID), SKMeta,
		"SET #ldb = :ldb, #lds = :lds, #lurl = :lurl, #ddb = :ddb, #dds = :dds, #p = :p, #s = :active, #u = :u REMOVE #e",
		"attribute_exists(pk)",
		map[string]string{"#ldb": "library_db_id", "#lds": "library_ds_id", "#lurl": "library_url", "#ddb": "daily_db_id",
			"#dds": "daily_ds_id", "#p": "props", "#s": "status", "#u": "updated_at", "#e": "setup_error"},
		map[string]types.AttributeValue{":ldb": str(db.LibraryDBID), ":lds": str(db.LibraryDSID), ":lurl": str(db.LibraryURL),
			":ddb": str(db.DailyDBID), ":dds": str(db.DailyDSID), ":p": props, ":active": str(domain.StatusActive), ":u": str(domain.Timestamp(now))})
}

// UpdateInstallationTokens persists both rotated tokens, guarded by token_version (C7).
func (s *Store) UpdateInstallationTokens(ctx context.Context, botID, accessEnc, refreshEnc string, oldVersion int, now time.Time) error {
	return s.update(ctx, InstPK(botID), SKMeta,
		"SET #a = :a, #r = :r, #v = :new, #u = :u", "#v = :old",
		map[string]string{"#a": "access_token_enc", "#r": "refresh_token_enc", "#v": "token_version", "#u": "updated_at"},
		map[string]types.AttributeValue{":a": str(accessEnc), ":r": str(refreshEnc), ":new": numI(int64(oldVersion + 1)),
			":old": numI(int64(oldVersion)), ":u": str(domain.Timestamp(now))})
}
