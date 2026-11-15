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
	m := Article("u1", hash)
	a := m.DedupID(start.Add(5*time.Second), bucket)
	b := m.DedupID(start.Add(119*time.Second), bucket)
	c := m.DedupID(start.Add(120*time.Second), bucket)
	if a != b {
		t.Fatalf("same bucket differs: %q %q", a, b)
	}
	if a == c {
		t.Fatal("next bucket must differ")
	}
	if a != "article#u1#"+hash+"#1000" {
		t.Fatalf("format: %q", a)
	}
	if d := Day("u1", "2026-10-06").DedupID(start, bucket); d != "day#u1#2026-10-06#1000" {
		t.Fatalf("day format: %q", d)
	}
}

// Review focus 3: a worker re-enqueue inside the same bucket must not be deduplicated away.
func TestRequeueDedupIDNeverCollides(t *testing.T) {
	now := time.Unix(1000*120+5, 0)
	m := Article("u1", hash)
	r := m.RequeueDedupID(now)
	if r == m.DedupID(now, 120*time.Second) || len(r) > 128 || r == m.RequeueDedupID(now.Add(time.Nanosecond)) {
		t.Fatalf("requeue id %q collides or is too long", r)
	}
}

func TestGroupIDs(t *testing.T) {
	for name, m := range map[string]Message{
		"article":        Article("u1", hash),
		"day":            Day("u1", "2026-10-06"),
		"purge":          Purge("u1", bot),
		"purge, no inst": Purge("u1", ""),
	} {
		if g := m.GroupID(); g != "user#u1" {
			t.Fatalf("%s group %q", name, g)
		}
	}
	if id := Purge("u1", bot).DedupID(time.Unix(42, 0), time.Minute); id != "purge#u1#42" {
		t.Fatalf("purge dedup %q", id)
	}
}

func TestMessageJSONShape(t *testing.T) {
	for want, m := range map[string]Message{
		`{"type":"article","user_id":"u1","url_hash":"h1"}`:         Article("u1", "h1"),
		`{"type":"day","user_id":"u1","date":"2026-10-06"}`:         Day("u1", "2026-10-06"),
		`{"type":"purge","installation_id":"bot-1","user_id":"u1"}`: Purge("u1", "bot-1"),
	} {
		b, _ := json.Marshal(m)
		if string(b) != want {
			t.Fatalf("json %s, want %s", b, want)
		}
	}
}

func TestRecorder(t *testing.T) {
	r := &Recorder{}
	r.Send(context.Background(), Bucketed(time.Unix(0, 0), time.Minute, Article("u1", hash), Day("u1", "2026-10-06"))...)
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
	if err := q.EnqueueSync(context.Background(), "u1", []string{hash}, []string{"2026-10-06"}); err != nil {
		t.Fatal(err)
	}
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Article("u1", hash) || sent[1].Message != Day("u1", "2026-10-06") ||
		sent[0].DedupID != "article#u1#"+hash+"#1000" {
		t.Fatalf("sent %+v", sent)
	}
	if err := q.EnqueueSync(context.Background(), "u1", nil, nil); err != nil || len(rec.Drain()) != 0 {
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
	must(q.EnqueueSync(ctx, "u1", []string{hash}, []string{"2026-10-06"}))
	must(q.EnqueueResync(ctx, "u1", []string{hash}, []string{"2026-10-06"}))
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
	if err := q.Requeue(context.Background(), port.Job{Type: port.JobDay, UserID: "u1", Date: "2026-10-06"}); err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueuePurge(context.Background(), "u1", bot); err != nil {
		t.Fatal(err)
	}
	sent := rec.Drain()
	if len(sent) != 2 || sent[0].Message != Day("u1", "2026-10-06") || !strings.Contains(sent[0].DedupID, "#r") ||
		sent[1].Message != Purge("u1", bot) || sent[1].DedupID != "purge#u1#42" {
		t.Fatalf("sent %+v", sent)
	}
}

func TestJobCarriesEveryField(t *testing.T) {
	m := Message{Type: TypePurge, InstallationID: "bot-1", URLHash: "h1", Date: "2026-10-06", UserID: "u1"}
	want := port.Job{Type: port.JobPurge, InstallationID: "bot-1", URLHash: "h1", Date: "2026-10-06", UserID: "u1"}
	if got := m.Job(); got != want {
		t.Fatalf("Job() = %+v, want %+v", got, want)
	}
}
