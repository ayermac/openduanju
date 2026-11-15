package data

import (
	"context"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/mediabatch"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/revisionepisode"

	"entgo.io/ent/dialect/sql"
)

// mediaPage normalizes the pagination of a media query.
func mediaPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

// MediaBatchRepo persists media batches, items and assets.
type MediaBatchRepo struct {
	client *ent.Client
}

// NewMediaBatchRepo builds the repository on the shared Ent client.
func NewMediaBatchRepo(d *Data) *MediaBatchRepo {
	return &MediaBatchRepo{client: d.Ent()}
}

// NewMediaBatchRepoWithDB builds the repository on an explicit client.
func NewMediaBatchRepoWithDB(client *ent.Client) *MediaBatchRepo {
	return &MediaBatchRepo{client: client}
}

// GetBatch returns one batch.
func (r *MediaBatchRepo) GetBatch(ctx context.Context, id int64) (*biz.MediaBatch, error) {
	row, err := r.client.MediaBatch.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return toMediaBatch(row), nil
}

// GetBatchByRevision returns the batch of a revision.
func (r *MediaBatchRepo) GetBatchByRevision(ctx context.Context, revisionID int64) (*biz.MediaBatch, error) {
	row, err := r.client.MediaBatch.Query().
		Where(mediabatch.RevisionIDEQ(revisionID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return toMediaBatch(row), nil
}

// ListBatches returns the batches matching the query.
func (r *MediaBatchRepo) ListBatches(ctx context.Context, query biz.BatchQuery) ([]biz.MediaBatch, int, error) {
	builder := r.client.MediaBatch.Query()
	if query.SeriesID > 0 {
		builder = builder.Where(mediabatch.SeriesIDEQ(query.SeriesID))
	}
	if query.RevisionID > 0 {
		builder = builder.Where(mediabatch.RevisionIDEQ(query.RevisionID))
	}
	if query.Status != "" {
		builder = builder.Where(mediabatch.StatusEQ(query.Status))
	}
	total, err := builder.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	page, pageSize := mediaPage(query.Page, query.PageSize)
	rows, err := builder.
		Order(ent.Desc(mediabatch.FieldID)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	batches := make([]biz.MediaBatch, 0, len(rows))
	for _, row := range rows {
		batches = append(batches, *toMediaBatch(row))
	}
	return batches, total, nil
}

// ListItems returns the items of a batch.
func (r *MediaBatchRepo) ListItems(ctx context.Context, query biz.ItemQuery) ([]biz.MediaItem, int, error) {
	builder := r.client.MediaItem.Query().Where(mediaitem.BatchIDEQ(query.BatchID))
	if query.Status != "" {
		builder = builder.Where(mediaitem.StatusEQ(query.Status))
	}
	total, err := builder.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	page, pageSize := mediaPage(query.Page, query.PageSize)
	rows, err := builder.
		Order(ent.Asc(mediaitem.FieldEpisodeNo)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]biz.MediaItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, *toMediaItem(row))
	}
	return items, total, nil
}

// GetItem returns one item.
func (r *MediaBatchRepo) GetItem(ctx context.Context, id int64) (*biz.MediaItem, error) {
	row, err := r.client.MediaItem.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return toMediaItem(row), nil
}

// ClaimItems takes ownership of the items that are due. The lease
// generation fences a worker that lost its lease: only the current
// generation may commit the item.
func (r *MediaBatchRepo) ClaimItems(ctx context.Context, worker string, lease time.Duration, limit int) ([]biz.MediaItem, error) {
	if limit <= 0 {
		limit = 4
	}
	now := time.Now().UTC()
	until := now.Add(lease)
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.MediaItem.Query().
		Where(
			// A task that crashed in the middle of a stage keeps its stage.
			// Its lease expired, so another worker may take it over and
			// resume from the persisted checkpoint.
			mediaitem.StatusIn(
				string(biz.MediaItemQueued),
				string(biz.MediaItemWaitingRetry),
				string(biz.MediaItemResolving),
				string(biz.MediaItemAcquiring),
				string(biz.MediaItemValidating),
				string(biz.MediaItemProcessing),
				string(biz.MediaItemUploading),
				string(biz.MediaItemVerifying),
			),
			mediaitem.Or(
				mediaitem.NextEligibleAtIsNil(),
				mediaitem.NextEligibleAtLTE(now),
			),
			mediaitem.Or(
				mediaitem.LeaseUntilIsNil(),
				mediaitem.LeaseUntilLT(now),
			),
		).
		Order(ent.Asc(mediaitem.FieldID)).
		Limit(limit).
		ForUpdate(sql.WithLockAction(sql.SkipLocked)).
		All(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	claimed := make([]biz.MediaItem, 0, len(rows))
	for _, row := range rows {
		batch, err := tx.MediaBatch.Get(ctx, row.BatchID)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if batch.CancelRequested || batch.Status == string(biz.MediaBatchCancelled) {
			continue
		}
		updated, err := tx.MediaItem.UpdateOneID(row.ID).
			SetLeaseOwner(worker).
			SetLeaseUntil(until).
			AddLeaseGeneration(1).
			AddAttempt(1).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		claimed = append(claimed, *toMediaItem(updated))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if len(claimed) > 0 {
		if _, err := r.markBatchesRunning(ctx, claimed); err != nil {
			return nil, err
		}
	}
	return claimed, nil
}

// SaveItem persists a stage checkpoint of an item. The write is rejected
// when the lease moved on or when the batch was cancelled.
func (r *MediaBatchRepo) SaveItem(ctx context.Context, item biz.MediaItem) (*biz.MediaItem, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.MediaItem.Query().
		Where(mediaitem.IDEQ(item.ID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	batch, err := tx.MediaBatch.Get(ctx, row.BatchID)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// The cancellation is checked before the lease: a cancelled batch stops
	// every task, and the worker learns why instead of seeing a lost lease.
	if batch.CancelRequested {
		if _, err := tx.MediaItem.UpdateOneID(row.ID).
			SetStatus(string(biz.MediaItemCancelled)).
			ClearLeaseUntil().
			SetLeaseOwner("").
			AddVersion(1).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, biz.ErrBatchCancelled
	}
	if err := checkLease(row, item); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	updated, err := tx.MediaItem.UpdateOneID(row.ID).
		SetStatus(string(item.Status)).
		SetErrorCode(item.ErrorCode).
		SetErrorMessage(item.ErrorMessage).
		SetInputVersion(item.InputVersion).
		SetObjectProvider(item.Object.Provider).
		SetObjectBucket(item.Object.Bucket).
		SetObjectRegion(item.Object.Region).
		SetObjectKey(item.Object.Key).
		SetObjectVersion(item.Object.VersionID).
		SetInputArtifactPath(item.InputArtifactPath).
		SetInputArtifactSha256(item.InputArtifactSHA256).
		SetOutputArtifactPath(item.OutputArtifactPath).
		SetOutputArtifactSha256(item.OutputArtifactSHA256).
		SetOutputArtifactSize(item.OutputArtifactSize).
		SetSha256(item.SHA256).
		SetSizeBytes(item.SizeBytes).
		SetContentType(item.ContentType).
		SetCodec(item.Codec).
		SetDurationMs(item.DurationMS).
		SetWidth(item.Width).
		SetHeight(item.Height).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toMediaItem(updated), nil
}

// RenewItemLease extends the lease of an item. A worker that lost the item
// to another worker gets ErrItemLeaseLost and stops.
func (r *MediaBatchRepo) RenewItemLease(ctx context.Context, item biz.MediaItem, lease time.Duration) (*biz.MediaItem, error) {
	if lease <= 0 {
		lease = 90 * time.Second
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.MediaItem.Query().
		Where(mediaitem.IDEQ(item.ID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if row.LeaseGeneration != item.LeaseGeneration || row.LeaseOwner != item.LeaseOwner {
		_ = tx.Rollback()
		return nil, biz.ErrItemLeaseLost
	}
	if row.Status == string(biz.MediaItemCancelled) || row.Status == string(biz.MediaItemReady) {
		_ = tx.Rollback()
		return nil, biz.ErrItemLeaseLost
	}
	updated, err := tx.MediaItem.UpdateOneID(row.ID).
		SetLeaseUntil(time.Now().UTC().Add(lease)).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toMediaItem(updated), nil
}

// CommitItemReady links a confirmed object to the revision episode and
// promotes it to ready. The asset, the item, the batch counters and the
// review task are written in one transaction, so a ready item always has a
// confirmed object and a fully ready batch always has exactly one review
// task.
func (r *MediaBatchRepo) CommitItemReady(ctx context.Context, item biz.MediaItem, asset biz.MediaAsset) (*biz.MediaItem, *biz.MediaBatch, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, nil, err
	}
	row, err := tx.MediaItem.Query().
		Where(mediaitem.IDEQ(item.ID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	batch, err := tx.MediaBatch.Get(ctx, row.BatchID)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	if batch.CancelRequested {
		_ = tx.Rollback()
		return nil, nil, biz.ErrBatchCancelled
	}
	if err := checkLease(row, item); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	// The batch may only advance a revision it still owns. A revision that
	// was withdrawn, replaced or already left the processing state does not
	// belong to this batch any more.
	if err := checkRevisionOwnership(ctx, tx, batch.RevisionID); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	// The frozen grant is re-checked before the material becomes ready. A
	// revocation during processing stops the task here instead of producing
	// publishable material.
	if err := ensureReadyAuthorization(ctx, tx, batch.RevisionID); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}

	stored, err := upsertMediaAsset(ctx, tx, asset, batch, row.EpisodeID)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	updated, err := tx.MediaItem.UpdateOneID(row.ID).
		SetStatus(string(biz.MediaItemReady)).
		SetAssetID(stored.ID).
		SetObjectProvider(asset.Object.Provider).
		SetObjectBucket(asset.Object.Bucket).
		SetObjectRegion(asset.Object.Region).
		SetObjectKey(asset.Object.Key).
		SetObjectVersion(asset.Object.VersionID).
		SetSha256(asset.SHA256).
		SetSizeBytes(asset.SizeBytes).
		SetContentType(asset.ContentType).
		SetErrorCode("").
		SetErrorMessage("").
		SetRetryable(false).
		ClearNextEligibleAt().
		ClearLeaseUntil().
		SetLeaseOwner("").
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	// The revision episode points at the ready asset, so the back office can
	// show which episodes already have publishable material.
	if _, err := tx.RevisionEpisode.Update().
		Where(revisionepisode.RevisionIDEQ(batch.RevisionID), revisionepisode.IDEQ(row.EpisodeID)).
		SetAssetID(stored.ID).
		SetAssetState(string(biz.MediaAssetReady)).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	batch, err = refreshBatchState(ctx, tx, batch.ID)
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return toMediaItem(updated), toMediaBatch(batch), nil
}

// FailItem records a classified failure. Retryable failures wait for the
// next eligible time; the batch never loses the consumed attempt.
func (r *MediaBatchRepo) FailItem(ctx context.Context, item biz.MediaItem, code biz.MediaErrorCode, message string, retryable bool, nextEligibleAt *time.Time) (*biz.MediaItem, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.MediaItem.Query().
		Where(mediaitem.IDEQ(item.ID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := checkLease(row, item); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	status := biz.MediaItemFailed
	if retryable {
		status = biz.MediaItemWaitingRetry
	}
	update := tx.MediaItem.UpdateOneID(row.ID).
		SetStatus(string(status)).
		SetErrorCode(string(code)).
		SetErrorMessage(message).
		SetRetryable(retryable).
		ClearLeaseUntil().
		SetLeaseOwner("")
	if nextEligibleAt != nil {
		update = update.SetNextEligibleAt(*nextEligibleAt)
	} else {
		update = update.ClearNextEligibleAt()
	}
	updated, err := update.
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	batch, err := tx.MediaBatch.Get(ctx, row.BatchID)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := refreshBatchState(ctx, tx, batch.ID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toMediaItem(updated), nil
}

// RequestCancel stops a batch. Queued work is cancelled right away, work
// in flight stops at its next checkpoint.
func (r *MediaBatchRepo) RequestCancel(ctx context.Context, id int64, expectedVersion int, reason string) (*biz.MediaBatch, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	batch, err := tx.MediaBatch.Query().
		Where(mediabatch.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrBatchNotFound
		}
		return nil, err
	}
	if expectedVersion > 0 && batch.Version != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrBatchVersion
	}
	_, err = tx.MediaBatch.UpdateOneID(id).
		SetCancelRequested(true).
		SetStatus(string(biz.MediaBatchCancelling)).
		SetErrorMessage(reason).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.MediaItem.Update().
		Where(
			mediaitem.BatchIDEQ(id),
			mediaitem.StatusIn(string(biz.MediaItemQueued), string(biz.MediaItemWaitingRetry)),
		).
		SetStatus(string(biz.MediaItemCancelled)).
		ClearLeaseUntil().
		SetLeaseOwner("").
		AddVersion(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	batch, err = refreshBatchState(ctx, tx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, batch.ID, int(batch.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toMediaBatch(batch), nil
}

// RetryItems requeues failed items. Ready items are never reprocessed.
func (r *MediaBatchRepo) RetryItems(ctx context.Context, id int64, expectedVersion int, itemIDs []int64) (*biz.MediaBatch, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	batch, err := tx.MediaBatch.Query().
		Where(mediabatch.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrBatchNotFound
		}
		return nil, err
	}
	if expectedVersion > 0 && batch.Version != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrBatchVersion
	}
	selector := mediaitem.BatchIDEQ(id)
	if len(itemIDs) > 0 {
		// The batch stays part of the selector: a caller may not requeue a
		// task of another batch.
		selector = mediaitem.And(mediaitem.BatchIDEQ(id), mediaitem.IDIn(itemIDs...))
	}
	// The candidates are counted before they are requeued: afterwards the
	// count would be zero and a successful retry would look like a failure.
	candidates, err := tx.MediaItem.Query().
		Where(
			selector,
			mediaitem.StatusIn(string(biz.MediaItemFailed), string(biz.MediaItemWaitingRetry)),
			mediaitem.RetryableEQ(true),
		).Count(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if candidates == 0 {
		_ = tx.Rollback()
		return nil, biz.ErrBatchNotRetryable
	}
	_, err = tx.MediaItem.Update().
		Where(
			selector,
			mediaitem.StatusIn(string(biz.MediaItemFailed), string(biz.MediaItemWaitingRetry)),
			mediaitem.RetryableEQ(true),
		).
		SetStatus(string(biz.MediaItemQueued)).
		SetErrorCode("").
		SetErrorMessage("").
		ClearNextEligibleAt().
		ClearLeaseUntil().
		SetLeaseOwner("").
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	batch, err = refreshBatchState(ctx, tx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, batch.ID, int(batch.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toMediaBatch(batch), nil
}

// RefreshBatchStatus recomputes the counters and the state of a batch.
func (r *MediaBatchRepo) RefreshBatchStatus(ctx context.Context, id int64) (*biz.MediaBatch, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	batch, err := refreshBatchState(ctx, tx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toMediaBatch(batch), nil
}

// GetRevisionForMedia returns the revision a batch is built from.
func (r *MediaBatchRepo) GetRevisionForMedia(ctx context.Context, id int64) (*biz.ContentRevision, error) {
	row, err := r.client.ContentRevision.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionNotFound
		}
		return nil, err
	}
	return &biz.ContentRevision{
		ID:       row.ID,
		SeriesID: row.SeriesID,
		State:    row.State,
		Version:  int(row.Version),
		Title:    row.Title,
	}, nil
}

// ListSelectedEpisodes returns the selected episodes of a revision.
func (r *MediaBatchRepo) ListSelectedEpisodes(ctx context.Context, revisionID int64) ([]biz.MediaEpisodeTarget, error) {
	rows, err := r.client.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID), revisionepisode.SelectedEQ(true)).
		Order(ent.Asc(revisionepisode.FieldEpisodeNo)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	episodes := make([]biz.MediaEpisodeTarget, 0, len(rows))
	for _, row := range rows {
		episodes = append(episodes, biz.MediaEpisodeTarget{
			ID:         row.ID,
			ExternalID: textValue(row.ExternalID),
			Number:     int(row.EpisodeNo),
		})
	}
	return episodes, nil
}

// markBatchesRunning moves the batches of the claimed items to running.
func (r *MediaBatchRepo) markBatchesRunning(ctx context.Context, items []biz.MediaItem) (int, error) {
	seen := map[int64]struct{}{}
	for _, item := range items {
		seen[item.BatchID] = struct{}{}
	}
	for batchID := range seen {
		if _, err := r.client.MediaBatch.UpdateOneID(batchID).
			SetStatus(string(biz.MediaBatchRunning)).
			Save(ctx); err != nil {
			return 0, err
		}
	}
	return len(seen), nil
}

// checkLease rejects a commit from a worker that lost the item.
func checkLease(row *ent.MediaItem, item biz.MediaItem) error {
	if row.LeaseGeneration != item.LeaseGeneration || row.LeaseOwner != item.LeaseOwner {
		return biz.ErrItemLeaseLost
	}
	if row.Status == string(biz.MediaItemCancelled) || row.Status == string(biz.MediaItemReady) {
		return biz.ErrItemLeaseLost
	}
	// An expired lease is lost even when nobody took the item over yet: the
	// worker has to renew before the deadline, otherwise its write races
	// with the worker that takes the item over.
	if row.LeaseUntil != nil && !row.LeaseUntil.After(time.Now().UTC()) {
		return biz.ErrItemLeaseLost
	}
	return nil
}

// toMediaBatch maps a stored batch.
func toMediaBatch(row *ent.MediaBatch) *biz.MediaBatch {
	return &biz.MediaBatch{
		ID:              row.ID,
		SeriesID:        row.SeriesID,
		RevisionID:      row.RevisionID,
		SelectionHash:   row.SelectionHash,
		ProfileVersion:  row.ProfileVersion,
		InputKind:       row.InputKind,
		Status:          biz.MediaBatchStatus(row.Status),
		CoverStatus:     row.CoverStatus,
		ItemTotal:       row.ItemTotal,
		ItemReady:       row.ItemReady,
		ItemFailed:      row.ItemFailed,
		CancelRequested: row.CancelRequested,
		Version:         int(row.Version),
		ErrorCode:       row.ErrorCode,
		ErrorMessage:    row.ErrorMessage,
		CreatedBy:       row.CreatedBy,
		TraceID:         row.TraceID,
		ReviewTaskID:    row.ReviewTaskID,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

// toMediaItem maps a stored item.
func toMediaItem(row *ent.MediaItem) *biz.MediaItem {
	item := &biz.MediaItem{
		ID:                   row.ID,
		BatchID:              row.BatchID,
		EpisodeID:            row.EpisodeID,
		ExternalID:           row.ExternalID,
		EpisodeNo:            int(row.EpisodeNo),
		TraceID:              row.TraceID,
		Status:               biz.MediaItemStatus(row.Status),
		Attempt:              row.Attempt,
		Retryable:            row.Retryable,
		ErrorCode:            row.ErrorCode,
		ErrorMessage:         row.ErrorMessage,
		LeaseGeneration:      row.LeaseGeneration,
		LeaseOwner:           row.LeaseOwner,
		InputVersion:         row.InputVersion,
		InputUploadID:        row.InputUploadID,
		AssetID:              row.AssetID,
		InputArtifactPath:    row.InputArtifactPath,
		InputArtifactSHA256:  row.InputArtifactSha256,
		OutputArtifactPath:   row.OutputArtifactPath,
		OutputArtifactSHA256: row.OutputArtifactSha256,
		OutputArtifactSize:   row.OutputArtifactSize,
		SHA256:               row.Sha256,
		SizeBytes:            row.SizeBytes,
		ContentType:          row.ContentType,
		Codec:                row.Codec,
		DurationMS:           row.DurationMs,
		Width:                row.Width,
		Height:               row.Height,
		Version:              int(row.Version),
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
	if row.NextEligibleAt != nil {
		value := *row.NextEligibleAt
		item.NextEligibleAt = &value
	}
	if row.LeaseUntil != nil {
		value := *row.LeaseUntil
		item.LeaseUntil = &value
	}
	item.Object = biz.ObjectRef{
		Provider:  row.ObjectProvider,
		Bucket:    row.ObjectBucket,
		Region:    row.ObjectRegion,
		Key:       row.ObjectKey,
		VersionID: row.ObjectVersion,
	}
	return item
}
