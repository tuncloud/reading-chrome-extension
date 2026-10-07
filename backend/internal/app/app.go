// Package app wires production dependencies for the cmd binaries.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"reading-tracker/backend/internal/config"
	"reading-tracker/backend/internal/httpapi"
	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

// Logger writes JSON logs to stdout (CloudWatch).
func Logger() *slog.Logger { return slog.New(slog.NewJSONHandler(os.Stdout, nil)) }

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

func NotionClient(cfg *config.Config, pacer notion.Pacer) *notion.Client {
	return &notion.Client{BaseURL: cfg.NotionAPIBase, Version: cfg.NotionVersion, ClientID: cfg.NotionClientID,
		ClientSecret: cfg.NotionClientSecret, HTTP: &http.Client{Timeout: 15 * time.Second}, Pacer: pacer}
}

// APIDeps assembles the api Lambda's dependencies. The API never paces or refreshes (C3, C8).
func APIDeps(cfg *config.Config, st *store.Store, q queue.Sender, log *slog.Logger) httpapi.Deps {
	return httpapi.Deps{Config: cfg, Store: st, Notion: NotionClient(cfg, nil), Queue: q, Now: time.Now, Log: log}
}
