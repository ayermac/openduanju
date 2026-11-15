package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/authorizationgrant"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/mediaasset"
	"openduanju-server/internal/data/ent/mediabatch"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/reviewtask"
)

// RefreshReadyBatches re-evaluates the counters and the review gate of the
// batches whose cover is ready. A batch that became ready before its cover
// was uploaded is completed here, so exactly one review task exists per ready
// revision. A batch whose stored state is stale is recomputed as well.
func (r *MediaBatchRepo) RefreshReadyBatches(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.client.MediaBatch.Query().
		Where(
			mediabatch.CoverStatusEQ(biz.MediaCoverReady),
			mediabatch.StatusNotIn(
				string(biz.MediaBatchCancelled),
				string(biz.MediaBatchPendingReview),
			),
		).
		Order(ent.Asc(mediabatch.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return 0, err
	}
	refreshed := 0
	for _, row := range rows {
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return refreshed, err
		}
		if _, err := refreshBatchState(ctx, tx, row.ID); err != nil {
			_ = tx.Rollback()
			return refreshed, err
		}
		if err := tx.Commit(); err != nil {
			return refreshed, err
		}
		refreshed++
	}
	return refreshed, nil
}

// refreshBatchState recomputes the counters of a batch and applies the
// review gate: a fully ready batch creates exactly one review task.
func refreshBatchState(ctx context.Context, tx *ent.Tx, batchID int64) (*ent.MediaBatch, error) {
	batch, err := tx.MediaBatch.Get(ctx, batchID)
	if err != nil {
		return nil, err
	}
	total, err := tx.MediaItem.Query().Where(mediaitem.BatchIDEQ(batchID)).Count(ctx)
	if err != nil {
		return nil, err
	}
	ready, err := tx.MediaItem.Query().
		Where(mediaitem.BatchIDEQ(batchID), mediaitem.StatusEQ(string(biz.MediaItemReady))).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	failed, err := tx.MediaItem.Query().
		Where(mediaitem.BatchIDEQ(batchID),
			mediaitem.StatusIn(string(biz.MediaItemFailed), string(biz.MediaItemWaitingRetry))).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	cancelled, err := tx.MediaItem.Query().
		Where(mediaitem.BatchIDEQ(batchID), mediaitem.StatusEQ(string(biz.MediaItemCancelled))).
		Count(ctx)
	if err != nil {
		return nil, err
	}
	pending := total - ready - failed - cancelled

	status := batch.Status
	switch {
	case batch.CancelRequested && pending == 0:
		status = string(biz.MediaBatchCancelled)
	case batch.CancelRequested:
		status = string(biz.MediaBatchCancelling)
	case failed > 0:
		status = string(biz.MediaBatchPartiallyFailed)
	case ready == total && total > 0:
		status = string(biz.MediaBatchSucceeded)
	case ready > 0:
		status = string(biz.MediaBatchRunning)
	}
	if batch.Status == string(biz.MediaBatchPendingReview) {
		status = string(biz.MediaBatchPendingReview)
	}
	gate := ready == total && total > 0 && batch.CoverStatus == biz.MediaCoverReady && !batch.CancelRequested
	failureCode := ""
	failureMessage := ""
	if gate {
		// A batch may only send a revision to review while it owns it. A
		// revision that was withdrawn or replaced stops the batch instead of
		// opening a review task for material nobody asked for.
		if err := checkRevisionStillOwned(ctx, tx, batch.RevisionID); err != nil {
			if !errors.Is(err, biz.ErrRevisionNotOwned) {
				return nil, err
			}
			gate = false
			status = string(biz.MediaBatchFailed)
			failureCode = "REVISION_NOT_OWNED"
			failureMessage = "the revision left the processing state; the batch stops here"
		}
	}
	if gate {
		// The frozen grant is re-checked now, not when the batch was
		// submitted: a grant revoked or expired while the media was processed
		// must not open a review task.
		if err := ensureReviewAuthorization(ctx, tx, batch.RevisionID); err != nil {
			if !errors.Is(err, biz.ErrGrantNotFound) && !errors.Is(err, biz.ErrGrantNotEffective) && !errors.Is(err, biz.ErrGrantPermission) {
				return nil, err
			}
			gate = false
			status = string(biz.MediaBatchFailed)
			failureCode = "AUTHORIZATION_INVALID"
			failureMessage = "the frozen authorization is no longer in force; the batch cannot go to review"
		}
	}
	update := tx.MediaBatch.UpdateOneID(batchID).
		SetItemTotal(total).
		SetItemReady(ready).
		SetItemFailed(failed).
		SetStatus(status).
		AddVersion(1)
	if failureCode != "" && batch.Status != string(biz.MediaBatchFailed) {
		update = update.
			SetErrorCode(failureCode).
			SetErrorMessage(failureMessage)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	if gate {
		if err := createReviewTask(ctx, tx, updated); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

// checkRevisionStillOwned reports whether the revision of a batch is still
// owned by that batch. A revision that waits for a review is still owned: its
// batch opened the review task already.
func checkRevisionStillOwned(ctx context.Context, tx *ent.Tx, revisionID int64) error {
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
	switch revision.State {
	case string(biz.RevisionStateProcessing), string(biz.RevisionStatePendingReview):
		return nil
	}
	return fmt.Errorf("%w: state %s", biz.ErrRevisionNotOwned, revision.State)
}

// createReviewTask creates the single review task of a ready revision. The
// unique key on the revision keeps concurrent workers from creating two.
func createReviewTask(ctx context.Context, tx *ent.Tx, batch *ent.MediaBatch) error {
	_, err := tx.ReviewTask.Query().
		Where(reviewtask.RevisionIDEQ(batch.RevisionID)).
		Only(ctx)
	if err == nil {
		return nil
	}
	if !ent.IsNotFound(err) {
		return err
	}
	task, err := tx.ReviewTask.Create().
		SetRevisionID(batch.RevisionID).
		SetSeriesID(batch.SeriesID).
		SetBatchID(batch.ID).
		SetStatus("open").
		SetSubmittedBy(batch.CreatedBy).
		Save(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.ContentRevision.UpdateOneID(batch.RevisionID).
		SetState(string(biz.RevisionStatePendingReview)).
		AddVersion(1).
		Save(ctx); err != nil {
		return err
	}
	_, err = tx.MediaBatch.UpdateOneID(batch.ID).
		SetStatus(string(biz.MediaBatchPendingReview)).
		SetReviewTaskID(task.ID).
		AddVersion(1).
		Save(ctx)
	return err
}

// upsertMediaAsset stores the confirmed object and promotes it to ready.
func upsertMediaAsset(ctx context.Context, tx *ent.Tx, asset biz.MediaAsset, batch *ent.MediaBatch, episodeID int64) (*ent.MediaAsset, error) {
	existing, err := tx.MediaAsset.Query().
		Where(
			mediaasset.ProviderEQ(asset.Object.Provider),
			mediaasset.BucketEQ(asset.Object.Bucket),
			mediaasset.RegionEQ(asset.Object.Region),
			mediaasset.ObjectKeyEQ(asset.Object.Key),
			mediaasset.ObjectVersionEQ(asset.Object.VersionID),
		).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		// The object identity is content addressed: the same bytes uploaded
		// for a second episode resolve to the same asset. Only its readiness
		// changes; the episode/batch a task points at is recorded on the item
		// and on the revision episode, so this row is not reparented.
		return tx.MediaAsset.UpdateOneID(existing.ID).
			SetState(string(biz.MediaAssetReady)).
			Save(ctx)
	}
	return tx.MediaAsset.Create().
		SetProvider(asset.Object.Provider).
		SetBucket(asset.Object.Bucket).
		SetRegion(asset.Object.Region).
		SetObjectKey(asset.Object.Key).
		SetObjectVersion(asset.Object.VersionID).
		SetSha256(asset.SHA256).
		SetSizeBytes(asset.SizeBytes).
		SetContentType(asset.ContentType).
		SetKind("episode").
		SetState(string(biz.MediaAssetReady)).
		SetRevisionID(batch.RevisionID).
		SetEpisodeID(episodeID).
		SetBatchID(batch.ID).
		Save(ctx)
}

// frozenGrant returns the authorization a revision was submitted with. The
// grant row itself is immutable; only its status and version can change, so
// reading the latest row is enough to know whether it is still in force.
func frozenGrant(ctx context.Context, client *ent.Client, revisionID int64) (*biz.AuthorizationGrant, error) {
	revision, err := client.ContentRevision.Get(ctx, revisionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	if revision.AuthorizationGrantID == 0 {
		return nil, fmt.Errorf("%w: the revision has no frozen authorization", biz.ErrGrantNotFound)
	}
	row, err := client.AuthorizationGrant.Get(ctx, revision.AuthorizationGrantID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	return toAuthorizationGrant(row), nil
}

// frozenGrantTx is the transactional variant used by the write gates.
func frozenGrantTx(ctx context.Context, tx *ent.Tx, revisionID int64) (*biz.AuthorizationGrant, error) {
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(revisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	if revision.AuthorizationGrantID == 0 {
		return nil, fmt.Errorf("%w: the revision has no frozen authorization", biz.ErrGrantNotFound)
	}
	row, err := tx.AuthorizationGrant.Query().
		Where(authorizationgrant.IDEQ(revision.AuthorizationGrantID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	return toAuthorizationGrant(row), nil
}

// EnsureBatchAuthorization re-checks the frozen grant outside a transaction,
// before a task starts or before a worker commits ready.
func (r *MediaBatchRepo) EnsureBatchAuthorization(ctx context.Context, batchID int64, permission biz.Permission) error {
	batch, err := r.client.MediaBatch.Get(ctx, batchID)
	if err != nil {
		if ent.IsNotFound(err) {
			return biz.ErrBatchNotFound
		}
		return err
	}
	grant, err := frozenGrant(ctx, r.client, batch.RevisionID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !grant.ValidAt(now) {
		return fmt.Errorf("%w: status %s", biz.ErrGrantNotEffective, grant.Status)
	}
	if !grant.Allows(permission, now) {
		return fmt.Errorf("%w: %s", biz.ErrGrantPermission, permission)
	}
	return nil
}

// ensureReviewAuthorization checks that the frozen grant still allows review:
// it has to be in force and allow both display and playback.
func ensureReviewAuthorization(ctx context.Context, tx *ent.Tx, revisionID int64) error {
	grant, err := frozenGrantTx(ctx, tx, revisionID)
	if err != nil {
		return err
	}
	if !grant.ValidAt(time.Now().UTC()) {
		return fmt.Errorf("%w: status %s", biz.ErrGrantNotEffective, grant.Status)
	}
	if !grant.Allows(biz.PermissionDisplay, time.Now().UTC()) {
		return fmt.Errorf("%w: display", biz.ErrGrantPermission)
	}
	if !grant.Allows(biz.PermissionPlayback, time.Now().UTC()) {
		return fmt.Errorf("%w: playback", biz.ErrGrantPermission)
	}
	return nil
}

// ensureReadyAuthorization checks that the frozen grant still allows
// processing when a task commits ready.
func ensureReadyAuthorization(ctx context.Context, tx *ent.Tx, revisionID int64) error {
	grant, err := frozenGrantTx(ctx, tx, revisionID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !grant.ValidAt(now) {
		return fmt.Errorf("%w: status %s", biz.ErrGrantNotEffective, grant.Status)
	}
	if !grant.Allows(biz.PermissionProcess, now) {
		return fmt.Errorf("%w: process", biz.ErrGrantPermission)
	}
	return nil
}
