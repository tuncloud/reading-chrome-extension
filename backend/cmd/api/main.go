// Command api is the api Lambda: Echo behind a Function URL (payload v2).
package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	echoadapter "github.com/awslabs/aws-lambda-go-api-proxy/echo"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/app"
	"reading-tracker/backend/internal/platform/config"
)

func main() {
	log := app.Logger()
	cfg, err := config.Load(config.RoleAPI, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	awsCfg, err := app.AWSConfig(context.Background())
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	st := dynamo.New(app.DynamoDB(awsCfg), cfg.TableName)
	q := &queue.SQS{API: app.SQS(awsCfg), QueueURL: cfg.QueueURL}
	e := app.API(cfg, st, q, log)
	lambda.Start(echoadapter.NewV2(e).ProxyWithContext)
}
