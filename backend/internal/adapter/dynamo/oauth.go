package dynamo

import (
	"context"
	"reading-tracker/backend/internal/domain"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func (s *Store) PutOAuthState(ctx context.Context, state string, st domain.OAuthState, expires time.Time) error {
	return s.putItem(ctx, oauthStateRecord{PK: OAuthStatePK(state), SK: SKState, ExtRedirect: st.ExtRedirect,
		CodeChallenge: st.CodeChallenge, TTL: expires.Unix()})
}

func (s *Store) ConsumeOAuthState(ctx context.Context, state string, now time.Time) (*domain.OAuthState, error) {
	var r oauthStateRecord
	if err := s.consume(ctx, OAuthStatePK(state), SKState, now, &r); err != nil {
		return nil, err
	}
	return &domain.OAuthState{ExtRedirect: r.ExtRedirect, CodeChallenge: r.CodeChallenge}, nil
}

func (s *Store) PutAuthCode(ctx context.Context, code string, ac domain.AuthCode, expires time.Time) error {
	return s.putItem(ctx, authCodeRecord{PK: AuthCodePK(code), SK: SKCode, UserID: ac.UserID, InstallationID: ac.InstallationID,
		CodeChallenge: ac.CodeChallenge, ExtRedirect: ac.ExtRedirect, TTL: expires.Unix()})
}

func (s *Store) ConsumeAuthCode(ctx context.Context, code string, now time.Time) (*domain.AuthCode, error) {
	var r authCodeRecord
	if err := s.consume(ctx, AuthCodePK(code), SKCode, now, &r); err != nil {
		return nil, err
	}
	return &domain.AuthCode{UserID: r.UserID, InstallationID: r.InstallationID, CodeChallenge: r.CodeChallenge,
		ExtRedirect: r.ExtRedirect}, nil
}

// consume deletes a single-use TTL item and returns it. Expired items that DynamoDB
// hasn't reaped yet fail the condition, because TTL deletion is lazy (C12).
func (s *Store) consume(ctx context.Context, pk, sk string, now time.Time, out any) error {
	res, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:                 &s.table,
		Key:                       key(pk, sk),
		ConditionExpression:       aws.String("attribute_exists(pk) AND #ttl > :now"),
		ExpressionAttributeNames:  map[string]string{"#ttl": "ttl"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":now": numI(now.Unix())},
		ReturnValues:              types.ReturnValueAllOld,
	})
	if isConditionFailed(err) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return attributevalue.UnmarshalMap(res.Attributes, out)
}
