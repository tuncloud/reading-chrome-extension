package syncsvc

import (
	"context"
	"errors"
	"strings"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/queue"
)

// ErrAllThrottled means every valid segment hit DynamoDB throttling → 503.
var ErrAllThrottled = errors.New("syncsvc: all segments throttled")

// Pusher applies one segment exactly once (*dynamo.Store).
type Pusher interface {
	PushSegment(ctx context.Context, w domain.SegmentWrite) (domain.PushOutcome, error)
}

type Service struct {
	Store  Pusher
	Queue  queue.Sender
	Bucket time.Duration
	Now    func() time.Time
}

// Push validates, writes each segment in its own transaction (C25), and enqueues
// one article/day message per touched key.
func (s *Service) Push(ctx context.Context, installationID, deviceID string, req PushRequest) (*PushResponse, error) {
	loc, err := checkEnvelope(&req)
	if err != nil {
		return nil, err
	}
	now := s.Now()

	articles := map[string]domain.ArticleMeta{}
	invalid := map[string]bool{}
	for _, a := range req.Articles {
		key := strings.TrimSpace(a.CanonicalURL)
		meta, ok := normalizeArticle(a)
		if !ok {
			if _, have := articles[key]; !have {
				invalid[key] = true
			}
			continue
		}
		if _, dup := articles[key]; !dup {
			articles[key] = meta
			delete(invalid, key)
		}
	}

	resp := newResponse()
	var writes []domain.SegmentWrite
	for _, seg := range req.Segments {
		start, end, reason := validateSegment(seg, now)
		key := strings.TrimSpace(seg.CanonicalURL)
		meta, known := articles[key]
		if reason == "" && !known {
			reason = ReasonUnknownArticle
			if invalid[key] {
				reason = ReasonInvalidArticle
			}
		}
		if reason != "" {
			resp.Rejected = append(resp.Rejected, Rejection{ID: seg.ID, Reason: reason})
			continue
		}
		writes = append(writes, domain.SegmentWrite{
			InstallationID: installationID, DeviceID: deviceID, SegmentID: seg.ID, SessionID: seg.SessionID, Seq: seg.Seq,
			URLHash: domain.URLHash(meta.CanonicalURL), Date: start.In(loc).Format(time.DateOnly),
			StartedAt: start, EndedAt: end, ActiveSeconds: seg.ActiveSeconds, MaxProgress: seg.MaxProgress, Article: meta,
		})
	}

	var touched touchedKeys
	for _, w := range writes {
		out, err := s.Store.PushSegment(ctx, w)
		if err != nil {
			return nil, err
		}
		switch out {
		case domain.PushAccepted:
			resp.Accepted = append(resp.Accepted, w.SegmentID)
			touched.add(w)
		case domain.PushDuplicate:
			// Re-enqueue: the push that committed it may have failed to enqueue (interpretation 3).
			resp.Duplicate = append(resp.Duplicate, w.SegmentID)
			touched.add(w)
		case domain.PushRetry:
			resp.Retry = append(resp.Retry, w.SegmentID)
		}
	}
	if len(writes) > 0 && len(resp.Retry) == len(writes) {
		return nil, ErrAllThrottled
	}
	if msgs := touched.messages(installationID); len(msgs) > 0 {
		if err := s.Queue.Send(ctx, queue.Bucketed(now, s.Bucket, msgs...)...); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// touchedKeys collects distinct article hashes and dates in first-seen order.
type touchedKeys struct {
	hashes, dates []string
	seen          map[string]bool
}

func (t *touchedKeys) add(w domain.SegmentWrite) {
	if t.seen == nil {
		t.seen = map[string]bool{}
	}
	if k := "a#" + w.URLHash; !t.seen[k] {
		t.seen[k] = true
		t.hashes = append(t.hashes, w.URLHash)
	}
	if k := "d#" + w.Date; !t.seen[k] {
		t.seen[k] = true
		t.dates = append(t.dates, w.Date)
	}
}

// messages lists article messages before day messages, so a day page can relate
// to article pages created in the same batch.
func (t *touchedKeys) messages(inst string) []queue.Message {
	msgs := make([]queue.Message, 0, len(t.hashes)+len(t.dates))
	for _, h := range t.hashes {
		msgs = append(msgs, queue.Article(inst, h))
	}
	for _, d := range t.dates {
		msgs = append(msgs, queue.Day(inst, d))
	}
	return msgs
}
