package worker

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"reading-tracker/backend/internal/adapter/notion"
	"reading-tracker/backend/internal/adapter/queue"
	"reading-tracker/backend/internal/domain"
)

var errPageGone = errors.New("worker: notion page archived")

// updatePage PATCHes properties of a row of data source dsID. A page that comes back
// archived counts as gone, and so does one whose parent is another data source: the
// installation moved to new databases (setup or a new template) and the page was left
// in the old ones, so it must be re-created in dsID (scenario 10).
func (w *Worker) updatePage(ctx context.Context, token, pageID, dsID string, props map[string]any) error {
	p, err := w.Notion.UpdatePage(ctx, token, pageID, props)
	if err != nil {
		return err
	}
	if p.Archived || p.InTrash || (p.Parent.DataSourceID != "" && !notion.SameID(p.Parent.DataSourceID, dsID)) {
		return errPageGone
	}
	return nil
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
func (w *Worker) dataSourceError(ctx context.Context, inst *domain.Installation, title string, err error) error {
	if !isStatus(err, http.StatusNotFound) {
		return err
	}
	if serr := w.Store.SetInstallationStatus(ctx, inst.ID, domain.StatusNeedsSetup, "database_missing:"+title, w.Now()); serr != nil {
		return serr
	}
	w.Log.WarnContext(ctx, "worker: database missing; installation needs setup", "installation_id", inst.ID, "database", title)
	return errAck
}
