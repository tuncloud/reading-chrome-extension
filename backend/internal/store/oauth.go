package store

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func (s *Store) PutOAuthState(ctx context.Context, state string, st OAuthState, expires time.Time) error {
	st.PK, st.SK, st.TTL = OAuthStatePK(state), SKState, expires.Unix()
	return s.putItem(ctx, st)
}

func (s *Store) ConsumeOAuthState(ctx context.Context, state string, now time.Time) (*OAuthState, error) {
	var st OAuthState
	if err := s.consume(ctx, OAuthStatePK(state), SKState, now, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) PutAuthCode(ctx context.Context, code string, ac AuthCode, expires time.Time) error {
	ac.PK, ac.SK, ac.TTL = AuthCodePK(code), SKCode, expires.Unix()
	return s.putItem(ctx, ac)
}

func (s *Store) ConsumeAuthCode(ctx context.Context, code string, now time.Time) (*AuthCode, error) {
	var ac AuthCode
	if err := s.consume(ctx, AuthCodePK(code), SKCode, now, &ac); err != nil {
		return nil, err
	}
	return &ac, nil
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
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return attributevalue.UnmarshalMap(res.Attributes, out)
}
