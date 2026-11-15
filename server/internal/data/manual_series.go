package data

import (
	"context"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/contentseries"
)

// CreateManualSeries declares a local work with its first working draft.
//
// The work keeps no source code and no external identifier, and its episodes
// keep no provider identity either: they are identified by the revision they
// belong to and by the number the operator wrote. Nothing is downloaded or
// processed here, and no authorization or review state is touched.
//
// The whole form is written in one transaction, so an invalid value leaves no
// half-created work behind. A repeated submission is recognised by its request
// id: the same payload returns the series that already exists, a different
// payload is a conflict.
func (r *SourceCatalogRepo) CreateManualSeries(ctx context.Context, input biz.ManualSeriesInput, fingerprint string) (*biz.SeriesView, error) {
	existing, err := r.manualSeriesByRequest(ctx, input.RequestID, fingerprint)
	if err != nil || existing != nil {
		if existing != nil {
			// A repeated submission returns the work the first call created.
			// Nothing changed this time, so there is no change to record.
			biz.AuditUnchanged(ctx)
		}
		return existing, err
	}
	series, err := r.createManualSeries(ctx, input, fingerprint)
	if err != nil {
		// Two submissions of one form can pass the lookup together; the unique
		// index then decides and the loser answers with the winner's work.
		if input.RequestID != "" && ent.IsConstraintError(err) {
			return r.manualSeriesByRequest(ctx, input.RequestID, fingerprint)
		}
		return nil, err
	}
	return r.GetSeriesDetail(ctx, series.ID)
}

// manualSeriesByRequest returns the series a request id already created, and
// refuses a key that was used for another payload.
func (r *SourceCatalogRepo) manualSeriesByRequest(ctx context.Context, requestID, fingerprint string) (*biz.SeriesView, error) {
	if requestID == "" {
		return nil, nil
	}
	row, err := r.db.ContentSeries.Query().
		Where(contentseries.RequestIDEQ(requestID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if row.RequestFingerprint != fingerprint {
		return nil, biz.ErrIdempotencyConflict
	}
	return r.GetSeriesDetail(ctx, row.ID)
}

// createManualSeries writes the work, its first revision and its episodes.
func (r *SourceCatalogRepo) createManualSeries(ctx context.Context, input biz.ManualSeriesInput, fingerprint string) (*ent.ContentSeries, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	create := tx.ContentSeries.Create().
		// An empty source code says the work has no provider; the external
		// identifier stays NULL.
		SetSourceCode("").
		SetTitle(input.Title).
		SetDescription(input.Description).
		SetStatus("draft").
		SetRevision(1).
		SetVersion(1)
	if input.RequestID != "" {
		create = create.SetRequestID(input.RequestID).SetRequestFingerprint(fingerprint)
	}
	series, err := create.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	revision, err := tx.ContentRevision.Create().
		SetSeriesID(series.ID).
		SetRevisionNo(1).
		SetState("draft").
		SetVersion(1).
		SetTitle(input.Title).
		SetDescription(input.Description).
		SetTags([]string{}).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	builders := make([]*ent.RevisionEpisodeCreate, 0, len(input.Episodes))
	for _, episode := range input.Episodes {
		builders = append(builders, tx.RevisionEpisode.Create().
			SetRevisionID(revision.ID).
			SetEpisodeNo(int32(episode.Number)).
			// The episodes come from an upload, never from a provider: saying
			// so at creation keeps the freeze honest, because it then refuses
			// the work until every selected episode has a confirmed input.
			SetInputKind("manual").
			// The declared episodes are what the work is made of, so they
			// start selected; the operator still chooses what to upload.
			SetSelected(true))
	}
	if err := tx.RevisionEpisode.CreateBulk(builders...).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := series.Update().SetCurrentDraftRevisionID(revision.ID).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, series.ID, int(series.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return series, nil
}
