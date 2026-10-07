package worker

import (
	"context"
	"errors"

	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/setup"
	"reading-tracker/backend/internal/store"
)

// handleArticle implements spec §2.13. It always writes absolute values read from DynamoDB (C6).
func (w *Worker) handleArticle(ctx context.Context, m queue.Message) error {
	inst, err := w.activeInstallation(ctx, m.InstallationID)
	if inst == nil || err != nil {
		return err
	}
	art, err := w.Store.GetArticle(ctx, inst.ID(), m.URLHash)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	completed := isCompleted(art, w.Tracking)
	sess, err := w.openSession(inst)
	if err != nil {
		return err
	}

	if art.NotionPageID == "" {
		if art.TotalSeconds < int64(w.Tracking.MinCreateSeconds) {
			return nil
		}
		if err := w.createArticlePage(ctx, sess, m.URLHash, art, completed); err != nil {
			return w.dataSourceError(ctx, inst, setup.LibraryTitle, err)
		}
	} else {
		err := sess.call(ctx, func(tok string) error {
			return w.updatePage(ctx, tok, art.NotionPageID, inst.LibraryDSID, articleMetrics(inst.Props, art, completed))
		})
		if isPageGone(err) {
			if err := w.Store.ClearArticlePage(ctx, inst.ID(), m.URLHash); err != nil {
				return err
			}
			return w.requeue(ctx, m)
		}
		if err != nil {
			return err
		}
	}
	return w.Store.MarkArticleSynced(ctx, inst.ID(), m.URLHash, art.Version, completed, w.Now())
}

// createArticlePage adopts a page with the same URL if one exists, else creates one.
func (w *Worker) createArticlePage(ctx context.Context, sess *session, hash string, art *store.Article, completed bool) error {
	inst := sess.inst
	var page *notion.Page
	if len(art.CanonicalURL) <= notionMaxURL {
		err := sess.call(ctx, func(tok string) error {
			res, err := w.Notion.QueryDataSource(ctx, tok, inst.LibraryDSID, map[string]any{
				"filter":    map[string]any{"property": inst.Props["url"], "url": map[string]any{"equals": art.CanonicalURL}},
				"page_size": 1,
			})
			if err == nil && len(res.Results) > 0 {
				page = &res.Results[0]
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	if page != nil {
		err := sess.call(ctx, func(tok string) error {
			return w.updatePage(ctx, tok, page.ID, inst.LibraryDSID, articleMetrics(inst.Props, art, completed))
		})
		if isPageGone(err) {
			page = nil // vanished (or moved) between the query and the PATCH: create a fresh page below
		} else if err != nil {
			return err
		}
	}
	if page == nil {
		err := sess.call(ctx, func(tok string) error {
			p, err := w.Notion.CreatePage(ctx, tok, articleCreateBody(inst.LibraryDSID, inst.Props, art, completed))
			page = p
			return err
		})
		if err != nil {
			return err
		}
	}
	return w.Store.SetArticlePage(ctx, inst.ID(), hash, page.ID, page.URL)
}
