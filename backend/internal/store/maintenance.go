package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// PendingSync lists ART hashes and DAY dates whose version is ahead of
// notion_synced_version, i.e. never synced or changed since (T22).
func (s *Store) PendingSync(ctx context.Context, inst string) (hashes, dates []string, err error) {
	if hashes, err = s.pending(ctx, inst, "ART#"); err != nil {
		return nil, nil, err
	}
	if dates, err = s.pending(ctx, inst, "DAY#"); err != nil {
		return nil, nil, err
	}
	return hashes, dates, nil
}

func (s *Store) pending(ctx context.Context, inst, prefix string) ([]string, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression:    aws.String("pk = :pk AND begins_with(sk, :prefix)"),
		ProjectionExpression:      aws.String("sk, #v, #sv"),
		ExpressionAttributeNames:  map[string]string{"#v": "version", "#sv": "notion_synced_version"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": str(InstPK(inst)), ":prefix": str(prefix)},
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, it := range items {
		var row struct {
			SK     string `dynamodbav:"sk"`
			Ver    int64  `dynamodbav:"version"`
			Synced int64  `dynamodbav:"notion_synced_version"`
		}
		if err := attributevalue.UnmarshalMap(it, &row); err != nil {
			return nil, err
		}
		if row.Ver > row.Synced {
			out = append(out, strings.TrimPrefix(row.SK, prefix))
		}
	}
	return out, nil
}

// Key identifies one item.
type Key struct{ PK, SK string }

// PartitionKeys lists the keys of every item in partition pk (purge, T23).
func (s *Store) PartitionKeys(ctx context.Context, pk string) ([]Key, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression:    aws.String("pk = :pk"),
		ProjectionExpression:      aws.String("pk, sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": str(pk)},
	})
	if err != nil {
		return nil, err
	}
	keys := make([]Key, len(items))
	for i, it := range items {
		keys[i] = Key{PK: it["pk"].(*types.AttributeValueMemberS).Value, SK: it["sk"].(*types.AttributeValueMemberS).Value}
	}
	return keys, nil
}

const batchWriteLimit = 25

// BatchDelete deletes keys 25 at a time, retrying UnprocessedItems with back-off.
func (s *Store) BatchDelete(ctx context.Context, keys []Key) error {
	for start := 0; start < len(keys); start += batchWriteLimit {
		chunk := keys[start:min(start+batchWriteLimit, len(keys))]
		reqs := make([]types.WriteRequest, len(chunk))
		for i, k := range chunk {
			reqs[i] = types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: key(k.PK, k.SK)}}
		}
		pending := map[string][]types.WriteRequest{s.table: reqs}
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt > 0 {
				if attempt > maxBatchAttempts {
					return fmt.Errorf("store: batch delete: items still unprocessed after %d attempts", maxBatchAttempts)
				}
				s.sleep(backoff(attempt))
			}
			res, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
			if err != nil {
				return err
			}
			pending = res.UnprocessedItems
		}
	}
	return nil
}

// DeleteNotionUserLink deletes NUSER#<id> only while it still points at uid.
func (s *Store) DeleteNotionUserLink(ctx context.Context, notionUserID, uid string) error {
	_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:                 &s.table,
		Key:                       key(NUserPK(notionUserID), SKNUser),
		ConditionExpression:       aws.String("#u = :u"),
		ExpressionAttributeNames:  map[string]string{"#u": "user_id"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":u": str(uid)},
	})
	if isConditionFailed(err) {
		return nil
	}
	return err
}
