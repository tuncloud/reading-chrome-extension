// Command api is the api Lambda: Echo behind a Function URL (payload v2).
package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	echoadapter "github.com/awslabs/aws-lambda-go-api-proxy/echo"

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/wire"
)

func main() {
	log := wire.Logger()
	cfg, err := config.Load(config.RoleAPI, os.Getenv)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	awsCfg, err := wire.AWSConfig(context.Background())
	if err != nil {
		log.Error("aws config", "err", err)
		os.Exit(1)
	}
	st := dynamo.New(wire.DynamoDB(awsCfg), cfg.TableName)
	q := &queue.SQS{API: wire.SQS(awsCfg), QueueURL: cfg.QueueURL}
	e := wire.API(cfg, st, q, log)
	lambda.Start(echoadapter.NewV2(e).ProxyWithContext)
}
