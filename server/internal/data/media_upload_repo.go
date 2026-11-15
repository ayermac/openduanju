package data

import (
	"context"
	"fmt"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/mediaasset"
	"openduanju-server/internal/data/ent/mediabatch"
	"openduanju-server/internal/data/ent/revisionepisode"
	"openduanju-server/internal/data/ent/uploadsession"
)

// UploadRepo persists upload sessions and the input they register.
type UploadRepo struct {
	client *ent.Client
}

// NewUploadRepo builds the upload repository.
func NewUploadRepo(d *Data) *UploadRepo {
	return &UploadRepo{client: d.Ent()}
}

// NewUploadRepoWithDB builds the repository over an explicit client, for
// integration tests that own the database.
func NewUploadRepoWithDB(client *ent.Client) *UploadRepo {
	return &UploadRepo{client: client}
}

// CreateUpload opens one staging session. The revision row is locked, so a
// concurrent freeze or withdrawal is serialized against the session.
func (r *UploadRepo) CreateUpload(ctx context.Context, record biz.CreateUploadRecord) (*biz.UploadSession, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(record.RevisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionNotFound
		}
		return nil, err
	}
	if !biz.UploadAcceptsRevision(record.Kind, revision.State) {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: state %s", biz.ErrRevisionNotEditable, revision.State)
	}
	if biz.UploadChecksRevisionVersion(record.Kind) && int(revision.Version) != record.RevisionVersion {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: revision version %d, request %d", biz.ErrRevisionVersion, revision.Version, record.RevisionVersion)
	}
	if record.Kind == biz.UploadKindEpisode {
		exists, err := tx.RevisionEpisode.Query().
			Where(revisionepisode.RevisionIDEQ(record.RevisionID), revisionepisode.IDEQ(record.EpisodeID)).
			Exist(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if !exists {
			_ = tx.Rollback()
			return nil, biz.ErrUploadEpisodeInvalid
		}
	}
	// One pending session per target: a repeated create returns the same
	// staging object instead of scattering half-uploaded files.
	existing, err := tx.UploadSession.Query().
		Where(
			uploadsession.RevisionIDEQ(record.RevisionID),
			uploadsession.KindEQ(record.Kind),
			uploadsession.EpisodeIDEQ(record.EpisodeID),
			uploadsession.StatusEQ(biz.UploadPending),
			uploadsession.ExpiresAtGT(time.Now()),
		).
		Order(ent.Desc(uploadsession.FieldID)).
		First(ctx)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return toUploadSession(existing), nil
	}
	if !ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, err
	}
	created, err := tx.UploadSession.Create().
		SetRevisionID(record.RevisionID).
		SetEpisodeID(record.EpisodeID).
		SetKind(record.Kind).
		SetStatus(biz.UploadPending).
		SetProvider(record.Provider).
		SetBucket(record.Bucket).
		SetRegion(record.Region).
		SetStagingKey(record.StagingKey).
		SetFilename(record.Filename).
		SetContentType(record.ContentType).
		SetSizeBytes(record.SizeBytes).
		SetExpectedSha256(record.ExpectedSHA256).
		SetCreatedBy(record.Actor).
		SetRevisionVersion(record.RevisionVersion).
		SetExpiresAt(record.ExpiresAt).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toUploadSession(created), nil
}

// GetUpload returns one session.
func (r *UploadRepo) GetUpload(ctx context.Context, id int64) (*biz.UploadSession, error) {
	row, err := r.client.UploadSession.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrUploadNotFound
		}
		return nil, err
	}
	return toUploadSession(row), nil
}

// ListUploads returns a page of sessions of a revision.
func (r *UploadRepo) ListUploads(ctx context.Context, query biz.UploadQuery) ([]biz.UploadSession, int, error) {
	page, size := mediaPage(query.Page, query.PageSize)
	q := r.client.UploadSession.Query()
	if query.RevisionID > 0 {
		q = q.Where(uploadsession.RevisionIDEQ(query.RevisionID))
	}
	if query.Kind != "" {
		q = q.Where(uploadsession.KindEQ(query.Kind))
	}
	if query.Status != "" {
		q = q.Where(uploadsession.StatusEQ(query.Status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(uploadsession.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.UploadSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, *toUploadSession(row))
	}
	return out, total, nil
}

// RenewUpload extends one pending session. The revision has to be the draft
// the session was opened for; a changed selection or a freeze rejects it.
func (r *UploadRepo) RenewUpload(ctx context.Context, id int64, expectedVersion int, actor string, expiresAt time.Time) (*biz.UploadSession, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	session, err := tx.UploadSession.Query().
		Where(uploadsession.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrUploadNotFound
		}
		return nil, err
	}
	if err := checkUploadWritable(session, expectedVersion, actor); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if time.Now().After(session.ExpiresAt) {
		_ = tx.Rollback()
		return nil, biz.ErrUploadExpired
	}
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(session.RevisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if !biz.UploadAcceptsRevision(session.Kind, revision.State) ||
		(biz.UploadChecksRevisionVersion(session.Kind) && int(revision.Version) != session.RevisionVersion) {
		_ = tx.Rollback()
		return nil, fmt.Errorf("%w: revision is %s at version %d", biz.ErrUploadRevisionChanged, revision.State, revision.Version)
	}
	updated, err := tx.UploadSession.UpdateOneID(id).
		SetExpiresAt(expiresAt).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toUploadSession(updated), nil
}

// CompleteUploadInput registers the confirmed object as the input of one
// episode. The revision episode keeps the session id, so a later batch can
// freeze the same object identity.
func (r *UploadRepo) CompleteUploadInput(ctx context.Context, sessionID int64, expectedVersion int, actor string, object biz.ObjectRef, sha256 string, sizeBytes int64, contentType string) (*biz.UploadSession, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	session, err := tx.UploadSession.Query().
		Where(uploadsession.IDEQ(sessionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrUploadNotFound
		}
		return nil, err
	}
	if session.Status == biz.UploadCompleted {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return toUploadSession(session), nil
	}
	if err := checkUploadWritable(session, expectedVersion, actor); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if session.Kind != biz.UploadKindEpisode {
		_ = tx.Rollback()
		return nil, biz.ErrUploadKindInvalid
	}
	// The revision must still accept this input at commit time, not only when
	// the caller started to verify the object.
	if _, err := lockUploadRevision(ctx, tx, session); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	now := time.Now()
	// The confirmed object identity is recorded with the input, so a task
	// reads that version even after the same key is written again.
	updated, err := tx.UploadSession.UpdateOneID(sessionID).
		SetStatus(biz.UploadCompleted).
		SetSha256(sha256).
		SetSizeBytes(sizeBytes).
		SetContentType(contentType).
		SetProvider(object.Provider).
		SetBucket(object.Bucket).
		SetRegion(object.Region).
		SetObjectKey(object.Key).
		SetObjectVersion(object.VersionID).
		SetCompletedAt(now).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	result, err := tx.RevisionEpisode.Update().
		Where(revisionepisode.RevisionIDEQ(session.RevisionID), revisionepisode.IDEQ(session.EpisodeID)).
		SetInputKind("manual").
		SetInputRef(session.ID).
		SetInputState(biz.InputStateReady).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if result == 0 {
		_ = tx.Rollback()
		return nil, biz.ErrUploadEpisodeInvalid
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toUploadSession(updated), nil
}

// CompleteUploadCover stores the confirmed cover asset, binds it to the
// revision and opens the review gate when the videos are already ready. A
// cover never enters the episode counters.
func (r *UploadRepo) CompleteUploadCover(ctx context.Context, record biz.CompleteCoverRecord, actor string) (*biz.UploadSession, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	session, err := tx.UploadSession.Query().
		Where(uploadsession.IDEQ(record.SessionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrUploadNotFound
		}
		return nil, err
	}
	if session.Status == biz.UploadCompleted {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return toUploadSession(session), nil
	}
	if err := checkUploadWritable(session, record.ExpectedVersion, actor); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if session.Kind != biz.UploadKindCover {
		_ = tx.Rollback()
		return nil, biz.ErrUploadKindInvalid
	}
	// The cover is validated outside this transaction. A revision that was
	// withdrawn or sent to review while the image was decoded must not accept
	// the cover, and its existing cover and review task stay untouched.
	if _, err := lockUploadRevision(ctx, tx, session); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	asset, err := createOrUpdateCoverAsset(ctx, tx, session.RevisionID, record)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	now := time.Now()
	updated, err := tx.UploadSession.UpdateOneID(session.ID).
		SetStatus(biz.UploadCompleted).
		SetSha256(record.SHA256).
		SetSizeBytes(record.SizeBytes).
		SetContentType(record.ContentType).
		SetWidth(record.Width).
		SetHeight(record.Height).
		SetAssetID(asset.ID).
		SetObjectKey(record.Object.Key).
		SetObjectVersion(record.Object.VersionID).
		SetCompletedAt(now).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.ContentRevision.UpdateOneID(session.RevisionID).
		SetCoverAssetID(asset.ID).
		AddVersion(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// A batch may already exist - the cover is often uploaded after the
	// videos. Marking it ready re-evaluates the gate in the same transaction
	// so the review task does not wait for the next worker tick.
	batch, err := tx.MediaBatch.Query().
		Where(mediabatch.RevisionIDEQ(session.RevisionID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, err
	}
	if err == nil && batch.CoverStatus != biz.MediaCoverReady {
		if _, err := tx.MediaBatch.UpdateOneID(batch.ID).
			SetCoverStatus(biz.MediaCoverReady).
			AddVersion(1).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if _, err := refreshBatchState(ctx, tx, batch.ID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toUploadSession(updated), nil
}

// CancelUpload releases a pending session. A completed session already
// produced a bound input and may not be cancelled.
func (r *UploadRepo) CancelUpload(ctx context.Context, id int64, expectedVersion int, actor string) (*biz.UploadSession, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	session, err := tx.UploadSession.Query().
		Where(uploadsession.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrUploadNotFound
		}
		return nil, err
	}
	if session.Status == biz.UploadCancelled {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return toUploadSession(session), nil
	}
	if session.Status == biz.UploadCompleted {
		_ = tx.Rollback()
		return nil, biz.ErrUploadAlreadyBound
	}
	if err := checkUploadWritable(session, expectedVersion, actor); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	updated, err := tx.UploadSession.UpdateOneID(id).
		SetStatus(biz.UploadCancelled).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return toUploadSession(updated), nil
}

// GetCompletedInput returns the confirmed object of one episode upload. A
// task freezes the session id, so a later cancel or a different revision
// cannot change the bytes it processes.
func (r *UploadRepo) GetCompletedInput(ctx context.Context, sessionID int64) (*biz.CompletedInput, error) {
	row, err := r.client.UploadSession.Get(ctx, sessionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrUploadNotFound
		}
		return nil, err
	}
	if row.Status != biz.UploadCompleted || row.Kind != biz.UploadKindEpisode {
		return nil, fmt.Errorf("%w: status %s kind %s", biz.ErrUploadNotPending, row.Status, row.Kind)
	}
	// The confirmed identity wins over the staging key: an object may have
	// been overwritten under the same key after the confirmation.
	key := row.ObjectKey
	if key == "" {
		key = row.StagingKey
	}
	input := &biz.CompletedInput{
		SessionID:  row.ID,
		RevisionID: row.RevisionID,
		EpisodeID:  row.EpisodeID,
		Kind:       row.Kind,
		Object: biz.ObjectRef{
			Provider:  row.Provider,
			Bucket:    row.Bucket,
			Region:    row.Region,
			Key:       key,
			VersionID: row.ObjectVersion,
		},
		SizeBytes: row.SizeBytes,
		SHA256:    row.Sha256,
		Status:    row.Status,
	}
	// The episode identity lets a task of a copied episode recognise the
	// upload it inherited. A missing row is reported as zero values: the
	// caller then refuses the input instead of reading it.
	if revision, err := r.client.ContentRevision.Get(ctx, row.RevisionID); err == nil {
		input.SeriesID = revision.SeriesID
	}
	if row.EpisodeID > 0 {
		if episode, err := r.client.RevisionEpisode.Get(ctx, row.EpisodeID); err == nil {
			input.EpisodeNo = int(episode.EpisodeNo)
			input.ExternalID = textValue(episode.ExternalID)
		}
	}
	return input, nil
}

// checkUploadWritable validates the common guards of a mutating call.
func checkUploadWritable(session *ent.UploadSession, expectedVersion int, actor string) error {
	if session.Status != biz.UploadPending {
		return fmt.Errorf("%w: status %s", biz.ErrUploadNotPending, session.Status)
	}
	if expectedVersion > 0 && session.Version != expectedVersion {
		return fmt.Errorf("%w: session version %d, request %d", biz.ErrUploadVersionRequired, session.Version, expectedVersion)
	}
	if session.CreatedBy != "" && actor != "" && session.CreatedBy != actor {
		return biz.ErrUploadActorNotOwner
	}
	return nil
}

// lockUploadRevision locks the revision of a session inside the write
// transaction and applies the state gate again.
//
// The caller checks the revision before it downloads and validates the bytes.
// That check is not enough: the revision can be withdrawn, frozen or sent to
// review while the confirmation runs. The row lock makes the write and the
// state it depends on atomic, so a confirmation that lost its revision is
// rejected instead of changing the cover or the input of a revision that no
// longer expects it.
func lockUploadRevision(ctx context.Context, tx *ent.Tx, session *ent.UploadSession) (*ent.ContentRevision, error) {
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(session.RevisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionNotFound
		}
		return nil, err
	}
	if !biz.UploadAcceptsRevision(session.Kind, revision.State) {
		return nil, fmt.Errorf("%w: state %s", biz.ErrUploadRevisionChanged, revision.State)
	}
	// A video input is tied to the exact revision version. A cover is not:
	// freezing the revision advances its version, and the cover an operator is
	// confirming is still the cover of that revision. The state gate above is
	// what keeps a cover out of a withdrawn or already reviewed revision.
	if biz.UploadChecksRevisionVersion(session.Kind) && int(revision.Version) != session.RevisionVersion {
		return nil, fmt.Errorf("%w: revision version %d, session version %d", biz.ErrUploadRevisionChanged, revision.Version, session.RevisionVersion)
	}
	return revision, nil
}

// createOrUpdateCoverAsset writes the cover as a ready asset of the revision.
func createOrUpdateCoverAsset(ctx context.Context, tx *ent.Tx, revisionID int64, record biz.CompleteCoverRecord) (*ent.MediaAsset, error) {
	existing, err := tx.MediaAsset.Query().
		Where(
			mediaasset.ProviderEQ(record.Object.Provider),
			mediaasset.BucketEQ(record.Object.Bucket),
			mediaasset.RegionEQ(record.Object.Region),
			mediaasset.ObjectKeyEQ(record.Object.Key),
			mediaasset.ObjectVersionEQ(record.Object.VersionID),
		).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		return tx.MediaAsset.UpdateOneID(existing.ID).
			SetState(string(biz.MediaAssetReady)).
			SetKind("cover").
			SetRevisionID(revisionID).
			SetSha256(record.SHA256).
			SetSizeBytes(record.SizeBytes).
			SetContentType(record.ContentType).
			SetWidth(record.Width).
			SetHeight(record.Height).
			Save(ctx)
	}
	return tx.MediaAsset.Create().
		SetProvider(record.Object.Provider).
		SetBucket(record.Object.Bucket).
		SetRegion(record.Object.Region).
		SetObjectKey(record.Object.Key).
		SetObjectVersion(record.Object.VersionID).
		SetSha256(record.SHA256).
		SetSizeBytes(record.SizeBytes).
		SetContentType(record.ContentType).
		SetWidth(record.Width).
		SetHeight(record.Height).
		SetState(string(biz.MediaAssetReady)).
		SetKind("cover").
		SetRevisionID(revisionID).
		Save(ctx)
}

// toUploadSession maps the entity to the domain type.
func toUploadSession(row *ent.UploadSession) *biz.UploadSession {
	if row == nil {
		return nil
	}
	out := &biz.UploadSession{
		ID:              row.ID,
		RevisionID:      row.RevisionID,
		EpisodeID:       row.EpisodeID,
		Kind:            row.Kind,
		Status:          row.Status,
		Version:         row.Version,
		Provider:        row.Provider,
		Bucket:          row.Bucket,
		Region:          row.Region,
		StagingKey:      row.StagingKey,
		ObjectKey:       row.ObjectKey,
		ObjectVersion:   row.ObjectVersion,
		Filename:        row.Filename,
		ContentType:     row.ContentType,
		SizeBytes:       row.SizeBytes,
		ExpectedSHA256:  row.ExpectedSha256,
		SHA256:          row.Sha256,
		Width:           row.Width,
		Height:          row.Height,
		AssetID:         row.AssetID,
		CreatedBy:       row.CreatedBy,
		RevisionVersion: row.RevisionVersion,
		ExpiresAt:       row.ExpiresAt,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
	if row.CompletedAt != nil {
		completed := *row.CompletedAt
		out.CompletedAt = &completed
	}
	return out
}

// GetRevisionForUpload returns the revision state and version for a session.
func (r *UploadRepo) GetRevisionForUpload(ctx context.Context, revisionID int64) (*biz.ContentRevision, error) {
	row, err := r.client.ContentRevision.Get(ctx, revisionID)
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

// HasRevisionEpisode reports whether an episode belongs to a revision.
func (r *UploadRepo) HasRevisionEpisode(ctx context.Context, revisionID int64, episodeID int64) (bool, error) {
	return r.client.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID), revisionepisode.IDEQ(episodeID)).
		Exist(ctx)
}
