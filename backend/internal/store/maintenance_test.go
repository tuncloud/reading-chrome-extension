package store_test

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/store/storetest"
)

// T22: only items whose version is ahead of notion_synced_version are pending.
func TestPendingSync(t *testing.T) {
	s := storetest.New(t).Store
	other := "https://example.com/post"
	push(t, s, segment("00000000-0000-4000-8000-000000000001", articleURL, 0, 60, 0.5, testNow))
	push(t, s, segment("00000000-0000-4000-8000-000000000002", other, 0, 60, 0.5, testNow))
	must(t, s.MarkArticleSynced(ctx, "bot-1", store.URLHash(articleURL), 1, false, testNow))

	hashes, dates, err := s.PendingSync(ctx, "bot-1")
	must(t, err)
	if len(hashes) != 1 || hashes[0] != store.URLHash(other) {
		t.Fatalf("hashes = %v", hashes)
	}
	if len(dates) != 1 || dates[0] != "2026-10-06" {
		t.Fatalf("dates = %v", dates)
	}
	must(t, s.MarkDaySynced(ctx, "bot-1", "2026-10-06", 2))
	if _, dates, _ = s.PendingSync(ctx, "bot-1"); len(dates) != 0 {
		t.Fatalf("synced day still pending: %v", dates)
	}
}

func TestBatchDeleteRemovesWholePartition(t *testing.T) {
	env := storetest.New(t)
	for i := 0; i < 30; i++ { // > 25 forces two BatchWriteItem chunks
		_, err := env.DB.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(env.Table), Item: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "INST#bot-1"}, "sk": &types.AttributeValueMemberS{Value: fmt.Sprintf("SEG#%02d", i)},
		}})
		must(t, err)
	}
	keys, err := env.Store.PartitionKeys(ctx, "INST#bot-1")
	must(t, err)
	if len(keys) != 30 {
		t.Fatalf("PartitionKeys = %d", len(keys))
	}
	must(t, env.Store.BatchDelete(ctx, keys))
	if keys, _ = env.Store.PartitionKeys(ctx, "INST#bot-1"); len(keys) != 0 {
		t.Fatalf("%d items left", len(keys))
	}
}

func TestDeleteNotionUserLinkOnlyForOwner(t *testing.T) {
	s := storetest.New(t).Store
	must(t, s.CreateUser(ctx, "u2", store.Profile{NotionUserID: "n1", Name: "new owner", CreatedAt: store.Timestamp(testNow)}))
	must(t, s.DeleteNotionUserLink(ctx, "n1", "u1")) // stale owner: no-op
	if uid, err := s.UserIDForNotionUser(ctx, "n1"); err != nil || uid != "u2" {
		t.Fatalf("link of another user deleted: %q %v", uid, err)
	}
	must(t, s.DeleteNotionUserLink(ctx, "n1", "u2"))
	if _, err := s.UserIDForNotionUser(ctx, "n1"); err == nil {
		t.Fatal("link not deleted")
	}
}
