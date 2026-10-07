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

	"reading-tracker/backend/internal/adapter/dynamo"
	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/httpapi"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/usecase/notionsync"
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
func APIDeps(cfg *config.Config, st *dynamo.Store, q queue.Sender, log *slog.Logger) httpapi.Deps {
	return httpapi.Deps{Config: cfg, Store: st, Notion: NotionClient(cfg, nil), Queue: q, Now: time.Now, Log: log}
}

// WorkerDeps are the notion-writer's collaborators.
type WorkerDeps struct {
	Store      *dynamo.Store
	Notion     *notion.Client
	Keys       *crypto.Keyring
	Queue      queue.Sender
	Visibility queue.VisibilityChanger
	Tracking   config.Tracking
	Now        func() time.Time
	Jitter     func() time.Duration
	Sleep      func(time.Duration)
	Log        *slog.Logger
}

// NewWorker assembles the Notion sync behind the SQS consumer.
func NewWorker(d WorkerDeps) *queue.Consumer {
	gw := &notion.Gateway{Client: d.Notion}
	svc := &notionsync.Service{
		Installations: d.Store, Reading: d.Store, Users: d.Store, Devices: d.Store, Purger: d.Store,
		Pages: gw, Auth: gw, Sealer: d.Keys,
		Queue: &queue.SyncQueue{Sender: d.Queue, Now: d.Now},
		Policy: domain.SyncPolicy{MinCreateSeconds: d.Tracking.MinCreateSeconds, CompleteProgress: d.Tracking.CompleteProgress,
			CompleteTimeRatio: d.Tracking.CompleteTimeRatio, WordsPerMinute: d.Tracking.WordsPerMinute},
		Now: d.Now, Jitter: d.Jitter, Sleep: d.Sleep, Log: d.Log,
	}
	return &queue.Consumer{Handler: svc, Visibility: d.Visibility, Log: d.Log}
}

// Worker is the production notion-writer: paced Notion client (C22).
func Worker(cfg *config.Config, st *dynamo.Store, q queue.Sender, vis queue.VisibilityChanger, log *slog.Logger) *queue.Consumer {
	return NewWorker(WorkerDeps{Store: st, Notion: NotionClient(cfg, notion.NewMinInterval(350*time.Millisecond)),
		Keys: cfg.Keys, Queue: q, Visibility: vis, Tracking: cfg.Public.Tracking, Now: time.Now, Log: log})
}
