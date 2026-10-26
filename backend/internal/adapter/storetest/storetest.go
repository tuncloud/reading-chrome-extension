// Package storetest is the store-agnostic conformance suite: every store adapter runs
// RunIdentity and RunReading, and each check gets a fresh store.
package storetest

import (
	"context"
	"testing"
	"time"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// IdentityStore is everything the identity checks exercise.
type IdentityStore interface {
	port.Users
	port.Devices
	port.OAuthGrants
	port.Installations
}

// ReadingStore is a full store: identity plus reading and purge.
type ReadingStore interface {
	IdentityStore
	port.Reading
	port.DataPurger
}

// RunIdentity runs every identity check as a subtest, each on its own newStore(t).
func RunIdentity(t *testing.T, newStore func(t *testing.T) IdentityStore) {
	for _, c := range identityChecks {
		t.Run(c.name, func(t *testing.T) { c.run(t, newStore(t)) })
	}
}

// RunReading runs every reading check as a subtest, each on its own newStore(t).
func RunReading(t *testing.T, newStore func(t *testing.T) ReadingStore) {
	for _, c := range readingChecks {
		t.Run(c.name, func(t *testing.T) { c.run(t, newStore(t)) })
	}
}

var ctx = context.Background()

// TestNow is the fixed clock of every check.
var TestNow = time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)

// ArticleURL is the article most checks read.
const ArticleURL = "https://medium.com/p/8f3c2a1b9d4e"

// Must fails the test on a non-nil error.
func Must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Segment builds a bot-1 segment of secs seconds starting at started.
func Segment(id, url string, seq, secs int, progress float64, started time.Time) domain.SegmentWrite {
	return domain.SegmentWrite{
		InstallationID: "bot-1", DeviceID: "dev-1", SegmentID: id, SessionID: "sess-1", Seq: seq,
		URLHash: domain.URLHash(url), Date: started.UTC().Format("2006-01-02"),
		StartedAt: started, EndedAt: started.Add(time.Duration(secs) * time.Second),
		ActiveSeconds: secs, MaxProgress: progress,
		Article: domain.ArticleMeta{CanonicalURL: url, Source: "medium", Title: "Designing Idempotent APIs", WordCount: 1820},
	}
}

// Push pushes w and fails the test on an error.
func Push(t *testing.T, s port.Reading, w domain.SegmentWrite) domain.PushOutcome {
	t.Helper()
	out, err := s.PushSegment(ctx, w)
	Must(t, err)
	return out
}
