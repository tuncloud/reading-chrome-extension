package port

import (
	"context"
	"fmt"
	"time"
)

type JobType string

const (
	JobArticle JobType = "article"
	JobDay     JobType = "day"
	JobPurge   JobType = "purge"
)

// Job is one unit of Notion-sync work (spec §2.12).
type Job struct {
	Type           JobType
	InstallationID string
	URLHash        string
	Date           string
	UserID         string
}

// SyncQueue enqueues Notion-sync work. Every job of an installation is processed in
// order, one at a time (C4).
type SyncQueue interface {
	// EnqueueSync debounces: jobs for the same key within one bucket collapse (C5).
	EnqueueSync(ctx context.Context, inst string, hashes, dates []string) error
	// EnqueueResync is never collapsed with pushes, so a backlog sent right after a push
	// in the same bucket is still delivered (R2). Writes are absolute, so extra runs are harmless.
	EnqueueResync(ctx context.Context, inst string, hashes, dates []string) error
	// Requeue re-delivers one job (e.g. after its page was cleared).
	Requeue(ctx context.Context, job Job) error
	EnqueuePurge(ctx context.Context, uid, inst string) error
}

// JobHandler processes one job. nil acks it; *RetryAfterError backs it off; any other
// error fails it for redelivery.
type JobHandler interface {
	HandleJob(ctx context.Context, job Job) error
}

// RetryAfterError asks the queue to hide the job for Delay before redelivery (spec §2.15).
type RetryAfterError struct {
	Delay time.Duration
	Err   error
}

func (e *RetryAfterError) Error() string { return fmt.Sprintf("retry after %s: %v", e.Delay, e.Err) }
func (e *RetryAfterError) Unwrap() error { return e.Err }
