package data

import (
	"context"
	"fmt"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/authorizationgrant"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/mediabatch"
	"openduanju-server/internal/data/ent/revisionepisode"
)

// FreezeAndCreateBatch freezes a revision and creates the batch over it in
// one transaction. The revision row is locked, the expected version and the
// draft state are checked, the selected set and the processing parameters are
// frozen into the batch and the revision leaves the draft state. A selection
// that is changed after this point can no longer reach the batch, and a batch
// can no longer advance a revision that left the processing state.
//
// A second submission for the same revision returns the existing batch.
func (r *MediaBatchRepo) FreezeAndCreateBatch(ctx context.Context, request biz.FreezeBatchRequest) (*biz.MediaBatch, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(request.RevisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionNotFound
		}
		return nil, err
	}
	// A batch that already exists for this revision wins: the submission is
	// idempotent and must not create a second batch. Nothing changed, so the
	// repeated submission is not recorded as a second submission.
	existing, err := tx.MediaBatch.Query().
		Where(mediabatch.RevisionIDEQ(request.RevisionID)).
		Only(ctx)
	if err == nil {
		biz.AuditUnchanged(ctx)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return toMediaBatch(existing), nil
	}
	if !ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, err
	}
	if revision.State != string(biz.RevisionStateDraft) {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: state %s", biz.ErrRevisionNotEditable, revision.State)
	}
	if int(revision.Version) != request.ExpectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrRevisionVersion
	}
	episodes, err := tx.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(request.RevisionID), revisionepisode.SelectedEQ(true)).
		Order(ent.Asc(revisionepisode.FieldEpisodeNo)).
		All(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if len(episodes) == 0 {
		_ = tx.Rollback()
		return nil, biz.ErrNoSelectedEpisodes
	}
	targets := make([]biz.MediaEpisodeTarget, 0, len(episodes))
	inputs := make([]int64, 0, len(episodes))
	for _, episode := range episodes {
		// A manual input is frozen by reference: the task reads exactly the
		// object the server confirmed at complete time. An input that was
		// declared but never confirmed stops the submission instead of
		// producing a task that cannot acquire anything.
		if episode.InputKind == "manual" {
			if episode.InputState != biz.InputStateReady || episode.InputRef <= 0 {
				_ = tx.Rollback()
				return nil, fmt.Errorf("%w: episode %d", biz.ErrBatchInputNotReady, episode.EpisodeNo)
			}
		} else if episode.InputRef != 0 {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: episode %d names an input of another kind", biz.ErrBatchInputNotReady, episode.EpisodeNo)
		}
		targets = append(targets, biz.MediaEpisodeTarget{
			ID:         episode.ID,
			ExternalID: textValue(episode.ExternalID),
			Number:     int(episode.EpisodeNo),
		})
		inputs = append(inputs, episode.InputRef)
	}
	// The cover is bound to the revision before the batch exists: a cover
	// uploaded while the revision was a draft is inherited here, so the gate
	// does not wait for a second upload. A cover that is not a confirmed,
	// ready asset of this revision keeps the batch pending.
	coverStatus := biz.MediaCoverPending
	if revision.CoverAssetID > 0 {
		asset, err := tx.MediaAsset.Get(ctx, revision.CoverAssetID)
		if err != nil && !ent.IsNotFound(err) {
			_ = tx.Rollback()
			return nil, err
		}
		// The revision holds the reference to the cover, so the reference
		// is the trusted relation. The revision that created the asset is
		// not the only place it may be used: a draft that inherits a cover
		// keeps it ready instead of waiting for a second upload.
		if err == nil && asset.State == string(biz.MediaAssetReady) && asset.Kind == "cover" {
			coverStatus = biz.MediaCoverReady
		}
	}
	// The grant is chosen explicitly and checked under the same lock: it must
	// cover this series, be the expected version, be in force and allow
	// processing. A version drift is a conflict, never a silent adoption of
	// the newest grant.
	if request.AuthorizationGrantID <= 0 {
		_ = tx.Rollback()
		return nil, biz.ErrGrantNotFound
	}
	grantRow, err := tx.AuthorizationGrant.Query().
		Where(authorizationgrant.IDEQ(request.AuthorizationGrantID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	grant := toAuthorizationGrant(grantRow)
	if err := biz.EnsureForSubmit(grant, revision.SeriesID, request.ExpectedAuthorizationVersion, time.Now().UTC()); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	authorizationHash := grant.Fingerprint()
	inputKind := request.InputKind
	if inputKind == "" {
		inputKind = "sdk"
	}
	created, err := tx.MediaBatch.Create().
		SetSeriesID(revision.SeriesID).
		SetRevisionID(revision.ID).
		SetSelectionHash(biz.SelectionHash(targets)).
		SetProfileVersion(request.ProfileVersion).
		SetInputKind(inputKind).
		SetStatus(string(biz.MediaBatchQueued)).
		SetCoverStatus(coverStatus).
		SetItemTotal(len(targets)).
		SetCreatedBy(request.Actor).
		// The worker processes the batch after the request is gone; the
		// identity of the submission travels with the row.
		SetTraceID(biz.TraceID(ctx)).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	for index, episode := range targets {
		if _, err := tx.MediaItem.Create().
			SetBatchID(created.ID).
			SetEpisodeID(episode.ID).
			SetExternalID(episode.ExternalID).
			SetEpisodeNo(episode.Number).
			SetStatus(string(biz.MediaItemQueued)).
			SetInputUploadID(inputs[index]).
			// The task is claimed on its own, so it carries the identity of
			// the submission: a resumed task still points at its trigger.
			SetTraceID(biz.TraceID(ctx)).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	// The revision is frozen with the batch: it leaves the draft state, so the
	// selection can no longer be edited while the batch runs.
	if _, err := tx.ContentRevision.UpdateOneID(revision.ID).
		SetState(string(biz.RevisionStateProcessing)).
		SetAuthorizationGrantID(grantRow.ID).
		SetAuthorizationVersion(grantRow.Version).
		SetAuthorizationHash(authorizationHash).
		AddVersion(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, created.ID, int(created.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toMediaBatch(created), nil
}

// checkRevisionOwnership reports whether the revision of a batch is still
// owned by that batch. Only a revision in the processing state belongs to a
// running batch: a revision that already waits for a review is no longer
// processed.
func checkRevisionOwnership(ctx context.Context, tx *ent.Tx, revisionID int64) error {
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(revisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return biz.ErrRevisionNotOwned
		}
		return err
	}
	if revision.State != string(biz.RevisionStateProcessing) {
		return fmt.Errorf("%w: state %s", biz.ErrRevisionNotOwned, revision.State)
	}
	return nil
}
