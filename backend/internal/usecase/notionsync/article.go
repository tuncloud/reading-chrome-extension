package notionsync

import (
	"context"
	"errors"

	"reading-tracker/backend/internal/domain"
	"reading-tracker/backend/internal/usecase/port"
)

// syncArticle implements spec §2.13. It writes absolute values read from storage (C6).
func (s *Service) syncArticle(ctx context.Context, job port.Job) error {
	inst, err := s.activeInstallation(ctx, job.InstallationID)
	if inst == nil || err != nil {
		return err
	}
	art, err := s.Reading.GetArticle(ctx, inst.ID, job.URLHash)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	completed := s.Policy.IsCompleted(*art)
	sess, err := s.openSession(inst)
	if err != nil {
		return err
	}

	switch {
	case art.NotionPageID != "" && art.NotionDataSourceID != "" && !domain.SameID(art.NotionDataSourceID, inst.LibraryDSID):
		// R1: the page lives in a database this installation no longer writes to. Its
		// property ids differ from ours, so a PATCH would be rejected; re-create instead.
		return s.forgetArticlePage(ctx, inst.ID, job)
	case art.NotionPageID == "":
		if art.TotalSeconds < int64(s.Policy.MinCreateSeconds) {
			return nil
		}
		if err := s.createArticlePage(ctx, sess, art, completed); err != nil {
			return s.dataSourceError(ctx, inst, domain.LibraryName, err)
		}
	default:
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateArticlePage(ctx, tok, sess.db(), art.NotionPageID, *art, completed)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			return s.forgetArticlePage(ctx, inst.ID, job)
		}
		if err != nil {
			return err
		}
	}
	return s.Reading.MarkArticleSynced(ctx, inst.ID, job.URLHash, art.Version, completed, s.Now())
}

// forgetArticlePage clears the page link and re-delivers the job, which then creates
// (or adopts) a page in the current Library.
func (s *Service) forgetArticlePage(ctx context.Context, inst string, job port.Job) error {
	if err := s.Reading.ClearArticlePage(ctx, inst, job.URLHash); err != nil {
		return err
	}
	return s.Queue.Requeue(ctx, job)
}

// createArticlePage adopts a page with the same URL if one exists, else creates one.
func (s *Service) createArticlePage(ctx context.Context, sess *session, art *domain.Article, completed bool) error {
	var page *port.Page
	err := sess.call(ctx, func(tok string) error {
		p, err := s.Pages.FindArticlePage(ctx, tok, sess.db(), art.CanonicalURL)
		page = p
		return err
	})
	if err != nil {
		return err
	}
	if page != nil {
		err := sess.call(ctx, func(tok string) error {
			return s.Pages.UpdateArticlePage(ctx, tok, sess.db(), page.ID, *art, completed)
		})
		if port.IsNotionKind(err, port.NotionPageGone) {
			page = nil // vanished between the query and the PATCH: create a fresh page below
		} else if err != nil {
			return err
		}
	}
	if page == nil {
		err := sess.call(ctx, func(tok string) error {
			p, err := s.Pages.CreateArticlePage(ctx, tok, sess.db(), *art, completed)
			page = p
			return err
		})
		if err != nil {
			return err
		}
	}
	return s.Reading.SetArticlePage(ctx, sess.inst.ID, art.Hash, page.ID, page.URL, sess.inst.LibraryDSID)
}
