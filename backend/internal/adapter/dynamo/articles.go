package dynamo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/domain"
)

const batchGetLimit = 100

func (s *Store) GetArticle(ctx context.Context, inst, hash string) (*domain.Article, error) {
	var r articleRecord
	if err := s.get(ctx, InstPK(inst), ArticleSK(hash), &r); err != nil {
		return nil, err
	}
	return r.toDomain(), nil
}

// BatchGetArticles loads ART items keyed by hash, ≤100 keys per call, retrying
// UnprocessedKeys with back-off. Missing articles are absent from the map.
func (s *Store) BatchGetArticles(ctx context.Context, inst string, hashes []string) (map[string]*domain.Article, error) {
	out := map[string]*domain.Article{}
	for start := 0; start < len(hashes); start += batchGetLimit {
		chunk := hashes[start:min(start+batchGetLimit, len(hashes))]
		keys := make([]map[string]types.AttributeValue, 0, len(chunk))
		seen := map[string]bool{}
		for _, h := range chunk {
			if !seen[h] {
				seen[h] = true
				keys = append(keys, key(InstPK(inst), ArticleSK(h)))
			}
		}
		req := map[string]types.KeysAndAttributes{s.table: {Keys: keys}}
		for attempt := 0; len(req) > 0; attempt++ {
			if attempt > 0 {
				if attempt > maxBatchAttempts {
					return nil, fmt.Errorf("store: batch get: keys still unprocessed after %d attempts", maxBatchAttempts)
				}
				s.sleep(backoff(attempt))
			}
			res, err := s.db.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: req})
			if err != nil {
				return nil, err
			}
			for _, item := range res.Responses[s.table] {
				var r articleRecord
				if err := attributevalue.UnmarshalMap(item, &r); err != nil {
					return nil, err
				}
				a := r.toDomain()
				out[a.Hash] = a
			}
			req = res.UnprocessedKeys
		}
	}
	return out, nil
}

func (s *Store) SetArticlePage(ctx context.Context, inst, hash, pageID, url, dataSourceID string) error {
	return s.update(ctx, InstPK(inst), ArticleSK(hash), "SET #p = :p, #u = :u, #d = :d", "attribute_exists(pk)",
		map[string]string{"#p": "notion_page_id", "#u": "notion_url", "#d": "notion_ds_id"},
		map[string]types.AttributeValue{":p": str(pageID), ":u": str(url), ":d": str(dataSourceID)})
}

func (s *Store) ClearArticlePage(ctx context.Context, inst, hash string) error {
	return s.update(ctx, InstPK(inst), ArticleSK(hash), "REMOVE #p, #u, #d", "",
		map[string]string{"#p": "notion_page_id", "#u": "notion_url", "#d": "notion_ds_id"}, nil)
}

// MarkArticleSynced records the version written to Notion (spec §2.13 step 5).
// An older version never overwrites a newer one.
func (s *Store) MarkArticleSynced(ctx context.Context, inst, hash string, version int64, completed bool, now time.Time) error {
	err := s.update(ctx, InstPK(inst), ArticleSK(hash), "SET #sv = :v, #sa = :now, #c = :c",
		"attribute_exists(pk) AND (attribute_not_exists(#sv) OR #sv < :v)",
		map[string]string{"#sv": "notion_synced_version", "#sa": "notion_synced_at", "#c": "completed"},
		map[string]types.AttributeValue{":v": numI(version), ":now": str(domain.Timestamp(now)), ":c": boolV(completed)})
	if errors.Is(err, domain.ErrConditionFailed) {
		return nil
	}
	return err
}
