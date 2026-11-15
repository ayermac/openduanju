package data_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent/revisionepisode"
)

func TestMediaSubmissionFreezesTheRevision(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 2)

	batch := freezeBatch(t, client, repo, revision)
	if batch.ItemTotal != 2 {
		t.Fatalf("item total = %d, want 2", batch.ItemTotal)
	}
	frozen, err := client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if frozen.State != string(biz.RevisionStateProcessing) {
		t.Fatalf("revision state = %q, want %q", frozen.State, biz.RevisionStateProcessing)
	}
	if int(frozen.Version) != int(revision.Version)+1 {
		t.Fatalf("revision version = %d, want %d", frozen.Version, int(revision.Version)+1)
	}
	// The selection of a frozen revision can no longer be changed.
	catalog := data.NewSourceCatalogRepoWithDB(client)
	if _, err := catalog.SetRevisionSelection(ctx, revision.ID, int(frozen.Version), "explicit", nil); !errors.Is(err, biz.ErrRevisionVersion) {
		t.Fatalf("err = %v, want %v for a frozen revision", err, biz.ErrRevisionVersion)
	}
	selected, err := client.RevisionEpisode.Query().Where(revisionepisode.RevisionIDEQ(revision.ID)).All(ctx)
	if err != nil {
		t.Fatalf("query episodes: %v", err)
	}
	for _, episode := range selected {
		if !episode.Selected {
			t.Fatalf("episode %s lost its selection", textOf(episode.ExternalID))
		}
	}
}

func TestMediaSubmissionRejectsARevisionThatMovedOn(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	grant := seedGrant(t, client, revision.SeriesID)
	// The version of the revision moved on: the submission is rejected.
	if _, err := repo.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: int(revision.Version) + 3,
		ProfileVersion: "p", Actor: "operator",
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 1,
	}); !errors.Is(err, biz.ErrRevisionVersion) {
		t.Fatalf("err = %v, want %v", err, biz.ErrRevisionVersion)
	}
	// A revision that is not a draft cannot be processed.
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).SetState("withdrawn").Save(ctx); err != nil {
		t.Fatalf("withdraw revision: %v", err)
	}
	if _, err := repo.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: int(revision.Version),
		ProfileVersion: "p", Actor: "operator",
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 1,
	}); !errors.Is(err, biz.ErrRevisionNotEditable) {
		t.Fatalf("err = %v, want %v", err, biz.ErrRevisionNotEditable)
	}
	batches, err := client.MediaBatch.Query().All(ctx)
	if err != nil {
		t.Fatalf("query batches: %v", err)
	}
	if len(batches) != 0 {
		t.Fatalf("batches = %d, want none after a rejected submission", len(batches))
	}
}

func TestMediaBatchCannotAdvanceARevisionItNoLongerOwns(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	batch := freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	// The revision is withdrawn while the task runs.
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).SetState("withdrawn").Save(ctx); err != nil {
		t.Fatalf("withdraw revision: %v", err)
	}
	asset := biz.MediaAsset{
		Object:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: "media/one.mp4"},
		State:       biz.MediaAssetVerified,
		SizeBytes:   10,
		SHA256:      "digest-one",
		ContentType: "video/mp4",
		ExternalID:  claimed[0].ExternalID,
	}
	if _, _, err := repo.CommitItemReady(ctx, claimed[0], asset); !errors.Is(err, biz.ErrRevisionNotOwned) {
		t.Fatalf("err = %v, want %v", err, biz.ErrRevisionNotOwned)
	}
	// The gate does not open a review task for a revision nobody owns.
	if _, err := client.MediaBatch.UpdateOneID(batch.ID).SetCoverStatus(biz.MediaCoverReady).Save(ctx); err != nil {
		t.Fatalf("mark cover ready: %v", err)
	}
	if _, err := repo.RefreshReadyBatches(ctx, 20); err != nil {
		t.Fatalf("RefreshReadyBatches: %v", err)
	}
	tasks, err := client.ReviewTask.Query().All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("review tasks = %d, want none for a withdrawn revision", len(tasks))
	}
	assets, err := client.MediaAsset.Query().All(ctx)
	if err != nil {
		t.Fatalf("query assets: %v", err)
	}
	if len(assets) != 0 {
		t.Fatalf("assets = %d, want none for a withdrawn revision", len(assets))
	}
}

func TestMediaExpiredLeaseIsTakenOverAndRejectsTheWrite(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	first := claimed[0]
	// The task crashed in the middle of the processing.
	if _, err := repo.SaveItem(ctx, first); err != nil {
		t.Fatalf("SaveItem: %v", err)
	}
	if _, err := client.MediaItem.UpdateOneID(first.ID).
		SetStatus(string(biz.MediaItemProcessing)).
		SetLeaseUntil(time.Now().UTC().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	// Another worker takes the crashed task over.
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
	if taken[0].Status != biz.MediaItemProcessing {
		t.Fatalf("status = %q, want the stage the crashed task reached", taken[0].Status)
	}
	// The worker that lost the lease may not write any more.
	first.Status = biz.MediaItemProcessing
	if _, err := repo.SaveItem(ctx, first); !errors.Is(err, biz.ErrItemLeaseLost) {
		t.Fatalf("err = %v, want %v", err, biz.ErrItemLeaseLost)
	}
}

func TestMediaExpiredLeaseRejectsTheWriteWithoutATakeover(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	if _, err := client.MediaItem.UpdateOneID(claimed[0].ID).
		SetLeaseUntil(time.Now().UTC().Add(-time.Second)).Save(ctx); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	claimed[0].Status = biz.MediaItemProcessing
	if _, err := repo.SaveItem(ctx, claimed[0]); !errors.Is(err, biz.ErrItemLeaseLost) {
		t.Fatalf("err = %v, want %v for an expired lease", err, biz.ErrItemLeaseLost)
	}
}

func TestMediaLeaseRenewalExtendsTheLease(t *testing.T) {
	client := openMediaClient(t)
	repo := mediaRepo(client)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)

	freezeBatch(t, client, repo, revision)
	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil {
		t.Fatalf("ClaimItems: %v", err)
	}
	before, err := client.MediaItem.Get(ctx, claimed[0].ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	renewed, err := repo.RenewItemLease(ctx, claimed[0], 2*time.Minute)
	if err != nil {
		t.Fatalf("RenewItemLease: %v", err)
	}
	if renewed.LeaseUntil == nil || !renewed.LeaseUntil.After(before.LeaseUntil.Add(time.Minute)) {
		t.Fatalf("lease until = %v, want more than a minute after %v", renewed.LeaseUntil, before.LeaseUntil)
	}
	// A worker that lost the lease may not renew it.
	stale := claimed[0]
	stale.LeaseGeneration = claimed[0].LeaseGeneration + 5
	if _, err := repo.RenewItemLease(ctx, stale, time.Minute); !errors.Is(err, biz.ErrItemLeaseLost) {
		t.Fatalf("err = %v, want %v", err, biz.ErrItemLeaseLost)
	}
}
