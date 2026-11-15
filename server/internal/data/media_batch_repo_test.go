package data_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/reviewtask"
)

// openMediaClient opens a disposable database with the media tables applied.
func openMediaClient(t *testing.T) *ent.Client {
	t.Helper()
	client := openCatalogClient(t)
	cleanMedia(t, client)
	t.Cleanup(func() { cleanMedia(t, client) })
	return client
}

// seedSeriesSeq keeps the external ids of seeded series unique inside one
// test binary, so a test can seed two series without a unique-key conflict.
var seedSeriesSeq atomic.Int64

func nextSeriesExternalID() string {
	return fmt.Sprintf("series-%d", seedSeriesSeq.Add(1))
}

func cleanMedia(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	for _, clean := range []func() (int, error){
		func() (int, error) { return client.LoginAttempt.Delete().Exec(ctx) },
		func() (int, error) { return client.StaffSession.Delete().Exec(ctx) },
		func() (int, error) { return client.Staff.Delete().Exec(ctx) },
		func() (int, error) { return client.AuthorizationGrant.Delete().Exec(ctx) },
		func() (int, error) { return client.UploadSession.Delete().Exec(ctx) },
		func() (int, error) { return client.ReviewDecision.Delete().Exec(ctx) },
		func() (int, error) { return client.Publication.Delete().Exec(ctx) },
		func() (int, error) { return client.ReviewTask.Delete().Exec(ctx) },
		func() (int, error) { return client.MediaAsset.Delete().Exec(ctx) },
		func() (int, error) { return client.MediaItem.Delete().Exec(ctx) },
		func() (int, error) { return client.MediaBatch.Delete().Exec(ctx) },
		// The source and channel rows are configuration, not test data: every
		// test starts from the deployed state, so a pause cannot leak.
		func() (int, error) {
			return client.SourceChannel.Update().
				SetStatus("enabled").
				SetRatePerMinute(30).
				SetTimeoutSeconds(20).
				SetName("热播榜").
				// The adapter key is deployment configuration; a test that
				// pointed the channel at a fake must not leak into the next.
				SetAdapterKey("hongguo_web").
				SetVersion(1).
				Save(ctx)
		},
		func() (int, error) {
			return client.Source.Update().SetStatus("enabled").SetVersion(1).Save(ctx)
		},
	} {
		if _, err := clean(); err != nil {
			t.Fatalf("clean media tables: %v", err)
		}
	}
}

// seedRevision stores a draft revision with selected episodes.
func seedRevision(t *testing.T, client *ent.Client, episodes int) *ent.ContentRevision {
	t.Helper()
	ctx := context.Background()
	series, err := client.ContentSeries.Create().
		SetSourceCode("hongguo").
		SetExternalID(nextSeriesExternalID()).
		SetTitle("测试剧集").
		SetStatus("draft").
		SetRevision(1).
		SetVersion(1).
		Save(ctx)
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	revision, err := client.ContentRevision.Create().
		SetSeriesID(series.ID).
		SetRevisionNo(1).
		SetState("draft").
		SetVersion(1).
		SetTitle("测试剧集").
		Save(ctx)
	if err != nil {
		t.Fatalf("create revision: %v", err)
	}
	for i := 1; i <= episodes; i++ {
		if _, err := client.RevisionEpisode.Create().
			SetRevisionID(revision.ID).
			SetExternalID(fmt.Sprintf("ep-%d", i)).
			SetEpisodeNo(int32(i)).
			SetSelected(true).
			Save(ctx); err != nil {
			t.Fatalf("create revision episode: %v", err)
		}
	}
	return revision
}

func mediaRepo(client *ent.Client) *data.MediaBatchRepo {
	return data.NewMediaBatchRepoWithDB(client)
}

// freezeBatch creates a batch over a draft revision. The revision is frozen
// by the same transaction, so it leaves the draft state.
// seedGrant stores one active grant with all three permissions for a series.
func seedGrant(t *testing.T, client *ent.Client, seriesID int64) *ent.AuthorizationGrant {
	t.Helper()
	now := time.Now().UTC()
	grant, err := client.AuthorizationGrant.Create().
		SetSeriesID(seriesID).
		SetReference("AUTH-TEST-001").
		SetAllowProcess(true).
		SetAllowDisplay(true).
		SetAllowPlayback(true).
		SetValidFrom(now.Add(-time.Hour)).
		SetStatus("active").
		SetVersion(1).
		SetConfirmedBy("tester").
		SetConfirmedAt(now).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return grant
}

func freezeBatch(t *testing.T, client *ent.Client, repo *data.MediaBatchRepo, revision *ent.ContentRevision) *biz.MediaBatch {
	t.Helper()
	grant := seedGrant(t, client, revision.SeriesID)
	batch, err := repo.FreezeAndCreateBatch(context.Background(), biz.FreezeBatchRequest{
		RevisionID:                   revision.ID,
		ExpectedVersion:              int(revision.Version),
		ProfileVersion:               "web-mp4-h264-aac-v1",
		Actor:                        "operator",
		AuthorizationGrantID:         grant.ID,
		ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("FreezeAndCreateBatch: %v", err)
	}
	return batch
}

func TestMediaBatchCreationIsIdempotentPerRevision(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 2)

	uc := biz.NewMediaBatches(repo, repo, nil)
	grant := seedGrant(t, client, revision.SeriesID)
	batch, err := uc.SubmitRevision(ctx, biz.SubmitBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: 1, ProfileVersion: "web-mp4-h264-aac-v1", Actor: "operator",
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("SubmitRevision: %v", err)
	}
	if batch.ItemTotal != 2 {
		t.Fatalf("item total = %d, want 2", batch.ItemTotal)
	}
	again, err := uc.SubmitRevision(ctx, biz.SubmitBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: 1, ProfileVersion: "web-mp4-h264-aac-v1", Actor: "operator",
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("second SubmitRevision: %v", err)
	}
	if again.ID != batch.ID {
		t.Fatalf("batch id = %d, want the existing batch %d", again.ID, batch.ID)
	}
	items, total, err := repo.ListItems(ctx, biz.ItemQuery{BatchID: batch.ID, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("items = %d (total %d), want 2", len(items), total)
	}
}

func TestMediaLeaseFencesTheOldWorker(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	first := claimed[0]
	if first.LeaseOwner != "worker-a" || first.LeaseGeneration != 1 {
		t.Fatalf("lease = %s/%d, want worker-a/1", first.LeaseOwner, first.LeaseGeneration)
	}

	// The lease of the first worker expires and another worker takes over.
	if _, err := client.MediaItem.UpdateOneID(first.ID).
		SetLeaseUntil(time.Now().UTC().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	taken, err := repo.ClaimItems(ctx, "worker-b", time.Minute, 10)
	if err != nil {
		t.Fatalf("second ClaimItems: %v", err)
	}
	if len(taken) != 1 {
		t.Fatalf("taken = %d, want 1", len(taken))
	}
	if taken[0].LeaseGeneration != first.LeaseGeneration+1 {
		t.Fatalf("generation = %d, want %d", taken[0].LeaseGeneration, first.LeaseGeneration+1)
	}
	if taken[0].Attempt != first.Attempt+1 {
		t.Fatalf("attempt = %d, want %d", taken[0].Attempt, first.Attempt+1)
	}

	// The worker that lost the item may not write a checkpoint any more.
	first.Status = biz.MediaItemResolving
	if _, err := repo.SaveItem(ctx, first); !errors.Is(err, biz.ErrItemLeaseLost) {
		t.Fatalf("err = %v, want %v", err, biz.ErrItemLeaseLost)
	}
	if _, _, err := repo.CommitItemReady(ctx, first, biz.MediaAsset{Object: biz.ObjectRef{Key: "k"}}); !errors.Is(err, biz.ErrItemLeaseLost) {
		t.Fatalf("err = %v, want %v", err, biz.ErrItemLeaseLost)
	}
	_ = batch
}

func TestMediaCancelStopsTheRunningTask(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 2)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed = %d, want 2", len(claimed))
	}
	cancelled, err := repo.RequestCancel(ctx, batch.ID, 0, "operator stopped it")
	if err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	if !cancelled.CancelRequested {
		t.Fatal("the batch must record the cancellation request")
	}
	// The in-flight task may not write a checkpoint any more.
	claimed[0].Status = biz.MediaItemProcessing
	if _, err := repo.SaveItem(ctx, claimed[0]); !errors.Is(err, biz.ErrBatchCancelled) {
		t.Fatalf("err = %v, want %v", err, biz.ErrBatchCancelled)
	}
	stopped, err := client.MediaItem.Get(ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if stopped.Status != string(biz.MediaItemCancelled) {
		t.Fatalf("item status = %q, want cancelled", stopped.Status)
	}
	// A cancelled batch hands out no work any more.
	again, err := repo.ClaimItems(ctx, "worker-b", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems after cancel: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("claimed = %d, want 0 after the cancellation", len(again))
	}
}

func TestMediaRetryOnlyRequeuesRetryableFailures(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 2)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	next := time.Now().UTC().Add(-time.Second)
	if _, err := repo.FailItem(ctx, claimed[0], biz.MediaCodeSourceUnavailable, "source is down", true, &next); err != nil {
		t.Fatalf("FailItem: %v", err)
	}
	if _, err := repo.FailItem(ctx, claimed[1], biz.MediaCodeMediaInvalid, "not playable", false, nil); err != nil {
		t.Fatalf("FailItem: %v", err)
	}
	if _, err := repo.RetryItems(ctx, batch.ID, 0, []int64{claimed[1].ID}); !errors.Is(err, biz.ErrBatchNotRetryable) {
		t.Fatalf("err = %v, want %v for a permanent failure", err, biz.ErrBatchNotRetryable)
	}
	requeued, err := repo.RetryItems(ctx, batch.ID, 0, nil)
	if err != nil {
		t.Fatalf("RetryItems: %v", err)
	}
	if requeued.Status != biz.MediaBatchPartiallyFailed {
		t.Fatalf("batch status = %q, want partially_failed", requeued.Status)
	}
	items, _, err := repo.ListItems(ctx, biz.ItemQuery{BatchID: batch.ID, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	states := map[int64]biz.MediaItemStatus{}
	for _, item := range items {
		states[item.ID] = item.Status
	}
	if states[claimed[0].ID] != biz.MediaItemQueued {
		t.Fatalf("retryable item = %q, want queued", states[claimed[0].ID])
	}
	if states[claimed[1].ID] != biz.MediaItemFailed {
		t.Fatalf("permanent item = %q, want failed", states[claimed[1].ID])
	}
}

func TestMediaReviewGateNeedsEveryItemAndTheCover(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 2)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	ready := func(item biz.MediaItem, key string) {
		t.Helper()
		asset := biz.MediaAsset{
			Object:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: key},
			State:       biz.MediaAssetVerified,
			SizeBytes:   10,
			SHA256:      "digest-" + key,
			ContentType: "video/mp4",
			ExternalID:  item.ExternalID,
		}
		if _, _, err := repo.CommitItemReady(ctx, item, asset); err != nil {
			t.Fatalf("CommitItemReady: %v", err)
		}
	}

	ready(claimed[0], "media/one.mp4")
	tasks, err := client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("review tasks = %d, want 0 while an episode is missing", len(tasks))
	}
	// The cover is a separate input: without it the batch never reaches the
	// review, even when every episode is ready.
	if _, err := client.MediaBatch.UpdateOneID(batch.ID).SetCoverStatus(biz.MediaCoverReady).Save(ctx); err != nil {
		t.Fatalf("mark cover ready: %v", err)
	}
	ready(claimed[1], "media/two.mp4")
	tasks, err = client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("review tasks = %d, want exactly one", len(tasks))
	}
	if tasks[0].RevisionID != revision.ID || tasks[0].BatchID != batch.ID {
		t.Fatalf("review task = %+v, want the batch of the revision", tasks[0])
	}
	if tasks[0].Status != "open" {
		t.Fatalf("review task status = %q, want open", tasks[0].Status)
	}
	updated, err := client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if updated.State != "pending_review" {
		t.Fatalf("revision state = %q, want pending_review", updated.State)
	}
	// Refreshing the batch must not create a second review task.
	if _, err := repo.RefreshBatchStatus(ctx, batch.ID); err != nil {
		t.Fatalf("RefreshBatchStatus: %v", err)
	}
	tasks, err = client.ReviewTask.Query().Where(reviewtask.RevisionIDEQ(revision.ID)).All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("review tasks = %d, want exactly one", len(tasks))
	}
	ready2, err := client.MediaBatch.Get(ctx, batch.ID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if ready2.Status != string(biz.MediaBatchPendingReview) {
		t.Fatalf("batch status = %q, want pending_review", ready2.Status)
	}
	if ready2.ItemReady != 2 || ready2.ItemTotal != 2 {
		t.Fatalf("batch counters = %d/%d, want 2/2", ready2.ItemReady, ready2.ItemTotal)
	}
}

func TestMediaBatchVersionConflictIsRejected(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	batch := freezeBatch(t, client, repo, revision)
	if _, err := repo.RequestCancel(ctx, batch.ID, batch.Version+5, "stop"); !errors.Is(err, biz.ErrBatchVersion) {
		t.Fatalf("err = %v, want %v", err, biz.ErrBatchVersion)
	}
	if _, err := repo.GetBatch(ctx, batch.ID+1000); err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	items, _, err := repo.ListItems(ctx, biz.ItemQuery{BatchID: batch.ID, Status: string(biz.MediaItemQueued), Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Status != biz.MediaItemQueued {
		t.Fatalf("item status = %q, want queued", items[0].Status)
	}
	if _, err := client.MediaItem.Query().Where(mediaitem.BatchIDEQ(batch.ID)).Count(ctx); err != nil {
		t.Fatalf("count items: %v", err)
	}
}

func TestMediaReviewGateOpensWhenTheCoverArrives(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	if _, _, err := repo.CommitItemReady(ctx, claimed[0], biz.MediaAsset{
		Object:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: "media/one.mp4"},
		State:       biz.MediaAssetVerified,
		SizeBytes:   10,
		SHA256:      "digest-one",
		ContentType: "video/mp4",
		ExternalID:  claimed[0].ExternalID,
	}); err != nil {
		t.Fatalf("CommitItemReady: %v", err)
	}
	// Every episode is ready but the cover is missing: the batch waits.
	stored, err := client.MediaBatch.Get(ctx, batch.ID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if stored.Status != string(biz.MediaBatchSucceeded) {
		t.Fatalf("batch status = %q, want succeeded", stored.Status)
	}
	if stored.CoverStatus != biz.MediaCoverPending {
		t.Fatalf("cover status = %q, want pending", stored.CoverStatus)
	}
	tasks, err := client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("review tasks = %d, want 0 while the cover is missing", len(tasks))
	}

	// The cover upload marks the cover ready; the worker applies the gate.
	if _, err := client.MediaBatch.UpdateOneID(batch.ID).SetCoverStatus(biz.MediaCoverReady).Save(ctx); err != nil {
		t.Fatalf("mark cover ready: %v", err)
	}
	refreshed, err := repo.RefreshReadyBatches(ctx, 20)
	if err != nil {
		t.Fatalf("RefreshReadyBatches: %v", err)
	}
	if refreshed != 1 {
		t.Fatalf("refreshed = %d, want 1", refreshed)
	}
	tasks, err = client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("review tasks = %d, want exactly one", len(tasks))
	}
	linked, err := client.MediaBatch.Get(ctx, batch.ID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if linked.ReviewTaskID != tasks[0].ID {
		t.Fatalf("batch review task = %d, want %d", linked.ReviewTaskID, tasks[0].ID)
	}
	// A second refresh must not create a second review task.
	if _, err := repo.RefreshReadyBatches(ctx, 20); err != nil {
		t.Fatalf("RefreshReadyBatches: %v", err)
	}
	tasks, err = client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("review tasks = %d, want exactly one", len(tasks))
	}
	opened, err := client.MediaBatch.Get(ctx, batch.ID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if opened.Status != string(biz.MediaBatchPendingReview) {
		t.Fatalf("batch status = %q, want pending_review", opened.Status)
	}
}

func TestMediaReviewGateRepairsAStaleBatchState(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	if _, err := repo.FailItem(ctx, claimed[0], biz.MediaCodeMediaInvalid, "not playable", false, nil); err != nil {
		t.Fatalf("FailItem: %v", err)
	}
	// The task is repaired by a later run: the stored state of the batch is
	// stale, but every task is ready again.
	if _, err := client.MediaItem.UpdateOneID(claimed[0].ID).
		SetStatus(string(biz.MediaItemReady)).SetErrorCode("").Save(ctx); err != nil {
		t.Fatalf("repair item: %v", err)
	}
	if _, err := client.MediaBatch.UpdateOneID(batch.ID).SetCoverStatus(biz.MediaCoverReady).Save(ctx); err != nil {
		t.Fatalf("mark cover ready: %v", err)
	}
	refreshed, err := repo.RefreshReadyBatches(ctx, 20)
	if err != nil {
		t.Fatalf("RefreshReadyBatches: %v", err)
	}
	if refreshed != 1 {
		t.Fatalf("refreshed = %d, want 1", refreshed)
	}
	stored, err := client.MediaBatch.Get(ctx, batch.ID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if stored.Status != string(biz.MediaBatchPendingReview) {
		t.Fatalf("batch status = %q, want pending_review", stored.Status)
	}
	if stored.ItemReady != 1 || stored.ItemFailed != 0 {
		t.Fatalf("batch counters = %d/%d, want 1/0", stored.ItemReady, stored.ItemFailed)
	}
	tasks, err := client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("review tasks = %d, want exactly one", len(tasks))
	}
}
