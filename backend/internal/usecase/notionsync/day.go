package notionsync

import (
	"context"
	"errors"
	"slices"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// maxDayRelations caps the Articles relation (spec §2.14 step 2).
const maxDayRelations = 100

// syncDay implements spec §2.14.
func (s *Service) syncDay(ctx context.Context, job port.Job) error {
	inst, err := s.activeInstallation(ctx, job.InstallationID)
	if inst == nil || err != nil {
		return err
	}
	day, err := s.Reading.GetDay(ctx, inst.ID, job.Date)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if day.NotionPageID != "" && day.NotionDataSourceID != "" && !domain.SameID(day.NotionDataSourceID, inst.DailyDSID) {
		return s.forgetDayPage(ctx, inst.ID, job) // R1, as for articles
	}
	pageIDs, err := s.dayArticlePages(ctx, inst.ID, day.ArticleHashes)
	if err != nil {
		return err
	}
	sess, err := s.openSession(inst)
	if err != nil {
		return err
	}
	if day.NotionPageID == "" {
		if err := s.createDayPage(ctx, sess, day, pageIDs); err != nil {
			return s.dataSourceError(ctx, inst, domain.DailyLogName, err)
		}
	} else {
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateDayPage(ctx, tok, sess.db(), day.NotionPageID, *day, pageIDs)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			return s.forgetDayPage(ctx, inst.ID, job)
		}
		if err != nil {
			return err
		}
	}
	return s.Reading.MarkDaySynced(ctx, inst.ID, job.Date, day.Version)
}

func (s *Service) forgetDayPage(ctx context.Context, inst string, job port.Job) error {
	if err := s.Reading.ClearDayPage(ctx, inst, job.Date); err != nil {
		return err
	}
	return s.Queue.Requeue(ctx, job)
}

// dayArticlePages returns the page ids of the day's articles that have one (≤ 100).
func (s *Service) dayArticlePages(ctx context.Context, inst string, hashes []string) ([]string, error) {
	sorted := slices.Sorted(slices.Values(hashes))
	arts, err := s.Reading.BatchGetArticles(ctx, inst, sorted)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, h := range sorted {
		if a := arts[h]; a != nil && a.NotionPageID != "" {
			ids = append(ids, a.NotionPageID)
			if len(ids) == maxDayRelations {
				break
			}
		}
	}
	return ids, nil
}

// createDayPage adopts the row for this date if one exists, else creates it.
func (s *Service) createDayPage(ctx context.Context, sess *session, day *domain.Day, pageIDs []string) error {
	var page *port.Page
	err := sess.call(ctx, func(tok string) error {
		p, err := s.Pages.FindDayPage(ctx, tok, sess.db(), day.Date)
		page = p
		return err
	})
	if err != nil {
		return err
	}
	if page != nil {
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateDayPage(ctx, tok, sess.db(), page.ID, *day, pageIDs)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			page = nil
		} else if err != nil {
			return err
		}
	}
	if page == nil {
		err := sess.call(ctx, func(tok string) error {
			p, err := s.Pages.CreateDayPage(ctx, tok, sess.db(), *day, pageIDs)
			page = p
			return err
		})
		if err != nil {
			return err
		}
	}
	return s.Reading.SetDayPage(ctx, sess.inst.ID, day.Date, page.ID, sess.inst.DailyDSID)
}
