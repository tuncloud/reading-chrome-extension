package msg

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reading-tracker/backend/internal/usecase/port"
)

const (
	bot  = "b7c1e0f2-1111-2222-3333-444455556666"
	hash = "0123456789abcdef0123456789abcdef"
)

// T18 AC: same bucket → identical IDs; different bucket → different.
func TestDedupIDBuckets(t *testing.T) {
	bucket := 120 * time.Second
	start := time.Unix(1000*120, 0)
	m := Article(bot, hash)
	a := m.DedupID(start.Add(5*time.Second), bucket)
	b := m.DedupID(start.Add(119*time.Second), bucket)
	c := m.DedupID(start.Add(120*time.Second), bucket)
	if a != b {
		t.Fatalf("same bucket differs: %q %q", a, b)
	}
	if a == c {
		t.Fatal("next bucket must differ")
	}
	if a != "article#"+bot+"#"+hash+"#1000" {
		t.Fatalf("format: %q", a)
	}
	if d := Day(bot, "2026-10-06").DedupID(start, bucket); d != "day#"+bot+"#2026-10-06#1000" {
		t.Fatalf("day format: %q", d)
	}
}

// Review focus 3: a worker re-enqueue inside the same bucket must not be deduplicated away.
func TestRequeueDedupIDNeverCollides(t *testing.T) {
	now := time.Unix(1000*120+5, 0)
	m := Article(bot, hash)
	r := m.RequeueDedupID(now)
	if r == m.DedupID(now, 120*time.Second) || len(r) > 128 || r == m.RequeueDedupID(now.Add(time.Nanosecond)) {
		t.Fatalf("requeue id %q collides or is too long", r)
	}
}

func TestGroupIDs(t *testing.T) {
	if g := Article(bot, hash).GroupID(); g != bot {
		t.Fatalf("article group %q", g)
	}
	if g := Purge("u1", "").GroupID(); g != "user#u1" {
		t.Fatalf("purge group without installation %q", g)
	}
	if g := Purge("u1", bot).GroupID(); g != bot {
		t.Fatalf("purge group with installation %q", g)
	}
	if id := Purge("u1", bot).DedupID(time.Unix(42, 0), time.Minute); id != "purge#u1#42" {
		t.Fatalf("purge dedup %q", id)
	}
}

func TestMessageJSONShape(t *testing.T) {
	b, _ := json.Marshal(Article(bot, hash))
	if string(b) != `{"type":"article","installation_id":"`+bot+`","url_hash":"`+hash+`"}` {
		t.Fatalf("article json %s", b)
	}
	b, _ = json.Marshal(Purge("u1", ""))
	if string(b) != `{"type":"purge","installation_id":"","user_id":"u1"}` {
		t.Fatalf("purge json %s", b)
	}
}

func TestRecorder(t *testing.T) {
	r := &Recorder{}
	r.Send(context.Background(), Bucketed(time.Unix(0, 0), time.Minute, Article(bot, hash), Day(bot, "2026-10-06"))...)
	if got := r.Messages(); len(got) != 2 || got[1].Type != TypeDay {
		t.Fatalf("got %+v", got)
	}
	if d := r.Drain(); len(d) != 2 || len(r.Messages()) != 0 {
		t.Fatal("Drain must empty the recorder")
	}
}

func newSyncQueue(rec *Recorder, now time.Time) *SyncQueue {
	return &SyncQueue{Sender: rec, Bucket: 120 * time.Second, Now: func() time.Time { return now }}
}

func TestEnqueueSyncDebouncesArticlesThenDays(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(1000*120+5, 0))
	if err := q.EnqueueSync(context.Background(), bot, []string{hash}, []string{"2026-10-06"}); err != nil {
		t.Fatal(err)
	}
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Article(bot, hash) || sent[1].Message != Day(bot, "2026-10-06") ||
		sent[0].DedupID != "article#"+bot+"#"+hash+"#1000" {
		t.Fatalf("sent %+v", sent)
	}
	if err := q.EnqueueSync(context.Background(), bot, nil, nil); err != nil || len(rec.Drain()) != 0 {
		t.Fatal("nothing to send must send nothing")
	}
}

// Review focus 5 (adapter half) / R2: resync ids never equal a push's bucket ids.
func TestResyncIDsNeverCollideWithPushIDs(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(1000*120+5, 0))
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(q.EnqueueSync(ctx, bot, []string{hash}, []string{"2026-10-06"}))
	must(q.EnqueueResync(ctx, bot, []string{hash}, []string{"2026-10-06"}))
	sent := rec.Drain()
	if len(sent) != 4 {
		t.Fatalf("sent %d", len(sent))
	}
	seen := map[string]bool{}
	for _, o := range sent {
		if seen[o.DedupID] {
			t.Fatalf("duplicate dedup id %q", o.DedupID)
		}
		seen[o.DedupID] = true
		if len(o.DedupID) > 128 {
			t.Fatalf("dedup id too long: %q", o.DedupID)
		}
	}
}

func TestRequeueAndPurge(t *testing.T) {
	rec := &Recorder{}
	q := newSyncQueue(rec, time.Unix(42, 0))
	if err := q.Requeue(context.Background(), port.Job{Type: port.JobDay, InstallationID: bot, Date: "2026-10-06"}); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueuePurge(context.Background(), "u1", bot); err != nil {
		t.Fatal(err)
	}
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Day(bot, "2026-10-06") || !strings.Contains(sent[0].DedupID, "#r") ||
		sent[1].Message != Purge("u1", bot) || sent[1].DedupID != "purge#u1#42" {
		t.Fatalf("sent %+v", sent)
	}
}
