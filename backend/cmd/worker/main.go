// Command worker is the notion-writer Lambda (SQS FIFO event source, ReportBatchItemFailures).
package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/wire"
	"reading-tracker/backend/internal/wire/awswire"
)

func main() {
	log := wire.Logger()
	cfg, err := config.Load(config.RoleWorker, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	awsCfg, err := awswire.AWSConfig(context.Background())
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	q := &queue.SQS{API: awswire.SQS(awsCfg), QueueURL: cfg.QueueURL}
	w := awswire.Worker(cfg, dynamo.New(awswire.DynamoDB(awsCfg), cfg.TableName), q, q, log)
	lambda.Start(w.HandleSQS)
}
