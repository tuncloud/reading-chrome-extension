package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/notion/notiontest"
	"reading-tracker/backend/internal/platform/config"
	"reading-tracker/backend/internal/platform/crypto"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
	"reading-tracker/backend/internal/store/storetest"
)

var (
	ctx     = context.Background()
	testNow = time.Date(2026, 10, 6, 3, 12, 0, 0, time.UTC)
)

const (
	testBot = "b7c1e0f2-0000-4000-8000-000000000001"
	artURL  = "https://medium.com/p/8f3c2a1b9d4e"
)

type fakeVisibility struct{ calls []time.Duration }

func (f *fakeVisibility) ChangeVisibility(_ context.Context, _ string, d time.Duration) error {
	f.calls = append(f.calls, d)
	return nil
}

type wenv struct {
	t      *testing.T
	w      *Worker
	db     *storetest.Env
	ns     *notiontest.Server
	q      *queue.Recorder
	vis    *fakeVisibility
	tpl    notiontest.Template
	keys   *crypto.Keyring
	now    time.Time
	logs   *bytes.Buffer
	sleeps []time.Duration
	segs   int
}

func b64(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newEnv(t *testing.T) *wenv { return newEnvWithKeys(t, "v1:"+b64(1)) }

func newEnvWithKeys(t *testing.T, keys string) *wenv {
	t.Helper()
	kr, err := crypto.ParseKeyring(keys)
	must(t, err)
	pub, err := config.LoadPublic("")
	must(t, err)
	e := &wenv{t: t, db: storetest.New(t), ns: notiontest.New(t), q: &queue.Recorder{}, vis: &fakeVisibility{},
		keys: kr, now: testNow, logs: &bytes.Buffer{}}
	e.tpl = e.ns.AddTemplate("tpl-1")
	e.w = New(Deps{
		Store:      e.db.Store,
		Notion:     &notion.Client{BaseURL: e.ns.URL, Version: "2025-09-03", ClientID: "cid", ClientSecret: "csecret"},
		Keys:       kr,
		Queue:      e.q,
		Visibility: e.vis,
		Tracking:   pub.Tracking,
		Now:        func() time.Time { return e.now },
		Jitter:     func() time.Duration { return 2 * time.Second },
		Sleep:      func(d time.Duration) { e.sleeps = append(e.sleeps, d) },
		Log:        slog.New(slog.NewTextHandler(e.logs, nil)),
	})
	return e
}

func (e *wenv) seedInstallation(access, refresh string) *store.Installation {
	return e.seedInstallationWith(e.keys, access, refresh)
}

// seedInstallationWith stores an active installation whose tokens are sealed by kr.
func (e *wenv) seedInstallationWith(kr *crypto.Keyring, access, refresh string) *store.Installation {
	e.t.Helper()
	a, err := kr.Seal([]byte(access), []byte(testBot))
	must(e.t, err)
	r := ""
	if refresh != "" {
		r, err = kr.Seal([]byte(refresh), []byte(testBot))
		must(e.t, err)
	}
	inst := &store.Installation{UserID: "u1", WorkspaceName: "WS", Status: store.StatusActive, AccessTokenEnc: a,
		RefreshTokenEnc: r, TokenVersion: 1, CreatedAt: store.Timestamp(e.now), UpdatedAt: store.Timestamp(e.now)}
	inst.ApplyDatabases(e.tpl.DBConfig())
	must(e.t, e.db.Store.PutInstallation(ctx, testBot, inst))
	return inst
}

func (e *wenv) read(url string, seq, secs int, progress float64) {
	e.readMeta(store.ArticleMeta{CanonicalURL: url, Source: "medium", Title: "Designing Idempotent APIs", Author: "Jane Doe",
		SiteName: "Better Programming", WordCount: 1000}, seq, secs, progress)
}

// readMeta records one new segment of secs active seconds starting at e.now, then advances e.now.
func (e *wenv) readMeta(meta store.ArticleMeta, seq, secs int, progress float64) {
	e.t.Helper()
	e.segs++
	_, err := e.db.Store.PushSegment(ctx, store.SegmentWrite{InstallationID: testBot, DeviceID: "d1",
		SegmentID: fmt.Sprintf("00000000-0000-4000-8000-%012d", e.segs), SessionID: "s1", Seq: seq,
		URLHash: store.URLHash(meta.CanonicalURL), Date: "2026-10-06", StartedAt: e.now,
		EndedAt: e.now.Add(time.Duration(secs) * time.Second), ActiveSeconds: secs, MaxProgress: progress, Article: meta})
	must(e.t, err)
	e.now = e.now.Add(time.Duration(secs) * time.Second)
}

// handle runs the messages as one SQS batch and returns the reported failures.
func (e *wenv) handle(msgs ...queue.Message) []events.SQSBatchItemFailure {
	e.t.Helper()
	var ev events.SQSEvent
	for i, m := range msgs {
		body, err := json.Marshal(m)
		must(e.t, err)
		ev.Records = append(ev.Records, events.SQSMessage{MessageId: fmt.Sprintf("m%d", i), ReceiptHandle: fmt.Sprintf("rh%d", i), Body: string(body)})
	}
	resp, err := e.w.HandleSQS(ctx, ev)
	must(e.t, err)
	return resp.BatchItemFailures
}

func pid(name string) string { return notiontest.PropID(name) }

// num reads a number property of a fake page by column name.
func num(pg *notiontest.Page, name string) float64 {
	v, _ := pg.Properties[pid(name)].(map[string]any)
	f, _ := v["number"].(float64)
	return f
}
