package data_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/reviewtask"
	"openduanju-server/internal/data/ent/uploadsession"
)

// uploadRecord builds a staging record for one episode of a revision.
func uploadRecord(revisionID int64, episodeID int64, key string, expiresAt time.Time) biz.CreateUploadRecord {
	return biz.CreateUploadRecord{
		RevisionID:      revisionID,
		EpisodeID:       episodeID,
		Kind:            biz.UploadKindEpisode,
		Filename:        "episode.mp4",
		ContentType:     "video/mp4",
		SizeBytes:       1024,
		StagingKey:      key,
		Provider:        "local",
		Bucket:          "local-bucket",
		Region:          "local",
		Actor:           "shared-token",
		RevisionVersion: 1,
		ExpiresAt:       expiresAt,
	}
}

func TestUploadCreateIsIdempotentAndGuardsTheDraft(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 2)
	episodes, err := client.RevisionEpisode.Query().All(context.Background())
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	first, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/1/one", time.Now().Add(10*time.Minute)))
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	// The same target returns the same staging session: a second create does
	// not scatter a second half-uploaded object.
	second, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/1/two", time.Now().Add(10*time.Minute)))
	if err != nil {
		t.Fatalf("repeat create: %v", err)
	}
	if second.ID != first.ID || second.StagingKey != first.StagingKey {
		t.Fatalf("repeat create opened a new session: %d/%s vs %d/%s", second.ID, second.StagingKey, first.ID, first.StagingKey)
	}
	// An episode that does not belong to the revision is rejected.
	if _, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, 99999, "uploads/1/other", time.Now().Add(time.Minute))); !errors.Is(err, biz.ErrUploadEpisodeInvalid) {
		t.Fatalf("foreign episode: %v", err)
	}
	// A frozen revision cannot open a video session.
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).SetState("processing").Save(ctx); err != nil {
		t.Fatalf("freeze revision: %v", err)
	}
	if _, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/1/three", time.Now().Add(time.Minute))); !errors.Is(err, biz.ErrRevisionNotEditable) {
		t.Fatalf("frozen revision: %v", err)
	}
}

func TestUploadRenewKeepsTheSameObjectAndRejectsExpiry(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/2/one", time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	renewed, err := repo.RenewUpload(ctx, session.ID, session.Version, "shared-token", time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.StagingKey != session.StagingKey {
		t.Fatalf("renew changed the staging key: %s", renewed.StagingKey)
	}
	if renewed.Version != session.Version+1 {
		t.Fatalf("renew version: %d", renewed.Version)
	}
	// A stale version cannot renew again.
	if _, err := repo.RenewUpload(ctx, session.ID, session.Version, "shared-token", time.Now().Add(time.Minute)); err == nil {
		t.Fatal("a stale session version renewed the upload")
	}
	// An expired session is rejected.
	if _, err := client.UploadSession.UpdateOneID(session.ID).
		SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := repo.RenewUpload(ctx, session.ID, renewed.Version, "shared-token", time.Now().Add(time.Minute)); !errors.Is(err, biz.ErrUploadExpired) {
		t.Fatalf("expired renew: %v", err)
	}
}

func TestUploadCompleteInputRegistersTheRevisionEpisode(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/3/one", time.Now().Add(10*time.Minute)))
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	completed, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token", confirmedObject(), "abc123", 1024, "video/mp4")
	if err != nil {
		t.Fatalf("complete input: %v", err)
	}
	if completed.Status != biz.UploadCompleted {
		t.Fatalf("status: %s", completed.Status)
	}
	episode, err := client.RevisionEpisode.Get(ctx, episodes[0].ID)
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if episode.InputKind != "manual" || episode.InputRef != session.ID || episode.InputState != biz.InputStateReady {
		t.Fatalf("episode input: kind=%s ref=%d state=%s", episode.InputKind, episode.InputRef, episode.InputState)
	}
	// A repeated confirmation binds nothing new.
	again, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token", confirmedObject(), "abc123", 1024, "video/mp4")
	if err != nil {
		t.Fatalf("repeat complete: %v", err)
	}
	if again.Version != completed.Version {
		t.Fatalf("repeat complete advanced the version: %d", again.Version)
	}
	// The frozen input is readable by the worker.
	input, err := repo.GetCompletedInput(ctx, session.ID)
	if err != nil {
		t.Fatalf("get completed input: %v", err)
	}
	if input.EpisodeID != episodes[0].ID || input.SizeBytes != 1024 {
		t.Fatalf("completed input: %+v", input)
	}
	// A cancelled session may not be completed.
	other, _ := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/3/two", time.Now().Add(10*time.Minute)))
	if _, err := client.UploadSession.UpdateOneID(other.ID).SetStatus(biz.UploadCancelled).Save(ctx); err != nil {
		t.Fatalf("cancel session: %v", err)
	}
	if _, err := repo.CompleteUploadInput(ctx, other.ID, other.Version, "shared-token", confirmedObject(), "abc", 10, "video/mp4"); !errors.Is(err, biz.ErrUploadNotPending) {
		t.Fatalf("complete cancelled session: %v", err)
	}
}

func TestUploadCancelRefusesABoundSession(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	session, _ := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/4/one", time.Now().Add(time.Minute)))
	if _, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token", confirmedObject(), "abc", 1024, "video/mp4"); err != nil {
		t.Fatalf("complete input: %v", err)
	}
	if _, err := repo.CancelUpload(ctx, session.ID, session.Version+1, "shared-token"); !errors.Is(err, biz.ErrUploadAlreadyBound) {
		t.Fatalf("cancel bound session: %v", err)
	}
	// A pending session cancels cleanly and repeats idempotently.
	pending, _ := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/4/two", time.Now().Add(time.Minute)))
	cancelled, err := repo.CancelUpload(ctx, pending.ID, pending.Version, "shared-token")
	if err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	if cancelled.Status != biz.UploadCancelled {
		t.Fatalf("status: %s", cancelled.Status)
	}
	again, err := repo.CancelUpload(ctx, pending.ID, cancelled.Version, "shared-token")
	if err != nil || again.Status != biz.UploadCancelled {
		t.Fatalf("repeat cancel: %v status=%s", err, again.Status)
	}
}

func TestUploadFreezeRejectsAnUnconfirmedInput(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 2)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	// Only the first episode gets a confirmed input; the second keeps a
	// declared reference that was never confirmed.
	first, _ := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/5/one", time.Now().Add(time.Minute)))
	if _, err := repo.CompleteUploadInput(ctx, first.ID, first.Version, "shared-token", confirmedObject(), "abc", 1024, "video/mp4"); err != nil {
		t.Fatalf("complete first input: %v", err)
	}
	if _, err := client.RevisionEpisode.UpdateOneID(episodes[1].ID).
		SetInputKind("manual").SetInputRef(9999).SetInputState("").
		Save(ctx); err != nil {
		t.Fatalf("declare unconfirmed input: %v", err)
	}
	batches := mediaRepo(client)
	grant := seedGrant(t, client, revision.SeriesID)
	_, err := batches.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID:                   revision.ID,
		ExpectedVersion:              1,
		ProfileVersion:               "web-mp4-h264-aac-v1",
		InputKind:                    "manual",
		Actor:                        "shared-token",
		AuthorizationGrantID:         grant.ID,
		ExpectedAuthorizationVersion: 1,
	})
	if !errors.Is(err, biz.ErrBatchInputNotReady) {
		t.Fatalf("freeze with unconfirmed input: %v", err)
	}
	// The rejected submission left the revision editable and created no batch.
	stored, err := client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if stored.State != biz.RevisionStateDraft {
		t.Fatalf("revision state after a rejected freeze: %s", stored.State)
	}
	if existing, _ := batches.GetBatchByRevision(ctx, revision.ID); existing != nil {
		t.Fatalf("a rejected freeze created batch %d", existing.ID)
	}
}

func TestUploadCoverAfterFreezeOpensTheReviewGate(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 2)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	for index := range episodes {
		session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[index].ID, "uploads/6/"+textOf(episodes[index].ExternalID), time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatalf("create episode upload: %v", err)
		}
		if _, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token", confirmedObject(), "abc", 1024, "video/mp4"); err != nil {
			t.Fatalf("complete episode input: %v", err)
		}
	}
	batches := mediaRepo(client)
	grant := seedGrant(t, client, revision.SeriesID)
	batch, err := batches.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID:                   revision.ID,
		ExpectedVersion:              1,
		ProfileVersion:               "web-mp4-h264-aac-v1",
		InputKind:                    "manual",
		Actor:                        "shared-token",
		AuthorizationGrantID:         grant.ID,
		ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if batch.CoverStatus != biz.MediaCoverPending {
		t.Fatalf("batch cover status before upload: %s", batch.CoverStatus)
	}
	// The videos are ready; the cover is still missing, so no review task may
	// exist yet.
	if _, err := client.MediaItem.Update().
		Where(mediaitem.BatchIDEQ(batch.ID)).
		SetStatus(string(biz.MediaItemReady)).
		Save(ctx); err != nil {
		t.Fatalf("mark items ready: %v", err)
	}
	if _, err := batches.RefreshReadyBatches(ctx, 10); err != nil {
		t.Fatalf("refresh without cover: %v", err)
	}
	if count, _ := client.ReviewTask.Query().Where(reviewtask.RevisionIDEQ(revision.ID)).Count(ctx); count != 0 {
		t.Fatalf("a batch without a cover opened %d review tasks", count)
	}
	// A cover is still accepted while the revision is processing.
	cover, err := repo.CreateUpload(ctx, biz.CreateUploadRecord{
		RevisionID: revision.ID,
		Kind:       biz.UploadKindCover,
		Filename:   "cover.png",
		SizeBytes:  2048,
		StagingKey: "uploads/6/cover",
		Provider:   "local",
		Bucket:     "local-bucket",
		Region:     "local",
		Actor:      "shared-token",
		// The revision advanced when the batch froze it; a cover is not tied
		// to that version.
		RevisionVersion: 1,
		ExpiresAt:       time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create cover upload after freeze: %v", err)
	}
	if _, err := repo.CompleteUploadCover(ctx, biz.CompleteCoverRecord{
		SessionID:       cover.ID,
		ExpectedVersion: cover.Version,
		Object: biz.ObjectRef{
			Provider: "local", Bucket: "local-bucket", Region: "local",
			Key: "openduanju-media/covers/abc.png",
		},
		SHA256:      "abc",
		SizeBytes:   2048,
		ContentType: "image/png",
		Width:       1080,
		Height:      1920,
	}, "shared-token"); err != nil {
		t.Fatalf("complete cover: %v", err)
	}
	refreshed, err := batches.GetBatch(ctx, batch.ID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if refreshed.CoverStatus != biz.MediaCoverReady {
		t.Fatalf("cover status: %s", refreshed.CoverStatus)
	}
	tasks, err := client.ReviewTask.Query().Where(reviewtask.RevisionIDEQ(revision.ID)).All(ctx)
	if err != nil {
		t.Fatalf("list review tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("review tasks after a ready cover: %d", len(tasks))
	}
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.State != biz.RevisionStatePendingReview {
		t.Fatalf("revision state: %s", stored.State)
	}
	if stored.CoverAssetID == 0 {
		t.Fatal("the revision does not reference the cover asset")
	}
	// The cover session is completed and points at the stored asset.
	session, err := client.UploadSession.Get(ctx, cover.ID)
	if err != nil {
		t.Fatalf("get cover session: %v", err)
	}
	if session.Status != biz.UploadCompleted || session.AssetID == 0 {
		t.Fatalf("cover session: status=%s asset=%d", session.Status, session.AssetID)
	}
	// No cover may be bound to a revision that is no longer draft or
	// processing.
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).SetState("withdrawn").Save(ctx); err != nil {
		t.Fatalf("withdraw revision: %v", err)
	}
	if _, err := repo.CreateUpload(ctx, biz.CreateUploadRecord{
		RevisionID: revision.ID, Kind: biz.UploadKindCover, Filename: "c.png", SizeBytes: 10,
		StagingKey: "uploads/6/cover2", Provider: "local", Bucket: "local-bucket", Region: "local",
		Actor: "shared-token", RevisionVersion: 1, ExpiresAt: time.Now().Add(time.Minute),
	}); !errors.Is(err, biz.ErrRevisionNotEditable) {
		t.Fatalf("cover on a withdrawn revision: %v", err)
	}
}

func TestUploadExpiredSessionsAreNotReused(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/7/one", time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if _, err := client.UploadSession.UpdateOneID(session.ID).
		SetExpiresAt(time.Now().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	// An expired session is not reused: the operator gets a fresh object.
	fresh, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/7/two", time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatalf("create after expiry: %v", err)
	}
	if fresh.ID == session.ID {
		t.Fatal("an expired session was reused")
	}
	// The expired row stays queryable and is marked pending, so an operator
	// can see it instead of a silently missing record.
	stale, err := client.UploadSession.Query().Where(uploadsession.IDEQ(session.ID)).Only(ctx)
	if err != nil {
		t.Fatalf("get stale session: %v", err)
	}
	if stale.Status != biz.UploadPending {
		t.Fatalf("stale session status: %s", stale.Status)
	}
}

// confirmedObject is the identity a session records when its object is
// confirmed. The version matters for the read path.
func confirmedObject() biz.ObjectRef {
	return biz.ObjectRef{
		Provider:  "local",
		Bucket:    "local-bucket",
		Region:    "local",
		Key:       "confirmed-input",
		VersionID: "input-version-a",
	}
}

// coverRecordFor builds a confirmed cover record for one session.
func coverRecordFor(session *biz.UploadSession, key string) biz.CompleteCoverRecord {
	return biz.CompleteCoverRecord{
		SessionID:       session.ID,
		ExpectedVersion: session.Version,
		Object: biz.ObjectRef{
			Provider: "local", Bucket: "local-bucket", Region: "local", Key: key,
		},
		SHA256:      "cover-sha",
		SizeBytes:   2048,
		ContentType: "image/png",
		Width:       800,
		Height:      1200,
	}
}

func TestUploadInputConfirmIsRejectedAfterTheRevisionFroze(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	// The operator opened the session while the revision was a draft and the
	// bytes are verified outside the transaction. The revision is frozen while
	// the confirmation runs.
	session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, "uploads/8/one", time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).
		SetState(biz.RevisionStateProcessing).AddVersion(1).Save(ctx); err != nil {
		t.Fatalf("freeze revision: %v", err)
	}
	if _, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token", confirmedObject(), "abc", 1024, "video/mp4"); !errors.Is(err, biz.ErrUploadRevisionChanged) {
		t.Fatalf("confirm after freeze: %v", err)
	}
	episode, err := client.RevisionEpisode.Get(ctx, episodes[0].ID)
	if err != nil {
		t.Fatalf("get episode: %v", err)
	}
	if episode.InputKind == "manual" || episode.InputState != "" {
		t.Fatalf("a rejected confirmation bound the input: kind=%s state=%s", episode.InputKind, episode.InputState)
	}
	stored, _ := client.UploadSession.Get(ctx, session.ID)
	if stored.Status != biz.UploadPending {
		t.Fatalf("session status after a rejected confirmation: %s", stored.Status)
	}
}

func TestUploadCoverConfirmIsRejectedAfterWithdrawal(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	cover, err := repo.CreateUpload(ctx, biz.CreateUploadRecord{
		RevisionID: revision.ID, Kind: biz.UploadKindCover, Filename: "cover.png", SizeBytes: 2048,
		StagingKey: "uploads/8/cover", Provider: "local", Bucket: "local-bucket", Region: "local",
		Actor: "shared-token", RevisionVersion: 1, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create cover: %v", err)
	}
	// The image was being decoded while the revision was withdrawn.
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).SetState("withdrawn").Save(ctx); err != nil {
		t.Fatalf("withdraw revision: %v", err)
	}
	if _, err := repo.CompleteUploadCover(ctx, coverRecordFor(cover, "covers/abc.png"), "shared-token"); !errors.Is(err, biz.ErrUploadRevisionChanged) {
		t.Fatalf("confirm cover after withdrawal: %v", err)
	}
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.CoverAssetID != 0 {
		t.Fatalf("a rejected cover was bound: %d", stored.CoverAssetID)
	}
	if count, _ := client.MediaAsset.Query().Count(ctx); count != 0 {
		t.Fatalf("a rejected cover created %d assets", count)
	}
	if count, _ := client.ReviewTask.Query().Count(ctx); count != 0 {
		t.Fatalf("a rejected cover created %d review tasks", count)
	}
}

func TestUploadCoverConfirmIsRejectedAfterTheReviewOpened(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 2)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	ctx := context.Background()

	for index := range episodes {
		session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[index].ID, "uploads/9/"+textOf(episodes[index].ExternalID), time.Now().Add(time.Minute)))
		if err != nil {
			t.Fatalf("create episode upload: %v", err)
		}
		if _, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token", confirmedObject(), "abc", 1024, "video/mp4"); err != nil {
			t.Fatalf("complete episode input: %v", err)
		}
	}
	batches := mediaRepo(client)
	grant := seedGrant(t, client, revision.SeriesID)
	batch, err := batches.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: 1,
		ProfileVersion: "web-mp4-h264-aac-v1", InputKind: "manual", Actor: "shared-token",
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	// Cover A is confirmed while the videos are still queued, so the review
	// gate stays closed and the revision is still processing.
	first, err := repo.CreateUpload(ctx, biz.CreateUploadRecord{
		RevisionID: revision.ID, Kind: biz.UploadKindCover, Filename: "a.png", SizeBytes: 2048,
		StagingKey: "uploads/9/cover-a", Provider: "local", Bucket: "local-bucket", Region: "local",
		Actor: "shared-token", RevisionVersion: 1, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create first cover: %v", err)
	}
	if _, err := repo.CompleteUploadCover(ctx, coverRecordFor(first, "covers/first.png"), "shared-token"); err != nil {
		t.Fatalf("complete first cover: %v", err)
	}
	// Cover B is opened afterwards; the revision is still processing.
	second, err := repo.CreateUpload(ctx, biz.CreateUploadRecord{
		RevisionID: revision.ID, Kind: biz.UploadKindCover, Filename: "b.png", SizeBytes: 2048,
		StagingKey: "uploads/9/cover-b", Provider: "local", Bucket: "local-bucket", Region: "local",
		Actor: "shared-token", RevisionVersion: 1, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create second cover: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("the second cover reused the completed session")
	}
	// While B is being decoded the videos become ready and the gate opens.
	if _, err := client.MediaItem.Update().Where(mediaitem.BatchIDEQ(batch.ID)).
		SetStatus(string(biz.MediaItemReady)).Save(ctx); err != nil {
		t.Fatalf("mark items ready: %v", err)
	}
	if _, err := batches.RefreshReadyBatches(ctx, 10); err != nil {
		t.Fatalf("refresh the gate: %v", err)
	}
	revisionAfterGate, _ := client.ContentRevision.Get(ctx, revision.ID)
	if revisionAfterGate.State != biz.RevisionStatePendingReview {
		t.Fatalf("revision state after the gate: %s", revisionAfterGate.State)
	}
	boundAsset := revisionAfterGate.CoverAssetID
	tasksBefore, _ := client.ReviewTask.Query().All(ctx)

	// B arrives after the review opened. It must not replace the cover the
	// review is looking at, and it must not touch the review task.
	if _, err := repo.CompleteUploadCover(ctx, coverRecordFor(second, "covers/second.png"), "shared-token"); !errors.Is(err, biz.ErrUploadRevisionChanged) {
		t.Fatalf("confirm a second cover after the review opened: %v", err)
	}
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.CoverAssetID != boundAsset {
		t.Fatalf("the cover of a reviewed revision changed: %d -> %d", boundAsset, stored.CoverAssetID)
	}
	tasksAfter, _ := client.ReviewTask.Query().All(ctx)
	if len(tasksAfter) != len(tasksBefore) {
		t.Fatalf("review tasks changed: %d -> %d", len(tasksBefore), len(tasksAfter))
	}
	if len(tasksAfter) != 1 || tasksAfter[0].ID != tasksBefore[0].ID || tasksAfter[0].BatchID != tasksBefore[0].BatchID {
		t.Fatalf("the review task was replaced: %+v", tasksAfter)
	}
	if storedAsset, err := client.ContentRevision.Get(ctx, revision.ID); err != nil || storedAsset.CoverAssetID == 0 {
		t.Fatalf("the reviewed revision lost its cover: %+v err=%v", storedAsset, err)
	}
	storedSecond, _ := client.UploadSession.Get(ctx, second.ID)
	if storedSecond.Status != biz.UploadPending {
		t.Fatalf("the rejected cover session changed status: %s", storedSecond.Status)
	}
}

func TestUploadInputKeepsTheConfirmedVersion(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	episodes, _ := client.RevisionEpisode.Query().All(context.Background())
	repo := data.NewUploadRepoWithDB(client)
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	ctx := context.Background()
	key := "uploads/10/episodes/one"
	write := func(body []byte) *biz.StoredObject {
		t.Helper()
		path := filepath.Join(t.TempDir(), "object")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		stored, err := storage.PutFile(ctx, biz.PutObjectRequest{
			Key: key, Path: path, SizeBytes: int64(len(body)), SHA256: digestOfStaging(string(body)),
		})
		if err != nil {
			t.Fatalf("put object: %v", err)
		}
		return stored
	}

	session, err := repo.CreateUpload(ctx, uploadRecord(revision.ID, episodes[0].ID, key, time.Now().Add(time.Minute)))
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	first := write([]byte("confirmed-version-a"))
	head, err := storage.Head(ctx, first.ObjectRef)
	if err != nil {
		t.Fatalf("head confirmed object: %v", err)
	}
	if _, err := repo.CompleteUploadInput(ctx, session.ID, session.Version, "shared-token",
		head.ObjectRef, head.MetadataSHA256, head.SizeBytes, "video/mp4"); err != nil {
		t.Fatalf("complete input: %v", err)
	}
	// The same key is written with different bytes after the confirmation.
	write([]byte("later-version-b"))

	input, err := repo.GetCompletedInput(ctx, session.ID)
	if err != nil {
		t.Fatalf("get completed input: %v", err)
	}
	if input.Object.VersionID != head.VersionID {
		t.Fatalf("the frozen input lost its version: %s vs %s", input.Object.VersionID, head.VersionID)
	}
	downloaded, err := storage.GetToFile(ctx, input.Object, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("read frozen input: %v", err)
	}
	body, err := os.ReadFile(downloaded.Path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != "confirmed-version-a" {
		t.Fatalf("the task read %q instead of the confirmed version", string(body))
	}
}

// textOf reads an optional text column in a test fixture.
func textOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
