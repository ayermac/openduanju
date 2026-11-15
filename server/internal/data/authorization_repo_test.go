package data_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/reviewtask"
)

// newGrant stores one grant with the given permission bits and window.
func newGrant(t *testing.T, client *ent.Client, seriesID int64, process bool, display bool, playback bool, from time.Time, until *time.Time) *ent.AuthorizationGrant {
	t.Helper()
	create := client.AuthorizationGrant.Create().
		SetSeriesID(seriesID).
		SetReference("AUTH-TEST").
		SetAllowProcess(process).
		SetAllowDisplay(display).
		SetAllowPlayback(playback).
		SetValidFrom(from.UTC()).
		SetStatus(biz.GrantActive).
		SetVersion(1).
		SetConfirmedBy("tester").
		SetConfirmedAt(time.Now().UTC())
	if until != nil {
		create = create.SetValidUntil(until.UTC())
	}
	grant, err := create.Save(context.Background())
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return grant
}

func TestAuthorizationCreateRevokeAndList(t *testing.T) {
	client := openMediaClient(t)
	revision := seedRevision(t, client, 1)
	repo := data.NewAuthorizationRepoWithDB(client)
	uc := biz.NewAuthorizations(repo, nil)
	ctx := context.Background()
	from := time.Now().UTC().Add(-time.Hour)

	grant, err := uc.Create(ctx, biz.CreateGrantRequest{
		SeriesID: revision.SeriesID, Reference: "AUTH-001", AllowProcess: true,
		ValidFrom: from, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if grant.AllowDisplay || grant.AllowPlayback {
		t.Fatal("an omitted permission was granted")
	}
	if grant.Status != biz.GrantActive || grant.Version != 1 || grant.ConfirmedBy != "tester" {
		t.Fatalf("grant: %+v", grant)
	}
	// Revocation is a version CAS and keeps the confirmation facts.
	revoked, err := uc.Revoke(ctx, grant.ID, 1, "admin", "授权方要求撤回")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked.Status != biz.GrantRevoked || revoked.Version != 2 {
		t.Fatalf("revoked grant: %+v", revoked)
	}
	if revoked.RevokedBy != "admin" || revoked.RevokeReason != "授权方要求撤回" || revoked.RevokedAt == nil {
		t.Fatalf("revocation facts: %+v", revoked)
	}
	if revoked.ConfirmedBy != "tester" || !revoked.ConfirmedAt.Equal(grant.ConfirmedAt) {
		t.Fatal("revocation rewrote the confirmation")
	}
	// The same version is idempotent; an old version conflicts.
	if _, err := uc.Revoke(ctx, grant.ID, 2, "admin", "重复"); err != nil {
		t.Fatalf("repeated revoke: %v", err)
	}
	if _, err := uc.Revoke(ctx, grant.ID, 1, "admin", "旧版本"); !errors.Is(err, biz.ErrGrantVersion) {
		t.Fatalf("stale revoke: %v", err)
	}
	set, total, err := uc.List(ctx, biz.GrantQuery{SeriesID: revision.SeriesID, Status: biz.GrantRevoked})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(set) != 1 || set[0].ID != grant.ID {
		t.Fatalf("list: total=%d items=%d", total, len(set))
	}
	if _, _, err := uc.List(ctx, biz.GrantQuery{SeriesID: revision.SeriesID, Status: biz.GrantActive}); err != nil {
		t.Fatalf("list active: %v", err)
	}
}

func TestAuthorizationCreateRejectsAnUnknownSeries(t *testing.T) {
	client := openMediaClient(t)
	uc := biz.NewAuthorizations(data.NewAuthorizationRepoWithDB(client), nil)
	_, err := uc.Create(context.Background(), biz.CreateGrantRequest{
		SeriesID: 99999, Reference: "AUTH-X", ValidFrom: time.Now().UTC(), Actor: "tester",
	})
	if !errors.Is(err, biz.ErrSeriesNotFound) {
		t.Fatalf("unknown series: %v", err)
	}
}

func TestAuthorizationSubmissionRejectsInvalidGrantsWithoutSideEffects(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	revision := seedRevision(t, client, 2)
	other := seedRevision(t, client, 1)
	repo := mediaRepo(client)
	authRepo := data.NewAuthorizationRepoWithDB(client)
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	pastUntil := now.Add(-time.Minute)

	cases := []struct {
		name   string
		grant  *ent.AuthorizationGrant
		expect error
	}{
		{"not started yet", newGrant(t, client, revision.SeriesID, true, true, true, future, nil), biz.ErrGrantNotEffective},
		{"expired", newGrant(t, client, revision.SeriesID, true, true, true, past, &pastUntil), biz.ErrGrantNotEffective},
		{"process not allowed", newGrant(t, client, revision.SeriesID, false, true, true, past, nil), biz.ErrGrantPermission},
		{"another series", newGrant(t, client, other.SeriesID, true, true, true, past, nil), biz.ErrGrantNotApplicable},
		{"version drift", newGrant(t, client, revision.SeriesID, true, true, true, past, nil), biz.ErrGrantVersion},
	}
	for _, testCase := range cases {
		expectedVersion := 1
		if testCase.name == "version drift" {
			expectedVersion = 2
		}
		_, err := repo.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
			RevisionID: revision.ID, ExpectedVersion: int(revision.Version),
			ProfileVersion: "web-mp4-h264-aac-v1", Actor: "operator",
			AuthorizationGrantID: testCase.grant.ID, ExpectedAuthorizationVersion: expectedVersion,
		})
		if !errors.Is(err, testCase.expect) {
			t.Fatalf("%s: err = %v, want %v", testCase.name, err, testCase.expect)
		}
	}
	// A revoked grant is refused too.
	revocable := newGrant(t, client, revision.SeriesID, true, true, true, past, nil)
	if _, err := authRepo.RevokeGrant(ctx, revocable.ID, 1, "admin", "撤回"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := repo.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: int(revision.Version),
		ProfileVersion: "web-mp4-h264-aac-v1", Actor: "operator",
		AuthorizationGrantID: revocable.ID, ExpectedAuthorizationVersion: 2,
	}); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("revoked grant: %v", err)
	}

	// None of the rejected submissions left a trace.
	stored, err := client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if stored.State != biz.RevisionStateDraft || stored.AuthorizationGrantID != 0 {
		t.Fatalf("revision changed: state=%s grant=%d", stored.State, stored.AuthorizationGrantID)
	}
	if batches, err := client.MediaBatch.Query().All(ctx); err != nil || len(batches) != 0 {
		t.Fatalf("batches after rejected submissions: %d err=%v", len(batches), err)
	}
	if items, err := client.MediaItem.Query().All(ctx); err != nil || len(items) != 0 {
		t.Fatalf("items after rejected submissions: %d err=%v", len(items), err)
	}
}

func TestAuthorizationIsFrozenOnTheRevision(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)
	repo := mediaRepo(client)
	now := time.Now().UTC()
	grant := newGrant(t, client, revision.SeriesID, true, true, true, now.Add(-time.Hour), nil)
	domainGrant, err := data.NewAuthorizationRepoWithDB(client).GetGrant(ctx, grant.ID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}

	batch := freezeWithGrant(t, repo, revision, grant.ID, 1)
	if batch.ID == 0 {
		t.Fatal("the batch was not created")
	}
	frozen, err := client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if frozen.AuthorizationGrantID != grant.ID || int(frozen.AuthorizationVersion) != 1 {
		t.Fatalf("frozen grant: id=%d version=%d", frozen.AuthorizationGrantID, frozen.AuthorizationVersion)
	}
	if frozen.AuthorizationHash != domainGrant.Fingerprint() {
		t.Fatalf("frozen hash: %s want %s", frozen.AuthorizationHash, domainGrant.Fingerprint())
	}
	// A newer grant never rewrites the frozen one.
	newer := newGrant(t, client, revision.SeriesID, true, true, true, now, nil)
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.AuthorizationGrantID == newer.ID {
		t.Fatal("a new grant replaced the frozen authorization")
	}
	if stored.AuthorizationHash != frozen.AuthorizationHash {
		t.Fatal("the frozen fingerprint changed")
	}
}

func TestAuthorizationReviewGateRequiresDisplayAndPlayback(t *testing.T) {
	cases := []struct {
		name     string
		display  bool
		playback bool
	}{
		{"display missing", false, true},
		{"playback missing", true, false},
	}
	for _, testCase := range cases {
		client := openMediaClient(t)
		ctx := context.Background()
		revision := seedRevision(t, client, 1)
		repo := mediaRepo(client)
		now := time.Now().UTC()
		grant := newGrant(t, client, revision.SeriesID, true, testCase.display, testCase.playback, now.Add(-time.Hour), nil)
		batch := freezeWithGrant(t, repo, revision, grant.ID, 1)
		if _, err := client.MediaItem.Update().Where(mediaitem.BatchIDEQ(batch.ID)).
			SetStatus(string(biz.MediaItemReady)).Save(ctx); err != nil {
			t.Fatalf("mark ready: %v", err)
		}
		if _, err := client.MediaBatch.UpdateOneID(batch.ID).SetCoverStatus(biz.MediaCoverReady).Save(ctx); err != nil {
			t.Fatalf("cover ready: %v", err)
		}
		if _, err := repo.RefreshReadyBatches(ctx, 10); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if count, _ := client.ReviewTask.Query().Where(reviewtask.RevisionIDEQ(revision.ID)).Count(ctx); count != 0 {
			t.Fatalf("%s: %d review tasks were opened", testCase.name, count)
		}
		storedBatch, _ := client.MediaBatch.Get(ctx, batch.ID)
		if storedBatch.Status != string(biz.MediaBatchFailed) || storedBatch.ErrorCode != "AUTHORIZATION_INVALID" {
			t.Fatalf("%s: batch state=%s code=%s", testCase.name, storedBatch.Status, storedBatch.ErrorCode)
		}
	}
}

func TestAuthorizationRevokedDuringProcessingStopsReady(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)
	repo := mediaRepo(client)
	authRepo := data.NewAuthorizationRepoWithDB(client)
	now := time.Now().UTC()
	grant := newGrant(t, client, revision.SeriesID, true, true, true, now.Add(-time.Hour), nil)
	batch := freezeWithGrant(t, repo, revision, grant.ID, 1)

	claimed, err := repo.ClaimItems(ctx, "worker-a", time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v n=%d", err, len(claimed))
	}
	if _, err := authRepo.RevokeGrant(ctx, grant.ID, 1, "admin", "处理中撤销"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	asset := biz.MediaAsset{
		Object:    biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: "media/grant.mp4"},
		State:     biz.MediaAssetVerified,
		SizeBytes: 10, SHA256: "digest", ContentType: "video/mp4", ExternalID: claimed[0].ExternalID,
	}
	if _, _, err := repo.CommitItemReady(ctx, claimed[0], asset); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("ready commit after revocation: %v", err)
	}
	item, _ := client.MediaItem.Get(ctx, claimed[0].ID)
	if item.Status == string(biz.MediaItemReady) {
		t.Fatal("a revoked grant still produced ready material")
	}
	if count, _ := client.MediaAsset.Query().Count(ctx); count != 0 {
		t.Fatalf("assets after a rejected ready commit: %d", count)
	}
	// The worker gate rejects the same batch before it starts another task.
	if err := repo.EnsureBatchAuthorization(ctx, batch.ID, biz.PermissionProcess); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("batch authorization: %v", err)
	}
	if _, err := repo.RefreshReadyBatches(ctx, 10); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if count, _ := client.ReviewTask.Query().Count(ctx); count != 0 {
		t.Fatalf("review tasks after revocation: %d", count)
	}
}

func TestAuthorizationRevocationBlocksPublicChecksEveryTime(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)
	repo := data.NewAuthorizationRepoWithDB(client)
	now := time.Now().UTC()
	grant := newGrant(t, client, revision.SeriesID, true, true, true, now.Add(-time.Hour), nil)
	domain, err := repo.GetGrant(ctx, grant.ID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}
	// A granted series is readable and playable while the grant is in force.
	if err := biz.EnsureForDisplay(domain, revision.SeriesID, now); err != nil {
		t.Fatalf("display before revocation: %v", err)
	}
	if err := biz.EnsureForPlayback(domain, revision.SeriesID, now); err != nil {
		t.Fatalf("playback before revocation: %v", err)
	}
	if _, err := repo.RevokeGrant(ctx, grant.ID, 1, "admin", "撤权"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Every later check reads the stored row again; a cached "allowed" answer
	// cannot survive the revocation.
	for attempt := 0; attempt < 3; attempt++ {
		reloaded, err := repo.GetGrant(ctx, grant.ID)
		if err != nil {
			t.Fatalf("reload grant: %v", err)
		}
		if err := biz.EnsureForDisplay(reloaded, revision.SeriesID, time.Now().UTC()); !errors.Is(err, biz.ErrGrantNotEffective) {
			t.Fatalf("display after revocation: %v", err)
		}
		if err := biz.EnsureForPlayback(reloaded, revision.SeriesID, time.Now().UTC()); !errors.Is(err, biz.ErrGrantNotEffective) {
			t.Fatalf("playback after revocation: %v", err)
		}
	}
	// An expired window behaves the same way without a status change.
	ended := now.Add(-time.Minute)
	expiring := newGrant(t, client, revision.SeriesID, true, true, true, now.Add(-time.Hour), &ended)
	expiredDomain, _ := repo.GetGrant(ctx, expiring.ID)
	if err := biz.EnsureForPlayback(expiredDomain, revision.SeriesID, time.Now().UTC()); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("playback on an expired grant: %v", err)
	}
}

func TestAuthorizationRevokedBeforeSubmitIsNeverFrozen(t *testing.T) {
	client := openMediaClient(t)
	ctx := context.Background()
	revision := seedRevision(t, client, 1)
	repo := mediaRepo(client)
	authRepo := data.NewAuthorizationRepoWithDB(client)
	now := time.Now().UTC()
	grant := newGrant(t, client, revision.SeriesID, true, true, true, now.Add(-time.Hour), nil)

	// The revocation wins the race: a submission that starts afterwards must
	// not freeze the revoked grant.
	if _, err := authRepo.RevokeGrant(ctx, grant.ID, 1, "admin", "并发撤销"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := repo.FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: int(revision.Version),
		ProfileVersion: "web-mp4-h264-aac-v1", Actor: "operator",
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 2,
	})
	if !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("submit after revocation: %v", err)
	}
	if batches, _ := client.MediaBatch.Query().All(ctx); len(batches) != 0 {
		t.Fatalf("batches after a revoked submission: %d", len(batches))
	}

	// The other order is allowed: a batch frozen before the revocation keeps
	// its snapshot, but every later step is blocked.
	other := seedRevision(t, client, 1)
	second := newGrant(t, client, other.SeriesID, true, true, true, now.Add(-time.Hour), nil)
	batch := freezeWithGrant(t, repo, other, second.ID, 1)
	if _, err := authRepo.RevokeGrant(ctx, second.ID, 1, "admin", "冻结后撤销"); err != nil {
		t.Fatalf("revoke after freeze: %v", err)
	}
	stored, _ := client.ContentRevision.Get(ctx, other.ID)
	if stored.AuthorizationGrantID != second.ID {
		t.Fatal("the frozen snapshot was rewritten by the revocation")
	}
	if err := repo.EnsureBatchAuthorization(ctx, batch.ID, biz.PermissionProcess); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("batch after revocation: %v", err)
	}
}

// freezeWithGrant submits a revision with an explicit grant.
func freezeWithGrant(t *testing.T, repo *data.MediaBatchRepo, revision *ent.ContentRevision, grantID int64, expectedVersion int) *biz.MediaBatch {
	t.Helper()
	batch, err := repo.FreezeAndCreateBatch(context.Background(), biz.FreezeBatchRequest{
		RevisionID:                   revision.ID,
		ExpectedVersion:              int(revision.Version),
		ProfileVersion:               "web-mp4-h264-aac-v1",
		Actor:                        "operator",
		AuthorizationGrantID:         grantID,
		ExpectedAuthorizationVersion: expectedVersion,
	})
	if err != nil {
		t.Fatalf("FreezeAndCreateBatch: %v", err)
	}
	return batch
}
