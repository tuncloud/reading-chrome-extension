package worker

import (
	"context"
	"errors"
	"slices"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/notion"
	"reading-tracker/backend/internal/queue"
	"reading-tracker/backend/internal/setup"
)

// handleDay implements spec §2.14.
func (w *Worker) handleDay(ctx context.Context, m queue.Message) error {
	inst, err := w.activeInstallation(ctx, m.InstallationID)
	if inst == nil || err != nil {
		return err
	}
	day, err := w.Store.GetDay(ctx, inst.ID, m.Date)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	pageIDs, err := w.dayArticlePages(ctx, inst.ID, day.ArticleHashes)
	if err != nil {
		return err
	}
	sess, err := w.openSession(inst)
	if err != nil {
		return err
	}
	props := dayMetrics(inst.Props, day, pageIDs)

	if day.NotionPageID == "" {
		if err := w.createDayPage(ctx, sess, day, props); err != nil {
			return w.dataSourceError(ctx, inst, setup.DailyTitle, err)
		}
	} else {
		err := sess.call(ctx, func(tok string) error { return w.updatePage(ctx, tok, day.NotionPageID, inst.DailyDSID, props) })
		if isPageGone(err) {
			if err := w.Store.ClearDayPage(ctx, inst.ID, m.Date); err != nil {
				return err
			}
			return w.requeue(ctx, m)
		}
		if err != nil {
			return err
		}
	}
	return w.Store.MarkDaySynced(ctx, inst.ID, m.Date, day.Version)
}

// dayArticlePages returns the Notion page ids of the day's articles that have one (≤ 100).
func (w *Worker) dayArticlePages(ctx context.Context, inst string, hashes []string) ([]string, error) {
	sorted := slices.Sorted(slices.Values(hashes))
	arts, err := w.Store.BatchGetArticles(ctx, inst, sorted)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, h := range sorted {
		if a := arts[h]; a != nil && a.NotionPageID != "" {
			ids = append(ids, a.NotionPageID)
			if len(ids) == maxRelations {
				break
			}
		}
	}
	return ids, nil
}

func dayMetrics(p map[string]string, d *domain.Day, pageIDs []string) map[string]any {
	rel := make([]any, len(pageIDs))
	for i, id := range pageIDs {
		rel[i] = map[string]any{"id": id}
	}
	return map[string]any{
		p["day_minutes"]:  map[string]any{"number": roundMinutes(d.Seconds)},
		p["day_articles"]: map[string]any{"relation": rel},
	}
}

// createDayPage adopts the row for this date if one exists, else creates it.
func (w *Worker) createDayPage(ctx context.Context, sess *session, day *domain.Day, metrics map[string]any) error {
	inst := sess.inst
	var page *notion.Page
	err := sess.call(ctx, func(tok string) error {
		res, err := w.Notion.QueryDataSource(ctx, tok, inst.DailyDSID, map[string]any{
			"filter":    map[string]any{"property": inst.Props["day_date"], "date": map[string]any{"equals": day.Date}},
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
	if page != nil {
		err := sess.call(ctx, func(tok string) error { return w.updatePage(ctx, tok, page.ID, inst.DailyDSID, metrics) })
		if isPageGone(err) {
			page = nil // vanished (or moved) between the query and the PATCH: create a fresh page below
		} else if err != nil {
			return err
		}
	}
	if page == nil {
		props := map[string]any{
			inst.Props["day_title"]: map[string]any{"title": richText(day.Date)},
			inst.Props["day_date"]:  dateValue(day.Date),
		}
		for k, v := range metrics {
			props[k] = v
		}
		err := sess.call(ctx, func(tok string) error {
			p, err := w.Notion.CreatePage(ctx, tok, map[string]any{
				"parent":     map[string]any{"type": "data_source_id", "data_source_id": inst.DailyDSID},
				"properties": props,
			})
			page = p
			return err
		})
		if err != nil {
			return err
		}
	}
	return w.Store.SetDayPage(ctx, inst.ID, day.Date, page.ID)
}
