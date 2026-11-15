package service

import (
	"context"
	"testing"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// stubBatchRepo implements the parts of the repository the media service uses.
type stubBatchRepo struct {
	biz.MediaBatchRepo
	batch     *biz.MediaBatch
	revision  *biz.ContentRevision
	episodes  []biz.MediaEpisodeTarget
	cancelled bool
	retried   bool
}

func (s *stubBatchRepo) FreezeAndCreateBatch(_ context.Context, request biz.FreezeBatchRequest) (*biz.MediaBatch, error) {
	stored := biz.MediaBatch{
		ID:             42,
		SeriesID:       3,
		RevisionID:     request.RevisionID,
		SelectionHash:  biz.SelectionHash(s.episodes),
		ProfileVersion: request.ProfileVersion,
		InputKind:      request.InputKind,
		Status:         biz.MediaBatchQueued,
		CoverStatus:    biz.MediaCoverPending,
		ItemTotal:      len(s.episodes),
		CreatedBy:      request.Actor,
		Version:        1,
	}
	s.batch = &stored
	return s.batch, nil
}

func (s *stubBatchRepo) GetBatch(_ context.Context, id int64) (*biz.MediaBatch, error) {
	if s.batch == nil || s.batch.ID != id {
		return nil, nil
	}
	return s.batch, nil
}

func (s *stubBatchRepo) ListItems(context.Context, biz.ItemQuery) ([]biz.MediaItem, int, error) {
	return nil, 0, nil
}

func (s *stubBatchRepo) ListBatches(context.Context, biz.BatchQuery) ([]biz.MediaBatch, int, error) {
	return nil, 0, nil
}

func (s *stubBatchRepo) RequestCancel(_ context.Context, id int64, expectedVersion int, reason string) (*biz.MediaBatch, error) {
	s.cancelled = true
	return s.batch, nil
}

func (s *stubBatchRepo) RetryItems(_ context.Context, id int64, expectedVersion int, itemIDs []int64) (*biz.MediaBatch, error) {
	s.retried = true
	return s.batch, nil
}

func (s *stubBatchRepo) GetRevisionForMedia(context.Context, int64) (*biz.ContentRevision, error) {
	return s.revision, nil
}

func (s *stubBatchRepo) ListSelectedEpisodes(context.Context, int64) ([]biz.MediaEpisodeTarget, error) {
	return s.episodes, nil
}

func newMediaService(repo *stubBatchRepo) *AdminMediaService {
	return NewAdminMediaService(biz.NewMediaBatches(repo, repo, nil))
}

func TestSubmitRevisionAnswersAccepted(t *testing.T) {
	repo := &stubBatchRepo{
		revision: &biz.ContentRevision{ID: 7, SeriesID: 3, State: "draft", Version: 1},
		episodes: []biz.MediaEpisodeTarget{{ID: 1, ExternalID: "ep-1", Number: 1}},
	}
	svc := newMediaService(repo)
	holder := &StatusHolder{}
	ctx := WithStatusHolder(context.Background(), holder)
	ctx = WithActor(ctx, &Actor{Staff: &biz.Staff{Username: "operator"}, SessionID: 1})

	reply, err := svc.SubmitRevision(ctx, &v1.SubmitMediaBatchRequest{AuthorizationGrantId: 9, ExpectedAuthorizationVersion: 1,
		Id: 7, ExpectedVersion: 1, ProcessingProfileKey: "web-mp4-h264-aac-v1",
	})
	if err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	if holder.Code != 202 {
		t.Fatalf("status = %d, want 202", holder.Code)
	}
	if reply.GetId() != 42 || reply.GetRevisionId() != 7 {
		t.Fatalf("reply = %+v, want the created batch", reply)
	}
	if reply.GetCreatedBy() == "" {
		t.Fatal("the batch must record the operator that submitted it")
	}
}

func TestSubmitRevisionKeepsTheErrorStatus(t *testing.T) {
	repo := &stubBatchRepo{}
	svc := newMediaService(repo)
	holder := &StatusHolder{}
	ctx := WithStatusHolder(context.Background(), holder)
	ctx = WithActor(ctx, &Actor{Staff: &biz.Staff{Username: "operator"}, SessionID: 1})

	_, err := svc.SubmitRevision(ctx, &v1.SubmitMediaBatchRequest{AuthorizationGrantId: 9, ExpectedAuthorizationVersion: 1,
		Id: 7, ExpectedVersion: 1, ProcessingProfileKey: "p",
	})
	if err == nil {
		t.Fatal("a missing revision must fail")
	}
	if holder.Code != 0 {
		t.Fatalf("status = %d, want the transport default for an error", holder.Code)
	}
	kratosErr := kratoserrors.FromError(err)
	if kratosErr.Code != 404 || kratosErr.Reason != "REVISION_NOT_FOUND" {
		t.Fatalf("error = %d/%s, want 404/REVISION_NOT_FOUND", kratosErr.Code, kratosErr.Reason)
	}
}

func TestGetBatchReportsAMissingBatch(t *testing.T) {
	svc := newMediaService(&stubBatchRepo{})
	_, err := svc.GetBatch(context.Background(), &v1.GetMediaBatchRequest{Id: 99})
	kratosErr := kratoserrors.FromError(err)
	if kratosErr.Code != 404 || kratosErr.Reason != "BATCH_NOT_FOUND" {
		t.Fatalf("error = %d/%s, want 404/BATCH_NOT_FOUND", kratosErr.Code, kratosErr.Reason)
	}
}

func TestCancelAndRetryAnswerAccepted(t *testing.T) {
	repo := &stubBatchRepo{batch: &biz.MediaBatch{ID: 42, Status: biz.MediaBatchRunning}}
	svc := newMediaService(repo)

	holder := &StatusHolder{}
	ctx := WithStatusHolder(context.Background(), holder)
	ctx = WithActor(ctx, &Actor{Staff: &biz.Staff{Username: "operator"}, SessionID: 1})
	if _, err := svc.CancelBatch(ctx, &v1.CancelMediaBatchRequest{Id: 42, ExpectedVersion: 1, Reason: "stop"}); err != nil {
		t.Fatalf("CancelBatch: %v", err)
	}
	if holder.Code != 202 {
		t.Fatalf("status = %d, want 202", holder.Code)
	}
	if !repo.cancelled {
		t.Fatal("the cancellation must reach the repository")
	}

	repo.batch.Status = biz.MediaBatchPartiallyFailed
	holder = &StatusHolder{}
	ctx = WithStatusHolder(context.Background(), holder)
	if _, err := svc.RetryFailures(ctx, &v1.RetryMediaBatchRequest{Id: 42, ExpectedVersion: 1}); err != nil {
		t.Fatalf("RetryFailures: %v", err)
	}
	if holder.Code != 202 {
		t.Fatalf("status = %d, want 202", holder.Code)
	}
	if !repo.retried {
		t.Fatal("the retry must reach the repository")
	}
}

func TestListRejectsAnUnknownFilter(t *testing.T) {
	svc := newMediaService(&stubBatchRepo{})
	_, err := svc.ListBatches(context.Background(), &v1.ListMediaBatchesRequest{Status: "not-a-status"})
	kratosErr := kratoserrors.FromError(err)
	if kratosErr.Code != 400 || kratosErr.Reason != "INVALID_FILTER" {
		t.Fatalf("error = %d/%s, want 400/INVALID_FILTER", kratosErr.Code, kratosErr.Reason)
	}
	_, err = svc.ListItems(context.Background(), &v1.ListMediaItemsRequest{Id: 42, Stage: "not-a-stage"})
	kratosErr = kratoserrors.FromError(err)
	if kratosErr.Code != 400 || kratosErr.Reason != "INVALID_FILTER" {
		t.Fatalf("error = %d/%s, want 400/INVALID_FILTER", kratosErr.Code, kratosErr.Reason)
	}
}

func TestListItemsAcceptsTheStageFilter(t *testing.T) {
	svc := newMediaService(&stubBatchRepo{batch: &biz.MediaBatch{ID: 42, Status: biz.MediaBatchRunning}})
	if _, err := svc.ListItems(context.Background(), &v1.ListMediaItemsRequest{Id: 42, Stage: "processing"}); err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if _, err := svc.ListBatches(context.Background(), &v1.ListMediaBatchesRequest{Status: "pending_review"}); err != nil {
		t.Fatalf("ListBatches: %v", err)
	}
}
