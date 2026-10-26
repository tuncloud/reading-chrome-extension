package dynamo_test

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"reading-tracker/backend/internal/adapter/dynamo/dynamotest"
)

func TestBatchDeleteRemovesWholePartition(t *testing.T) {
	env := dynamotest.New(t)
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
