package readsync_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/readsync"
)

var (
	ctx = context.Background()
	now = time.Date(2026, 10, 6, 3, 12, 0, 0, time.UTC)
)

const (
	artURL = "https://medium.com/p/8f3c2a1b9d4e"
	bot    = "bot-1"
)

// fakePusher remembers committed segment ids, like the real conditional put.
type fakePusher struct {
	seen     map[string]bool
	throttle map[string]bool
	got      []domain.SegmentWrite
}

func (f *fakePusher) PushSegment(_ context.Context, w domain.SegmentWrite) (domain.PushOutcome, error) {
	f.got = append(f.got, w)
	if f.throttle[w.SegmentID] {
		return domain.PushRetry, nil
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[w.SegmentID] {
		return domain.PushDuplicate, nil
	}
	f.seen[w.SegmentID] = true
	return domain.PushAccepted, nil
}

func newService() (*readsync.Service, *fakePusher, *queue.Recorder) {
	p, rec := &fakePusher{throttle: map[string]bool{}}, &queue.Recorder{}
	q := &queue.SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return now }}
	return &readsync.Service{Store: p, Queue: q, Now: q.Now}, p, rec
}

func article(u string) readsync.Article {
	return readsync.Article{CanonicalURL: u, Source: "medium", Title: "Designing Idempotent APIs", Author: "Jane Doe",
		SiteName: "Better Programming", WordCount: 1820, PublishedAt: "2026-09-30"}
}

func seg(id, u string, started time.Time, secs int) readsync.Segment {
	return readsync.Segment{ID: id, SessionID: "c3a9", Seq: 0, CanonicalURL: u, StartedAt: started.Format(time.RFC3339),
		EndedAt: started.Add(time.Duration(secs+5) * time.Second).Format(time.RFC3339), ActiveSeconds: secs, MaxProgress: 0.42}
}

func id(n int) string {
	return "6f1d2c9e-6a43-4b1e-9a0e-2f7c1d5b8e" + string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestPushClassifiesOutcomes(t *testing.T) {
	svc, p, _ := newService()
	p.seen = map[string]bool{id(2): true}
	p.throttle[id(3)] = true
	bad := seg(id(4), artURL, now.Add(-time.Hour), 60)
	bad.ActiveSeconds = 4000
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{article(artURL)}, Segments: []readsync.Segment{
		seg(id(1), artURL, now.Add(-time.Hour), 58), seg(id(2), artURL, now.Add(-time.Hour), 58), seg(id(3), artURL, now.Add(-time.Hour), 58), bad,
	}}
	resp, err := svc.Push(ctx, bot, "dev-1", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Accepted) != 1 || resp.Accepted[0] != id(1) || len(resp.Duplicate) != 1 || resp.Duplicate[0] != id(2) ||
		len(resp.Retry) != 1 || resp.Retry[0] != id(3) || len(resp.Rejected) != 1 || resp.Rejected[0].Reason != readsync.ReasonActiveSecondsRange {
		t.Fatalf("resp %+v", resp)
	}
}

// T16 AC: one article + one day message per distinct key.
func TestPushEnqueuesOncePerKey(t *testing.T) {
	svc, _, rec := newService()
	other := "https://example.com/post"
	start := now.Add(-time.Hour)
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{article(artURL), article(other)}, Segments: []readsync.Segment{
		seg(id(1), artURL, start, 60), seg(id(2), artURL, start.Add(time.Minute), 60), seg(id(3), artURL, start.Add(2*time.Minute), 60), seg(id(4), other, start, 60),
	}}
	if _, err := svc.Push(ctx, bot, "dev-1", req); err != nil {
		t.Fatal(err)
	}
	got := rec.Messages()
	want := []queue.Message{queue.Article(bot, domain.URLHash(artURL)), queue.Article(bot, domain.URLHash(other)), queue.Day(bot, "2026-10-06")}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// T16 AC / scenario 16: date is computed in the request tz.
func TestPushDateUsesRequestTZ(t *testing.T) {
	svc, p, rec := newService()
	started := time.Date(2026, 10, 5, 18, 30, 0, 0, time.UTC)
	later := func() time.Time { return started.Add(time.Hour) }
	svc.Now = later
	svc.Queue.(*queue.SyncQueue).Now = later
	req := readsync.PushRequest{TZ: "Asia/Ho_Chi_Minh", Articles: []readsync.Article{article(artURL)}, Segments: []readsync.Segment{seg(id(1), artURL, started, 60)}}
	if _, err := svc.Push(ctx, bot, "dev-1", req); err != nil {
		t.Fatal(err)
	}
	if p.got[0].Date != "2026-10-06" {
		t.Fatalf("date %q", p.got[0].Date)
	}
	if msgs := rec.Messages(); msgs[len(msgs)-1] != queue.Day(bot, "2026-10-06") {
		t.Fatalf("day message %+v", msgs)
	}
}

// Scenario 15: each invalid segment is rejected with a reason; the request still succeeds.
func TestPushValidationReasons(t *testing.T) {
	svc, p, _ := newService()
	ok := func(n int) readsync.Segment { return seg(id(n), artURL, now.Add(-time.Hour), 60) }
	cases := map[string]func(*readsync.Segment){
		readsync.ReasonInvalidID:           func(s *readsync.Segment) { s.ID = "not-a-uuid" },
		readsync.ReasonInvalidSession:      func(s *readsync.Segment) { s.SessionID = "" },
		readsync.ReasonInvalidSeq:          func(s *readsync.Segment) { s.Seq = -1 },
		readsync.ReasonInvalidTimestamp:    func(s *readsync.Segment) { s.StartedAt = "yesterday" },
		readsync.ReasonActiveSecondsRange:  func(s *readsync.Segment) { s.ActiveSeconds = 0 },
		readsync.ReasonEndedBeforeStarted:  func(s *readsync.Segment) { s.EndedAt = now.Add(-2 * time.Hour).Format(time.RFC3339) },
		readsync.ReasonActiveExceedsWindow: func(s *readsync.Segment) { s.ActiveSeconds = 600 },
		readsync.ReasonProgressRange:       func(s *readsync.Segment) { s.MaxProgress = 1.5 },
		readsync.ReasonStartedAtRange:      func(s *readsync.Segment) { *s = seg(s.ID, artURL, now.Add(-40*24*time.Hour), 60) },
		readsync.ReasonUnknownArticle:      func(s *readsync.Segment) { s.CanonicalURL = "https://not-in-articles.example/" },
		readsync.ReasonInvalidArticle:      func(s *readsync.Segment) { s.CanonicalURL = "ftp://bad.example/x" },
	}
	var segs []readsync.Segment
	n := 10
	for _, mutate := range cases {
		s := ok(n)
		mutate(&s)
		segs = append(segs, s)
		n++
	}
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{article(artURL), {CanonicalURL: "ftp://bad.example/x", Source: "x"}}, Segments: segs}
	resp, err := svc.Push(ctx, bot, "dev-1", req)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range resp.Rejected {
		got[r.Reason] = true
	}
	for reason := range cases {
		if !got[reason] {
			t.Errorf("no segment rejected with %s (got %+v)", reason, resp.Rejected)
		}
	}
	if len(p.got) != 0 {
		t.Fatalf("invalid segments reached the store: %d", len(p.got))
	}
}

func TestPushAllThrottled(t *testing.T) {
	svc, p, _ := newService()
	p.throttle[id(1)] = true
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{article(artURL)}, Segments: []readsync.Segment{seg(id(1), artURL, now.Add(-time.Hour), 60)}}
	if _, err := svc.Push(ctx, bot, "dev-1", req); !errors.Is(err, readsync.ErrAllThrottled) {
		t.Fatalf("err %v", err)
	}
}

func TestPushEnvelopeErrors(t *testing.T) {
	svc, _, _ := newService()
	many := make([]readsync.Segment, 51)
	_, err := svc.Push(ctx, bot, "dev-1", readsync.PushRequest{TZ: "UTC", Segments: many})
	var env *readsync.EnvelopeError
	if !errors.As(err, &env) || !env.TooLarge {
		t.Fatalf("51 segments: %v", err)
	}
	for _, tz := range []string{"", "Mars/Olympus", "Local"} {
		_, err = svc.Push(ctx, bot, "dev-1", readsync.PushRequest{TZ: tz})
		if !errors.As(err, &env) || env.TooLarge {
			t.Fatalf("tz %q: %v", tz, err)
		}
	}
}

// Interpretation 2: long titles are truncated, not rejected.
func TestPushTruncatesLongTitle(t *testing.T) {
	svc, p, _ := newService()
	a := article(artURL)
	a.Title = strings.Repeat("Đ", 600)
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{a}, Segments: []readsync.Segment{seg(id(1), artURL, now.Add(-time.Hour), 60)}}
	resp, err := svc.Push(ctx, bot, "dev-1", req)
	if err != nil || len(resp.Accepted) != 1 {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if n := len([]rune(p.got[0].Article.Title)); n != 500 {
		t.Fatalf("title has %d runes", n)
	}
}

// Stored times are UTC so DynamoDB string comparisons order correctly.
func TestPushNormalizesOffsetTimestamps(t *testing.T) {
	svc, p, _ := newService()
	s := seg(id(1), artURL, now.Add(-time.Hour), 60)
	s.StartedAt = now.Add(-time.Hour).In(time.FixedZone("ICT", 7*3600)).Format(time.RFC3339)
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{article(artURL)}, Segments: []readsync.Segment{s}}
	if _, err := svc.Push(ctx, bot, "dev-1", req); err != nil {
		t.Fatal(err)
	}
	if got := domain.Timestamp(p.got[0].StartedAt); got != domain.Timestamp(now.Add(-time.Hour)) {
		t.Fatalf("started %s", got)
	}
}

// Review focus 2: enqueue fails after commit; the client's retry sees duplicates and must still enqueue.
func TestPushRetryAfterEnqueueFailureStillEnqueues(t *testing.T) {
	svc, _, rec := newService()
	req := readsync.PushRequest{TZ: "UTC", Articles: []readsync.Article{article(artURL)}, Segments: []readsync.Segment{seg(id(1), artURL, now.Add(-time.Hour), 60)}}
	rec.Err = errors.New("sqs unavailable")
	if _, err := svc.Push(ctx, bot, "dev-1", req); err == nil {
		t.Fatal("enqueue failure must fail the request so the client retries")
	}
	rec.Err = nil
	resp, err := svc.Push(ctx, bot, "dev-1", req)
	if err != nil || len(resp.Duplicate) != 1 {
		t.Fatalf("retry resp %+v err %v", resp, err)
	}
	if len(rec.Messages()) != 2 {
		t.Fatalf("retry enqueued %+v", rec.Messages())
	}
}
