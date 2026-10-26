package msg

import (
	"context"
	"time"

	"reading-tracker/backend/internal/usecase/port"
)

// SyncQueue implements port.SyncQueue on any Sender (SQS, Local or Recorder) and owns
// the dedup-id policy: bucketed for pushes (C5), unique for resync and requeue (R2).
type SyncQueue struct {
	Sender Sender
	Bucket time.Duration
	Now    func() time.Time
}

var _ port.SyncQueue = (*SyncQueue)(nil)

func (q *SyncQueue) EnqueueSync(ctx context.Context, inst string, hashes, dates []string) error {
	msgs := syncMessages(inst, hashes, dates)
	if len(msgs) == 0 {
		return nil
	}
	return q.Sender.Send(ctx, Bucketed(q.Now(), q.Bucket, msgs...)...)
}

func (q *SyncQueue) EnqueueResync(ctx context.Context, inst string, hashes, dates []string) error {
	msgs := syncMessages(inst, hashes, dates)
	if len(msgs) == 0 {
		return nil
	}
	now := q.Now()
	out := make([]Outgoing, len(msgs))
	for i, m := range msgs {
		out[i] = Outgoing{Message: m, DedupID: m.RequeueDedupID(now)}
	}
	return q.Sender.Send(ctx, out...)
}

func (q *SyncQueue) Requeue(ctx context.Context, job port.Job) error {
	m := messageFor(job)
	return q.Sender.Send(ctx, Outgoing{Message: m, DedupID: m.RequeueDedupID(q.Now())})
}

func (q *SyncQueue) EnqueuePurge(ctx context.Context, uid, inst string) error {
	m := Purge(uid, inst)
	return q.Sender.Send(ctx, Outgoing{Message: m, DedupID: m.DedupID(q.Now(), q.Bucket)})
}

// syncMessages lists article messages before day messages, so a day page can relate
// to article pages created from the same batch.
func syncMessages(inst string, hashes, dates []string) []Message {
	msgs := make([]Message, 0, len(hashes)+len(dates))
	for _, h := range hashes {
		msgs = append(msgs, Article(inst, h))
	}
	for _, d := range dates {
		msgs = append(msgs, Day(inst, d))
	}
	return msgs
}

func messageFor(j port.Job) Message {
	return Message{Type: Type(j.Type), InstallationID: j.InstallationID, URLHash: j.URLHash, Date: j.Date, UserID: j.UserID}
}

func (m Message) Job() port.Job {
	return port.Job{Type: port.JobType(m.Type), InstallationID: m.InstallationID, URLHash: m.URLHash, Date: m.Date, UserID: m.UserID}
}
