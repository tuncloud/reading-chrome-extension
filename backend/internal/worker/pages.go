package worker

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/store"
)

var errPageGone = errors.New("worker: notion page archived")

// updatePage PATCHes properties; a page that comes back archived counts as gone.
func (w *Worker) updatePage(ctx context.Context, token, pageID string, props map[string]any) error {
	p, err := w.Notion.UpdatePage(ctx, token, pageID, props)
	if err == nil && (p.Archived || p.InTrash) {
		return errPageGone
	}
	return err
}

// isPageGone: 404, or Notion's 400 "Can't edit block that is archived" (spec §2.15).
func isPageGone(err error) bool {
	if errors.Is(err, errPageGone) {
		return true
	}
	e, ok := notion.AsAPIError(err)
	return ok && (e.Status == http.StatusNotFound ||
		(e.Status == http.StatusBadRequest && e.Code == "validation_error" && strings.Contains(strings.ToLower(e.Message), "archived")))
}

// requeue re-enqueues m with an id that can't collide with its bucket dedup id.
func (w *Worker) requeue(ctx context.Context, m queue.Message) error {
	return w.Queue.Send(ctx, queue.Outgoing{Message: m, DedupID: m.RequeueDedupID(w.Now())})
}

// dataSourceError: a 404 on a data source means the database was deleted, so the
// installation needs setup again and the message is acked (spec §2.15).
func (w *Worker) dataSourceError(ctx context.Context, inst *store.Installation, title string, err error) error {
	if !isStatus(err, http.StatusNotFound) {
		return err
	}
	if serr := w.Store.SetInstallationStatus(ctx, inst.ID(), store.StatusNeedsSetup, "database_missing:"+title, w.Now()); serr != nil {
		return serr
	}
	w.Log.WarnContext(ctx, "worker: database missing; installation needs setup", "installation_id", inst.ID(), "database", title)
	return errAck
}
