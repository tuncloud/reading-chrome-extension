package notionsync

import (
	"testing"

	"reading-tracker/backend/internal/adapter/queue/msg"
	"reading-tracker/backend/internal/domain"
)

// The job writes to whichever installation is the user's active one when it runs.
func TestJobUsesUsersActiveInstallation(t *testing.T) {
	e := newEnv(t)
	a := e.seedInstallation("tok", "rt")
	e.read(artURL, 0, 60, 0.5)
	job := msg.Article(testUser, domain.URLHash(artURL))

	if f := e.handle(job); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	first := e.onlyLibraryPage()
	if got := e.article(artURL).NotionDataSourceID; got != a.LibraryDSID {
		t.Fatalf("article data source %q, want %q", got, a.LibraryDSID)
	}

	// The user authorizes a second workspace; it becomes the active installation.
	tpl2 := e.ns.AddTemplate("tpl-2")
	sealed, err := e.keys.Seal([]byte("tok"), []byte("bot-2"))
	must(t, err)
	b := &domain.Installation{ID: "bot-2", UserID: testUser, WorkspaceName: "WS2", Status: domain.StatusActive, AccessTokenEnc: sealed,
		TokenVersion: 1, CreatedAt: domain.Timestamp(e.now), UpdatedAt: domain.Timestamp(e.now)}
	b.ApplyDatabases(tpl2.DBConfig())
	must(t, e.db.Store.PutInstallation(ctx, "bot-2", b))
	must(t, e.db.Store.SetActiveInstallation(ctx, testUser, "bot-2"))

	e.q.Drain()
	if f := e.handle(job); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}
	sent := e.q.Drain()
	if len(sent) != 1 || sent[0].Message != job {
		t.Fatalf("requeue %+v", sent)
	}
	if f := e.handle(sent[0].Message); len(f) != 0 {
		t.Fatalf("failures %+v", f)
	}

	pages := e.ns.Pages(tpl2.LibraryDS)
	if len(pages) != 1 || num(pages[0], "Reading time (min)") != 1 {
		t.Fatalf("tpl-2 library pages %d, want 1 with Reading time 1", len(pages))
	}
	if got := e.article(artURL).NotionDataSourceID; got != tpl2.LibraryDS {
		t.Fatalf("article data source %q, want %q", got, tpl2.LibraryDS)
	}
	old := e.ns.Page(first.ID)
	if old == nil || old.Archived {
		t.Fatalf("page in tpl-1 was removed: %+v", old)
	}
}

func TestJobForUserWithoutActiveInstallationIsAcked(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *wenv)
	}{
		{"user does not exist", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			if _, err := e.db.Pool.Exec(ctx, `DELETE FROM users`); err != nil {
				t.Fatal(err)
			}
		}},
		{"profile is deleted", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			must(e.t, e.db.Store.MarkProfileDeleted(ctx, testUser, e.now))
		}},
		{"no active installation", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			must(e.t, e.db.Store.SetActiveInstallation(ctx, testUser, ""))
		}},
		{"active installation missing", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			must(e.t, e.db.Store.SetActiveInstallation(ctx, testUser, "bot-gone"))
		}},
		{"active installation needs setup", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			must(e.t, e.db.Store.SetInstallationStatus(ctx, testBot, domain.StatusNeedsSetup, "", e.now))
		}},
		{"active installation revoked", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			must(e.t, e.db.Store.SetInstallationStatus(ctx, testBot, domain.StatusRevoked, "", e.now))
		}},
		{"active installation belongs to another user", func(e *wenv) {
			e.seedInstallation("tok", "rt")
			e.ensureUser("u2")
			if _, err := e.db.Pool.Exec(ctx, `UPDATE installations SET user_id = 'u2' WHERE id = $1`, testBot); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.read(artURL, 0, 120, 0.5)
			c.setup(e)
			if f := e.handle(msg.Article(testUser, domain.URLHash(artURL))); len(f) != 0 {
				t.Fatalf("failures %+v", f)
			}
			if n := len(e.ns.Requests()); n != 0 {
				t.Fatalf("%d Notion requests, want 0", n)
			}
		})
	}
}
