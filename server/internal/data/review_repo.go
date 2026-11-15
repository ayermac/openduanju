package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/authorizationgrant"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/mediaasset"
	"openduanju-server/internal/data/ent/mediabatch"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/publication"
	"openduanju-server/internal/data/ent/reviewdecision"
	"openduanju-server/internal/data/ent/reviewtask"
)

// ReviewRepo persists review tasks and publications.
type ReviewRepo struct {
	client *ent.Client
}

// NewReviewRepo builds the repository.
func NewReviewRepo(d *Data) *ReviewRepo {
	return &ReviewRepo{client: d.Ent()}
}

// NewReviewRepoWithDB builds the repository over an explicit client.
func NewReviewRepoWithDB(client *ent.Client) *ReviewRepo {
	return &ReviewRepo{client: client}
}

// ListReviews returns a page of review tasks with the counters the worklist
// shows.
func (r *ReviewRepo) ListReviews(ctx context.Context, query biz.ReviewQuery) ([]biz.ReviewCase, int, error) {
	page, size := mediaPage(query.Page, query.PageSize)
	q := r.client.ReviewTask.Query()
	if query.Status != "" {
		q = q.Where(reviewtask.StatusEQ(query.Status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(reviewtask.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.ReviewCase, 0, len(rows))
	for _, row := range rows {
		out = append(out, *r.toCase(ctx, row))
	}
	return out, total, nil
}

// GetReview returns the review screen payload.
func (r *ReviewRepo) GetReview(ctx context.Context, taskID int64) (*biz.ReviewDetail, error) {
	row, err := r.client.ReviewTask.Get(ctx, taskID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrReviewNotFound
		}
		return nil, err
	}
	detail := &biz.ReviewDetail{Case: *r.toCase(ctx, row)}
	batch, err := r.client.MediaBatch.Get(ctx, row.BatchID)
	if err != nil {
		return nil, err
	}
	revision, err := r.client.ContentRevision.Get(ctx, row.RevisionID)
	if err != nil {
		return nil, err
	}
	items, err := r.client.MediaItem.Query().
		Where(mediaitem.BatchIDEQ(batch.ID)).
		Order(ent.Asc(mediaitem.FieldEpisodeNo)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		episode := biz.ReviewEpisode{EpisodeID: item.EpisodeID, Number: item.EpisodeNo, ExternalID: item.ExternalID}
		if item.AssetID > 0 {
			episode.Asset = r.loadAsset(ctx, item.AssetID)
		}
		detail.Episodes = append(detail.Episodes, episode)
	}
	if revision.CoverAssetID > 0 {
		detail.Cover = r.loadAsset(ctx, revision.CoverAssetID)
	}
	if revision.AuthorizationGrantID > 0 {
		grant, err := r.client.AuthorizationGrant.Get(ctx, revision.AuthorizationGrantID)
		if err == nil {
			detail.Authorization = toAuthorizationGrant(grant)
		}
	}
	decision, err := r.client.ReviewDecision.Query().
		Where(reviewdecision.ReviewTaskIDEQ(taskID)).
		Only(ctx)
	if err == nil {
		detail.Decision = toReviewDecision(decision)
	} else if !ent.IsNotFound(err) {
		return nil, err
	}
	return detail, nil
}

// ClaimReview assigns an open task to one reviewer under a version CAS.
func (r *ReviewRepo) ClaimReview(ctx context.Context, taskID int64, expectedVersion int, actor string) (*biz.ReviewCase, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.ReviewTask.Query().Where(reviewtask.IDEQ(taskID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrReviewNotFound
		}
		return nil, err
	}
	// Repeating the same claim is idempotent; a claim by someone else is a
	// conflict.
	if row.Status == biz.ReviewClaimed && row.Assignee == actor {
		// The task was already claimed by this reviewer: nothing changed,
		// so the repeat is not recorded as a second claim.
		biz.AuditUnchanged(ctx)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return r.toCase(ctx, row), nil
	}
	if row.Status != biz.ReviewOpen {
		_ = tx.Rollback()
		return nil, biz.ErrReviewNotOpen
	}
	if row.Version != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrReviewVersion
	}
	if row.SubmittedBy != "" && row.SubmittedBy == actor {
		_ = tx.Rollback()
		return nil, biz.ErrReviewSelfDecision
	}
	updated, err := tx.ReviewTask.UpdateOneID(taskID).
		SetStatus(biz.ReviewClaimed).
		SetAssignee(actor).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.toCase(ctx, updated), nil
}

// DecideReview writes the single decision of a task. The approval path
// re-checks the revision, every selected material, the cover and the frozen
// authorization inside the transaction.
func (r *ReviewRepo) DecideReview(ctx context.Context, request biz.DecideReviewRequest) (*biz.ReviewCase, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.ReviewTask.Query().Where(reviewtask.IDEQ(request.TaskID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrReviewNotFound
		}
		return nil, err
	}
	if row.Status == biz.ReviewApproved || row.Status == biz.ReviewRejected {
		_ = tx.Rollback()
		return nil, biz.ErrReviewFinal
	}
	if row.Status != biz.ReviewClaimed {
		_ = tx.Rollback()
		return nil, biz.ErrReviewNotClaimed
	}
	if row.Assignee != request.Actor {
		_ = tx.Rollback()
		return nil, biz.ErrReviewNotAssignee
	}
	if row.SubmittedBy != "" && row.SubmittedBy == request.Actor {
		_ = tx.Rollback()
		return nil, biz.ErrReviewSelfDecision
	}
	if row.Version != request.ExpectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrReviewVersion
	}
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(row.RevisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	authorizationGrantID := int64(0)
	authorizationVersion := 0
	authorizationHash := ""
	if request.Decision == biz.DecisionApprove {
		if revision.State != biz.RevisionStatePendingReview {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: revision state %s", biz.ErrReviewPrecondition, revision.State)
		}
		batch, err := tx.MediaBatch.Query().Where(mediabatch.IDEQ(row.BatchID)).Only(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if batch.ItemTotal <= 0 || batch.ItemReady != batch.ItemTotal || batch.CoverStatus != biz.MediaCoverReady {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: material %d/%d cover %s", biz.ErrReviewPrecondition, batch.ItemReady, batch.ItemTotal, batch.CoverStatus)
		}
		ready, err := tx.MediaItem.Query().
			Where(mediaitem.BatchIDEQ(batch.ID), mediaitem.StatusEQ(string(biz.MediaItemReady))).
			Count(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if ready != batch.ItemTotal {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: %d of %d materials are ready", biz.ErrReviewPrecondition, ready, batch.ItemTotal)
		}
		// The reviewer cannot claim to have watched more episodes than the
		// batch holds.
		if request.Checklist.ReviewedEpisodes > batch.ItemTotal {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: reviewed %d of %d episodes", biz.ErrReviewChecklist, request.Checklist.ReviewedEpisodes, batch.ItemTotal)
		}
		// The frozen authorization is checked now: a revocation during the
		// review must not produce an approval.
		if revision.AuthorizationGrantID == 0 {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: no frozen authorization", biz.ErrReviewPrecondition)
		}
		grantRow, err := tx.AuthorizationGrant.Query().
			Where(authorizationgrant.IDEQ(revision.AuthorizationGrantID)).
			ForUpdate().
			Only(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: authorization missing", biz.ErrReviewPrecondition)
		}
		grant := toAuthorizationGrant(grantRow)
		now := time.Now().UTC()
		if err := biz.EnsureForReview(grant, revision.SeriesID, now); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("%w: %v", biz.ErrReviewPrecondition, err)
		}
		authorizationGrantID = grantRow.ID
		authorizationVersion = grantRow.Version
		// Keep the fingerprint frozen at submission; fall back to the current
		// one only for a revision that predates it.
		authorizationHash = revision.AuthorizationHash
		if authorizationHash == "" {
			authorizationHash = grant.Fingerprint()
		}
	}
	now := time.Now().UTC()
	if _, err := tx.ReviewDecision.Create().
		SetReviewTaskID(row.ID).
		SetDecision(request.Decision).
		SetReason(request.Reason).
		SetDecidedBy(request.Actor).
		SetDecidedAt(now).
		SetTaskVersion(row.Version).
		SetAuthorizationGrantID(authorizationGrantID).
		SetAuthorizationVersion(authorizationVersion).
		SetAuthorizationHash(authorizationHash).
		SetChecklist(checklistSnapshot(request)).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// A rejection is a final judgement of the material that was reviewed, so
	// the revision keeps that material and becomes history: it is not an
	// editable draft any more, and the operator creates a new draft from it.
	status := biz.ReviewRejected
	revisionState := biz.RevisionStateRejected
	if request.Decision == biz.DecisionApprove {
		status = biz.ReviewApproved
		// Approval never publishes: the revision becomes approved and waits
		// for an explicit publication.
		revisionState = biz.RevisionStateApproved
	}
	if _, err := tx.ContentRevision.UpdateOneID(revision.ID).
		SetState(revisionState).
		AddVersion(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	updated, err := tx.ReviewTask.UpdateOneID(row.ID).
		SetStatus(status).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.toCase(ctx, updated), nil
}

// PublishRevision makes an approved revision the published one and freezes
// the reference. The previous publication of the series is retired, never
// rewritten.
func (r *ReviewRepo) PublishRevision(ctx context.Context, seriesID int64, revisionID int64, actor string) (*biz.Publication, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(revisionID), contentrevision.SeriesIDEQ(seriesID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: revision not found", biz.ErrReviewNotApproved)
	}
	if revision.State != biz.RevisionStateApproved {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: revision state %s", biz.ErrReviewNotApproved, revision.State)
	}
	grantRow, err := tx.AuthorizationGrant.Query().
		Where(authorizationgrant.IDEQ(revision.AuthorizationGrantID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: authorization missing", biz.ErrReviewPrecondition)
	}
	grant := toAuthorizationGrant(grantRow)
	now := time.Now().UTC()
	if err := biz.EnsureForPublish(grant, seriesID, now); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: %v", biz.ErrReviewPrecondition, err)
	}
	// A repeat of the same publication is idempotent.
	if existing, err := tx.Publication.Query().
		Where(publication.RevisionIDEQ(revisionID), publication.StatusEQ(biz.PublicationPublished)).
		Only(ctx); err == nil {
		// Already published: the repeat did not put anything live.
		biz.AuditUnchanged(ctx)
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return toPublication(existing), nil
	}
	// The category is frozen with the publication, so a later edit of the
	// series does not change what the public list shows.
	seriesRow, err := tx.ContentSeries.Get(ctx, seriesID)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	category := seriesRow.Category
	// A revision that was taken down keeps its history row: publishing it
	// again reopens that row instead of writing a second one. The generation
	// grows, so playback sessions of the old run stay invalid.
	var reopened *ent.Publication
	if existing, err := tx.Publication.Query().
		Where(publication.RevisionIDEQ(revisionID)).
		Only(ctx); err == nil {
		row, err := tx.Publication.UpdateOneID(existing.ID).
			SetStatus(biz.PublicationPublished).
			SetPublishedBy(actor).
			SetPublishedAt(now).
			SetUnpublishedBy("").
			ClearUnpublishedAt().
			SetAuthorizationGrantID(grantRow.ID).
			SetAuthorizationVersion(grantRow.Version).
			SetAuthorizationHash(publicationHash(revision.AuthorizationHash, grant)).
			SetCategory(category).
			AddGeneration(1).
			AddVersion(1).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		reopened = row
	}
	// Retire the current publication of the series.
	if _, err := tx.Publication.Update().
		Where(
			publication.SeriesIDEQ(seriesID),
			publication.StatusEQ(biz.PublicationPublished),
			publication.RevisionIDNEQ(revisionID),
		).
		SetStatus(biz.PublicationUnpublished).
		SetUnpublishedBy(actor).
		SetUnpublishedAt(now).
		AddVersion(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	created := reopened
	if created == nil {
		row, err := tx.Publication.Create().
			SetSeriesID(seriesID).
			SetRevisionID(revisionID).
			SetStatus(biz.PublicationPublished).
			SetPublishedBy(actor).
			SetPublishedAt(now).
			SetAuthorizationGrantID(grantRow.ID).
			SetAuthorizationVersion(grantRow.Version).
			SetAuthorizationHash(publicationHash(revision.AuthorizationHash, grant)).
			SetCategory(category).
			SetGeneration(1).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		created = row
	}
	if _, err := tx.ContentSeries.UpdateOneID(seriesID).
		SetPublishedRevisionID(revisionID).
		AddRevision(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, created.ID, int(created.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toPublication(created), nil
}

// UnpublishSeries takes the current publication down under a version CAS.
func (r *ReviewRepo) UnpublishSeries(ctx context.Context, seriesID int64, expectedVersion int, actor string) (*biz.Publication, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.Publication.Query().
		Where(publication.SeriesIDEQ(seriesID), publication.StatusEQ(biz.PublicationPublished)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, biz.ErrPublicationMissing
	}
	if row.Version != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrPublicationVersion
	}
	now := time.Now().UTC()
	updated, err := tx.Publication.UpdateOneID(row.ID).
		SetStatus(biz.PublicationUnpublished).
		SetUnpublishedBy(actor).
		SetUnpublishedAt(now).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.ContentSeries.UpdateOneID(seriesID).
		SetPublishedRevisionID(0).
		AddRevision(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toPublication(updated), nil
}

// GetPublication returns the latest publication of a series.
func (r *ReviewRepo) GetPublication(ctx context.Context, seriesID int64) (*biz.Publication, error) {
	row, err := r.client.Publication.Query().
		Where(publication.SeriesIDEQ(seriesID)).
		Order(ent.Desc(publication.FieldID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrPublicationMissing
		}
		return nil, err
	}
	return toPublication(row), nil
}

// toCase builds the worklist row with the counters the screen shows.
func (r *ReviewRepo) toCase(ctx context.Context, row *ent.ReviewTask) *biz.ReviewCase {
	out := &biz.ReviewCase{
		TaskID:      row.ID,
		RevisionID:  row.RevisionID,
		SeriesID:    row.SeriesID,
		BatchID:     row.BatchID,
		Status:      row.Status,
		Assignee:    row.Assignee,
		SubmittedBy: row.SubmittedBy,
		Version:     row.Version,
		CreatedAt:   row.CreatedAt.UTC(),
	}
	if series, err := r.client.ContentSeries.Get(ctx, row.SeriesID); err == nil {
		out.SeriesTitle = series.Title
	}
	if batch, err := r.client.MediaBatch.Get(ctx, row.BatchID); err == nil {
		out.ItemTotal = batch.ItemTotal
		out.ItemReady = batch.ItemReady
		out.CoverStatus = batch.CoverStatus
	}
	return out
}

// loadAsset reads one stored material for preview.
func (r *ReviewRepo) loadAsset(ctx context.Context, assetID int64) *biz.ReviewAsset {
	row, err := r.client.MediaAsset.Query().Where(mediaasset.IDEQ(assetID)).Only(ctx)
	if err != nil {
		return nil
	}
	return &biz.ReviewAsset{
		AssetID: row.ID,
		Object: biz.ObjectRef{
			Provider:  row.Provider,
			Bucket:    row.Bucket,
			Region:    row.Region,
			Key:       row.ObjectKey,
			VersionID: row.ObjectVersion,
		},
		ContentType: row.ContentType,
		SizeBytes:   row.SizeBytes,
		SHA256:      row.Sha256,
		DurationMS:  row.DurationMs,
		Width:       row.Width,
		Height:      row.Height,
	}
}

// toReviewDecision maps the entity to the domain type.
func toReviewDecision(row *ent.ReviewDecision) *biz.ReviewDecision {
	if row == nil {
		return nil
	}
	return &biz.ReviewDecision{
		ID:                   row.ID,
		TaskID:               row.ReviewTaskID,
		Decision:             row.Decision,
		Reason:               row.Reason,
		DecidedBy:            row.DecidedBy,
		DecidedAt:            row.DecidedAt.UTC(),
		TaskVersion:          row.TaskVersion,
		AuthorizationGrantID: row.AuthorizationGrantID,
		AuthorizationVersion: row.AuthorizationVersion,
		AuthorizationHash:    row.AuthorizationHash,
	}
}

// toPublication maps the entity to the domain type.
func toPublication(row *ent.Publication) *biz.Publication {
	if row == nil {
		return nil
	}
	out := &biz.Publication{
		ID:                   row.ID,
		SeriesID:             row.SeriesID,
		RevisionID:           row.RevisionID,
		Status:               row.Status,
		PublishedBy:          row.PublishedBy,
		PublishedAt:          row.PublishedAt.UTC(),
		UnpublishedBy:        row.UnpublishedBy,
		AuthorizationGrantID: row.AuthorizationGrantID,
		AuthorizationVersion: row.AuthorizationVersion,
		Version:              row.Version,
	}
	if row.UnpublishedAt != nil {
		at := row.UnpublishedAt.UTC()
		out.UnpublishedAt = &at
	}
	return out
}

// compile-time guard.
var _ biz.ReviewRepo = (*ReviewRepo)(nil)

// checklistSnapshot serializes the confirmed items for the decision row.
func checklistSnapshot(request biz.DecideReviewRequest) string {
	if request.Decision != biz.DecisionApprove {
		return ""
	}
	body, err := json.Marshal(map[string]any{
		"title_cover_intro":    request.Checklist.TitleCoverIntro,
		"episode_mapping":      request.Checklist.EpisodeMapping,
		"audio_video":          request.Checklist.AudioVideo,
		"authorization_public": request.Checklist.AuthorizationPublic,
		"reviewed_episodes":    request.Checklist.ReviewedEpisodes,
	})
	if err != nil {
		return ""
	}
	return string(body)
}

// publicationHash keeps the frozen authorization fingerprint.
func publicationHash(frozen string, grant *biz.AuthorizationGrant) string {
	if frozen != "" {
		return frozen
	}
	return grant.Fingerprint()
}
