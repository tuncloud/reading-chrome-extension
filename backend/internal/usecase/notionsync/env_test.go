package notionsync

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/notion/notiontest"
	"reading-tracker/backend/internal/adapter/postgres/postgrestest"
	"reading-tracker/backend/internal/adapter/queue/msg"
	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/platform/crypto"
)

var (
	ctx     = context.Background()
	testNow = time.Date(2026, 10, 6, 3, 12, 0, 0, time.UTC)
)

const (
	testBot  = "b7c1e0f2-0000-4000-8000-000000000001"
	testUser = "u1" // testBot's user, who owns the reading data
	artURL   = "https://medium.com/p/8f3c2a1b9d4e"
)

type wenv struct {
	t      *testing.T
	s      *Service
	gw     *notion.Gateway
	db     *postgrestest.Env
	ns     *notiontest.Server
	q      *msg.Recorder
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
	e := &wenv{t: t, db: postgrestest.New(t), ns: notiontest.New(t), q: &msg.Recorder{},
		keys: kr, now: testNow, logs: &bytes.Buffer{}}
	e.tpl = e.ns.AddTemplate("tpl-1")
	e.gw = &notion.Gateway{Client: &notion.Client{BaseURL: e.ns.URL, Version: "2025-09-03", ClientID: "cid", ClientSecret: "csecret"}}
	clock := func() time.Time { return e.now }
	log := slog.New(slog.NewTextHandler(e.logs, nil))
	e.s = &Service{
		Installations: e.db.Store, Reading: e.db.Store, Users: e.db.Store, Devices: e.db.Store, Purger: e.db.Store,
		Pages: e.gw, Auth: e.gw, Sealer: kr,
		Queue:  &msg.SyncQueue{Sender: e.q, Bucket: 120 * time.Second, Now: clock},
		Policy: domain.SyncPolicy{MinCreateSeconds: 30, CompleteProgress: 0.9, CompleteTimeRatio: 0.3, WordsPerMinute: 230},
		Now:    clock,
		Jitter: func() time.Duration { return 2 * time.Second },
		Sleep:  func(d time.Duration) { e.sleeps = append(e.sleeps, d) },
		Log:    log,
	}
	return e
}

func (e *wenv) seedInstallation(access, refresh string) *domain.Installation {
	return e.seedInstallationWith(e.keys, access, refresh)
}

// seedInstallationWith stores an active installation whose tokens are sealed by kr.
func (e *wenv) seedInstallationWith(kr *crypto.Keyring, access, refresh string) *domain.Installation {
	e.t.Helper()
	a, err := kr.Seal([]byte(access), []byte(testBot))
	must(e.t, err)
	r := ""
	if refresh != "" {
		r, err = kr.Seal([]byte(refresh), []byte(testBot))
		must(e.t, err)
	}
	inst := &domain.Installation{ID: testBot, UserID: testUser, WorkspaceName: "WS", Status: domain.StatusActive, AccessTokenEnc: a,
		RefreshTokenEnc: r, TokenVersion: 1, CreatedAt: domain.Timestamp(e.now), UpdatedAt: domain.Timestamp(e.now)}
	inst.ApplyDatabases(e.tpl.DBConfig())
	must(e.t, e.db.Store.PutInstallation(ctx, testBot, inst))
	e.ensureUser(testUser)
	must(e.t, e.db.Store.SetActiveInstallation(ctx, testUser, testBot))
	return inst
}

// ensureUser creates the user (Notion user "n-<uid>") unless it already exists.
func (e *wenv) ensureUser(uid string) {
	e.t.Helper()
	if _, err := e.db.Store.GetProfile(ctx, uid); err == nil {
		return
	}
	must(e.t, e.db.Store.CreateUser(ctx, uid, domain.User{NotionUserID: "n-" + uid, Name: "T", CreatedAt: domain.Timestamp(e.now)}))
}

func (e *wenv) read(url string, seq, secs int, progress float64) {
	e.readMeta(domain.ArticleMeta{CanonicalURL: url, Source: "medium", Title: "Designing Idempotent APIs", Author: "Jane Doe",
		SiteName: "Better Programming", WordCount: 1000}, seq, secs, progress)
}

// readMeta records one new segment of secs active seconds starting at e.now, then advances e.now.
func (e *wenv) readMeta(meta domain.ArticleMeta, seq, secs int, progress float64) {
	e.t.Helper()
	e.segs++
	_, err := e.db.Store.PushSegment(ctx, domain.SegmentWrite{UserID: testUser, DeviceID: "d1",
		SegmentID: fmt.Sprintf("00000000-0000-4000-8000-%012d", e.segs), SessionID: "s1", Seq: seq,
		URLHash: domain.URLHash(meta.CanonicalURL), Date: "2026-10-06", StartedAt: e.now,
		EndedAt: e.now.Add(time.Duration(secs) * time.Second), ActiveSeconds: secs, MaxProgress: progress, Article: meta})
	must(e.t, err)
	e.now = e.now.Add(time.Duration(secs) * time.Second)
}

type failure struct{ ItemIdentifier string }

// handle runs the messages in order, as the queue consumer did: on the first error it
// reports that message and every later one as failed.
func (e *wenv) handle(msgs ...msg.Message) []failure {
	e.t.Helper()
	for i, m := range msgs {
		err := e.s.HandleJob(ctx, m.Job())
		if err == nil {
			continue
		}
		var out []failure
		for k := i; k < len(msgs); k++ {
			out = append(out, failure{ItemIdentifier: fmt.Sprintf("m%d", k)})
		}
		return out
	}
	return nil
}

func pid(name string) string { return notiontest.PropID(name) }

// num reads a number property of a fake page by column name.
func num(pg *notiontest.Page, name string) float64 {
	v, _ := pg.Properties[pid(name)].(map[string]any)
	f, _ := v["number"].(float64)
	return f
}
