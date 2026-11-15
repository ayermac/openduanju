package biz_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"openduanju-server/internal/biz"
)

// fakeContentRepo serves the frozen revision a batch is built from.
type fakeContentRepo struct {
	revision *biz.ContentRevision
	episodes []biz.MediaEpisodeTarget
	err      error
}

func (f *fakeContentRepo) GetRevisionForMedia(context.Context, int64) (*biz.ContentRevision, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.revision, nil
}

func (f *fakeContentRepo) ListSelectedEpisodes(context.Context, int64) ([]biz.MediaEpisodeTarget, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.episodes, nil
}

// fakeBatchRepo records what the use case asks the repository to do.
type fakeBatchRepo struct {
	batch     *biz.MediaBatch
	items     []biz.MediaItem
	created   []biz.MediaBatch
	episodes  []biz.MediaEpisodeTarget
	cancelled bool
	retried   bool
	retryIDs  []int64
}

func (f *fakeBatchRepo) FreezeAndCreateBatch(_ context.Context, request biz.FreezeBatchRequest) (*biz.MediaBatch, error) {
	episodes := append([]biz.MediaEpisodeTarget(nil), f.episodes...)
	sort.Slice(episodes, func(i, j int) bool { return episodes[i].Number < episodes[j].Number })
	stored := biz.MediaBatch{
		ID:             42,
		SeriesID:       3,
		RevisionID:     request.RevisionID,
		SelectionHash:  biz.SelectionHash(episodes),
		ProfileVersion: request.ProfileVersion,
		InputKind:      request.InputKind,
		Status:         biz.MediaBatchQueued,
		CoverStatus:    biz.MediaCoverPending,
		ItemTotal:      len(episodes),
		CreatedBy:      request.Actor,
		Version:        1,
	}
	f.created = append(f.created, stored)
	f.batch = &stored
	for i, episode := range episodes {
		f.items = append(f.items, biz.MediaItem{
			ID:         int64(i + 1),
			BatchID:    stored.ID,
			EpisodeID:  episode.ID,
			ExternalID: episode.ExternalID,
			EpisodeNo:  episode.Number,
			Status:     biz.MediaItemQueued,
		})
	}
	return f.batch, nil
}

func (f *fakeBatchRepo) GetBatch(_ context.Context, id int64) (*biz.MediaBatch, error) {
	if f.batch == nil || f.batch.ID != id {
		return nil, nil
	}
	return f.batch, nil
}

func (f *fakeBatchRepo) GetBatchByRevision(context.Context, int64) (*biz.MediaBatch, error) {
	return f.batch, nil
}

func (f *fakeBatchRepo) ListBatches(context.Context, biz.BatchQuery) ([]biz.MediaBatch, int, error) {
	if f.batch == nil {
		return nil, 0, nil
	}
	return []biz.MediaBatch{*f.batch}, 1, nil
}

func (f *fakeBatchRepo) ListItems(context.Context, biz.ItemQuery) ([]biz.MediaItem, int, error) {
	return f.items, len(f.items), nil
}

func (f *fakeBatchRepo) GetItem(context.Context, int64) (*biz.MediaItem, error) { return nil, nil }

func (f *fakeBatchRepo) ClaimItems(context.Context, string, time.Duration, int) ([]biz.MediaItem, error) {
	return nil, nil
}

func (f *fakeBatchRepo) SaveItem(_ context.Context, item biz.MediaItem) (*biz.MediaItem, error) {
	return &item, nil
}

func (f *fakeBatchRepo) RenewItemLease(_ context.Context, item biz.MediaItem, _ time.Duration) (*biz.MediaItem, error) {
	return &item, nil
}

func (f *fakeBatchRepo) CommitItemReady(_ context.Context, item biz.MediaItem, asset biz.MediaAsset) (*biz.MediaItem, *biz.MediaBatch, error) {
	return &item, f.batch, nil
}

func (f *fakeBatchRepo) FailItem(_ context.Context, item biz.MediaItem, code biz.MediaErrorCode, message string, retryable bool, nextEligibleAt *time.Time) (*biz.MediaItem, error) {
	return &item, nil
}

func (f *fakeBatchRepo) RequestCancel(_ context.Context, id int64, expectedVersion int, reason string) (*biz.MediaBatch, error) {
	f.cancelled = true
	return f.batch, nil
}

func (f *fakeBatchRepo) RetryItems(_ context.Context, id int64, expectedVersion int, itemIDs []int64) (*biz.MediaBatch, error) {
	f.retried = true
	f.retryIDs = itemIDs
	return f.batch, nil
}

func (f *fakeBatchRepo) RefreshBatchStatus(context.Context, int64) (*biz.MediaBatch, error) {
	return f.batch, nil
}

func (f *fakeBatchRepo) RefreshReadyBatches(context.Context, int) (int, error) {
	return 0, nil
}

func (f *fakeBatchRepo) EnsureBatchAuthorization(context.Context, int64, biz.Permission) error {
	return nil
}

func (f *fakeBatchRepo) InheritedAssetForItem(context.Context, biz.MediaItem) (*biz.InheritedAsset, error) {
	return nil, nil
}

func draftRevision() *biz.ContentRevision {
	return &biz.ContentRevision{ID: 7, SeriesID: 3, State: "draft", Version: 4, SelectedCount: 2}
}

func selectedEpisodes() []biz.MediaEpisodeTarget {
	return []biz.MediaEpisodeTarget{
		{ID: 101, ExternalID: "ep-2", Number: 2},
		{ID: 100, ExternalID: "ep-1", Number: 1},
	}
}

func TestSubmitRevisionFreezesTheSelection(t *testing.T) {
	repo := &fakeBatchRepo{episodes: selectedEpisodes()}
	content := &fakeContentRepo{revision: draftRevision(), episodes: selectedEpisodes()}
	uc := biz.NewMediaBatches(repo, content, nil)

	batch, err := uc.SubmitRevision(context.Background(), biz.SubmitBatchRequest{AuthorizationGrantID: 9, ExpectedAuthorizationVersion: 1,
		RevisionID: 7, ExpectedVersion: 4, ProfileVersion: "web-mp4-h264-aac-v1", Actor: "operator",
	})
	if err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	if batch.SelectionHash == "" {
		t.Fatal("the batch must fingerprint its selection")
	}
	if batch.ItemTotal != 2 {
		t.Fatalf("item total = %d, want 2", batch.ItemTotal)
	}
	if batch.ProfileVersion != "web-mp4-h264-aac-v1" {
		t.Fatalf("profile = %q", batch.ProfileVersion)
	}
	if batch.CreatedBy != "operator" {
		t.Fatalf("created by = %q, want the operator that submitted it", batch.CreatedBy)
	}
	if batch.Status != biz.MediaBatchQueued {
		t.Fatalf("status = %q, want queued", batch.Status)
	}
	if len(repo.items) != 2 {
		t.Fatalf("items = %d, want one task per selected episode", len(repo.items))
	}
	if repo.items[0].EpisodeNo != 1 || repo.items[1].EpisodeNo != 2 {
		t.Fatalf("items must follow the episode order, got %d then %d", repo.items[0].EpisodeNo, repo.items[1].EpisodeNo)
	}
}

func TestSubmitRevisionRejectsUnprocessableInput(t *testing.T) {
	cases := []struct {
		name     string
		revision *biz.ContentRevision
		episodes []biz.MediaEpisodeTarget
		version  int
		profile  string
		want     error
	}{
		{
			name:     "processing profile missing",
			revision: draftRevision(), episodes: selectedEpisodes(), version: 4, profile: "", want: biz.ErrProfileRequired,
		},
		{
			name:     "expected version missing",
			revision: draftRevision(), episodes: selectedEpisodes(), version: 0, profile: "p", want: biz.ErrVersionRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeBatchRepo{episodes: tc.episodes}
			uc := biz.NewMediaBatches(repo, &fakeContentRepo{revision: tc.revision, episodes: tc.episodes}, nil)
			_, err := uc.SubmitRevision(context.Background(), biz.SubmitBatchRequest{AuthorizationGrantID: 9, ExpectedAuthorizationVersion: 1,
				RevisionID: 7, ExpectedVersion: tc.version, ProfileVersion: tc.profile,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(repo.created) != 0 {
				t.Fatal("a rejected submission must not create a batch")
			}
		})
	}
}

func TestSubmitRevisionReportsMissingRevision(t *testing.T) {
	repo := &fakeBatchRepo{}
	uc := biz.NewMediaBatches(repo, &fakeContentRepo{revision: nil}, nil)
	if _, err := uc.SubmitRevision(context.Background(), biz.SubmitBatchRequest{AuthorizationGrantID: 9, ExpectedAuthorizationVersion: 1,
		RevisionID: 7, ExpectedVersion: 1, ProfileVersion: "p",
	}); !errors.Is(err, biz.ErrRevisionNotFound) {
		t.Fatalf("err = %v, want %v", err, biz.ErrRevisionNotFound)
	}
}

func TestSubmitRevisionRequiresTheExpectedVersion(t *testing.T) {
	repo := &fakeBatchRepo{episodes: selectedEpisodes()}
	uc := biz.NewMediaBatches(repo, &fakeContentRepo{revision: draftRevision(), episodes: selectedEpisodes()}, nil)
	if _, err := uc.SubmitRevision(context.Background(), biz.SubmitBatchRequest{AuthorizationGrantID: 9, ExpectedAuthorizationVersion: 1,
		RevisionID: 7, ProfileVersion: "p",
	}); !errors.Is(err, biz.ErrVersionRequired) {
		t.Fatalf("err = %v, want %v", err, biz.ErrVersionRequired)
	}
	if len(repo.created) != 0 {
		t.Fatal("a submission without the version must not create a batch")
	}
}

func TestCancelBatchRejectsAFinishedBatch(t *testing.T) {
	repo := &fakeBatchRepo{batch: &biz.MediaBatch{ID: 42, Status: biz.MediaBatchSucceeded}}
	uc := biz.NewMediaBatches(repo, &fakeContentRepo{}, nil)
	if _, err := uc.CancelBatch(context.Background(), 42, 1, "stop"); !errors.Is(err, biz.ErrBatchNotCancellable) {
		t.Fatalf("err = %v, want %v", err, biz.ErrBatchNotCancellable)
	}
	if repo.cancelled {
		t.Fatal("a rejected cancellation must not reach the repository")
	}
}

func TestCancelBatchRequestsCancellation(t *testing.T) {
	repo := &fakeBatchRepo{batch: &biz.MediaBatch{ID: 42, Status: biz.MediaBatchRunning}}
	uc := biz.NewMediaBatches(repo, &fakeContentRepo{}, nil)
	if _, err := uc.CancelBatch(context.Background(), 42, 1, "stop"); err != nil {
		t.Fatalf("CancelBatch: %v", err)
	}
	if !repo.cancelled {
		t.Fatal("the cancellation must reach the repository")
	}
}

func TestRetryFailuresRejectsAnActiveBatch(t *testing.T) {
	repo := &fakeBatchRepo{batch: &biz.MediaBatch{ID: 42, Status: biz.MediaBatchRunning}}
	uc := biz.NewMediaBatches(repo, &fakeContentRepo{}, nil)
	if _, err := uc.RetryFailures(context.Background(), 42, 1, nil); !errors.Is(err, biz.ErrBatchAlreadyActive) {
		t.Fatalf("err = %v, want %v", err, biz.ErrBatchAlreadyActive)
	}
	if repo.retried {
		t.Fatal("a rejected retry must not reach the repository")
	}
}

func TestRetryFailuresRequeuesTheRequestedItems(t *testing.T) {
	repo := &fakeBatchRepo{batch: &biz.MediaBatch{ID: 42, Status: biz.MediaBatchPartiallyFailed}}
	uc := biz.NewMediaBatches(repo, &fakeContentRepo{}, nil)
	if _, err := uc.RetryFailures(context.Background(), 42, 1, []int64{5}); err != nil {
		t.Fatalf("RetryFailures: %v", err)
	}
	if !repo.retried || len(repo.retryIDs) != 1 || repo.retryIDs[0] != 5 {
		t.Fatalf("retry ids = %v, want the requested item", repo.retryIDs)
	}
}
