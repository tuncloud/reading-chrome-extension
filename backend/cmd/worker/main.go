// Command worker is the notion-writer Lambda (SQS FIFO event source, ReportBatchItemFailures).
package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"reading-tracker/backend/internal/app"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

func main() {
	log := app.Logger()
	cfg, err := config.Load(config.RoleWorker, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	awsCfg, err := app.AWSConfig(context.Background())
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	q := &queue.SQS{API: app.SQS(awsCfg), QueueURL: cfg.QueueURL}
	w := app.Worker(cfg, store.New(app.DynamoDB(awsCfg), cfg.TableName), q, q, log)
	lambda.Start(w.HandleSQS)
}
