// Package awswire is the AWS half of the composition root: the AWS clients and the
// SQS consumer around wire's job handler. Only cmd/* (and integration tests) import it.
package awswire

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/wire"
)

func AWSConfig(ctx context.Context) (aws.Config, error) { return awsconfig.LoadDefaultConfig(ctx) }

// DynamoDB honours DYNAMODB_ENDPOINT (cmd/local → DynamoDB Local).
func DynamoDB(cfg aws.Config) *dynamodb.Client {
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if ep := os.Getenv("DYNAMODB_ENDPOINT"); ep != "" {
			o.BaseEndpoint = aws.String(ep)
		}
	})
}

func SQS(cfg aws.Config) *sqs.Client { return sqs.NewFromConfig(cfg) }

// WorkerDeps are the Notion sync's collaborators plus the SQS visibility hook.
type WorkerDeps struct {
	SyncDeps   wire.SyncDeps
	Visibility queue.VisibilityChanger
}

// NewWorker puts the Notion sync behind the SQS consumer.
func NewWorker(d WorkerDeps) *queue.Consumer {
	return &queue.Consumer{Handler: wire.NewJobHandler(d.SyncDeps), Visibility: d.Visibility, Log: d.SyncDeps.Log}
}

// Worker is the production notion-writer: paced Notion client (C22).
func Worker(cfg *config.Config, st wire.Store, q queue.Sender, vis queue.VisibilityChanger, log *slog.Logger) *queue.Consumer {
	return &queue.Consumer{Handler: wire.JobHandler(cfg, st, q, log), Visibility: vis, Log: log}
}
