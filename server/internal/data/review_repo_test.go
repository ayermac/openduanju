package data_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/loginattempt"
	"openduanju-server/internal/data/ent/mediaitem"
	"openduanju-server/internal/data/ent/reviewtask"
)

// seedReviewTask builds a revision whose material is ready and whose review
// task is open, submitted by the given operator.
func seedReviewTask(t *testing.T, client *ent.Client, submittedBy string) (*ent.ReviewTask, *ent.ContentRevision, *ent.AuthorizationGrant) {
	t.Helper()
	ctx := context.Background()
	revision := seedRevision(t, client, 1)
	now := time.Now().UTC()
	grant := newGrant(t, client, revision.SeriesID, true, true, true, now.Add(-time.Hour), nil)
	batch, err := mediaRepo(client).FreezeAndCreateBatch(ctx, biz.FreezeBatchRequest{
		RevisionID: revision.ID, ExpectedVersion: int(revision.Version),
		ProfileVersion: "web-mp4-h264-aac-v1", Actor: submittedBy,
		AuthorizationGrantID: grant.ID, ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if _, err := client.MediaItem.Update().Where(mediaitem.BatchIDEQ(batch.ID)).
		SetStatus(string(biz.MediaItemReady)).Save(ctx); err != nil {
		t.Fatalf("ready items: %v", err)
	}
	if _, err := client.MediaBatch.UpdateOneID(batch.ID).SetCoverStatus(biz.MediaCoverReady).Save(ctx); err != nil {
		t.Fatalf("cover ready: %v", err)
	}
	if _, err := mediaRepo(client).RefreshReadyBatches(ctx, 10); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	task, err := client.ReviewTask.Query().
		Where(reviewtask.RevisionIDEQ(revision.ID)).
		Only(ctx)
	if err != nil {
		t.Fatalf("review task: %v", err)
	}
	stored, err := client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	return task, stored, grant
}

// approvedChecklist is the minimum a reviewer confirms before an approval.
func approvedChecklist() biz.ReviewChecklist {
	return biz.ReviewChecklist{
		TitleCoverIntro: true, EpisodeMapping: true, AudioVideo: true,
		AuthorizationPublic: true, ReviewedEpisodes: 1,
	}
}

func TestReviewClaimIsExclusiveUnderConcurrency(t *testing.T) {
	client := openMediaClient(t)
	task, _, _ := seedReviewTask(t, client, "operator")
	repo := data.NewReviewRepoWithDB(client)
	ctx := context.Background()

	// Two reviewers claim the same task at the same time.
	var wait sync.WaitGroup
	results := make([]error, 2)
	for index, actor := range []string{"reviewer-a", "reviewer-b"} {
		wait.Add(1)
		go func(index int, actor string) {
			defer wait.Done()
			_, results[index] = repo.ClaimReview(ctx, task.ID, task.Version, actor)
		}(index, actor)
	}
	wait.Wait()
	success := 0
	for _, err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("successful claims = %d, want exactly 1 (%v)", success, results)
	}
	stored, err := client.ReviewTask.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Status != biz.ReviewClaimed || stored.Assignee == "" {
		t.Fatalf("task after claim: status=%s assignee=%s", stored.Status, stored.Assignee)
	}
}

func TestReviewRejectsSelfReviewAndForeignDecisions(t *testing.T) {
	client := openMediaClient(t)
	task, _, _ := seedReviewTask(t, client, "operator")
	repo := data.NewReviewRepoWithDB(client)
	ctx := context.Background()

	// The author cannot claim their own submission.
	if _, err := repo.ClaimReview(ctx, task.ID, task.Version, "operator"); !errors.Is(err, biz.ErrReviewSelfDecision) {
		t.Fatalf("self claim: %v", err)
	}
	claimed, err := repo.ClaimReview(ctx, task.ID, task.Version, "reviewer")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Another reviewer cannot decide a task they did not claim.
	if _, err := repo.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "reviewer2", Checklist: approvedChecklist(),
	}); !errors.Is(err, biz.ErrReviewNotAssignee) {
		t.Fatalf("foreign decision: %v", err)
	}
	// The author cannot decide even after claiming is done elsewhere.
	if _, err := repo.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "operator", Checklist: approvedChecklist(),
	}); !errors.Is(err, biz.ErrReviewNotAssignee) && !errors.Is(err, biz.ErrReviewSelfDecision) {
		t.Fatalf("author decision: %v", err)
	}
	// A stale version is a conflict.
	if _, err := repo.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version + 5, Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	}); !errors.Is(err, biz.ErrReviewVersion) {
		t.Fatalf("stale decision: %v", err)
	}
}

func TestReviewApprovalRechecksAuthorizationAndNeverPublishes(t *testing.T) {
	client := openMediaClient(t)
	task, revision, grant := seedReviewTask(t, client, "operator")
	repo := data.NewReviewRepoWithDB(client)
	ctx := context.Background()
	claimed, err := repo.ClaimReview(ctx, task.ID, task.Version, "reviewer")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The authorization is revoked during the review.
	if _, err := data.NewAuthorizationRepoWithDB(client).RevokeGrant(ctx, grant.ID, 1, "admin", "撤回"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := repo.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	}); !errors.Is(err, biz.ErrReviewPrecondition) {
		t.Fatalf("approval with a revoked grant: %v", err)
	}
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.State != biz.RevisionStatePendingReview {
		t.Fatalf("revision after a rejected approval: %s", stored.State)
	}

	// A valid grant lets the approval pass, and approval alone does not
	// publish.
	second, revisionTwo, _ := seedReviewTask(t, client, "operator")
	claimedTwo, err := repo.ClaimReview(ctx, second.ID, second.Version, "reviewer")
	if err != nil {
		t.Fatalf("claim second: %v", err)
	}
	approved, err := repo.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: second.ID, ExpectedVersion: claimedTwo.Version, Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != biz.ReviewApproved {
		t.Fatalf("task status: %s", approved.Status)
	}
	after, _ := client.ContentRevision.Get(ctx, revisionTwo.ID)
	if after.State != biz.RevisionStateApproved {
		t.Fatalf("revision state after approval: %s", after.State)
	}
	if count, _ := client.Publication.Query().Count(ctx); count != 0 {
		t.Fatalf("approval created %d publications", count)
	}
	// The decision is final.
	if _, err := repo.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: second.ID, ExpectedVersion: approved.Version, Decision: biz.DecisionReject, Reason: "改主意", Actor: "reviewer",
	}); !errors.Is(err, biz.ErrReviewFinal) {
		t.Fatalf("second decision: %v", err)
	}
}

func TestReviewRejectionNeedsAReasonAndMakesTheRevisionHistory(t *testing.T) {
	client := openMediaClient(t)
	task, revision, _ := seedReviewTask(t, client, "operator")
	repo := data.NewReviewRepoWithDB(client)
	ctx := context.Background()
	review := biz.NewReviews(repo, nil)
	claimed, err := review.Claim(ctx, task.ID, task.Version, "reviewer")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := review.Decide(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionReject, Actor: "reviewer",
	}); !errors.Is(err, biz.ErrReviewReason) {
		t.Fatalf("rejection without reason: %v", err)
	}
	rejected, err := review.Decide(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionReject,
		Reason: "第2集音画不同步", Actor: "reviewer",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != biz.ReviewRejected {
		t.Fatalf("task status: %s", rejected.Status)
	}
	// The rejected revision keeps the material that was judged and is not an
	// editable draft any more: the operator creates a new draft from it.
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.State != biz.RevisionStateRejected {
		t.Fatalf("revision after rejection: %s", stored.State)
	}
}

func TestPublicationFreezesTheApprovedRevision(t *testing.T) {
	client := openMediaClient(t)
	task, revision, _ := seedReviewTask(t, client, "operator")
	repo := data.NewReviewRepoWithDB(client)
	review := biz.NewReviews(repo, nil)
	ctx := context.Background()

	// Publication is refused before approval.
	if _, err := review.Publish(ctx, revision.SeriesID, revision.ID, "operator"); !errors.Is(err, biz.ErrReviewNotApproved) {
		t.Fatalf("publish before approval: %v", err)
	}
	claimed, _ := review.Claim(ctx, task.ID, task.Version, "reviewer")
	if _, err := review.Decide(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	publication, err := review.Publish(ctx, revision.SeriesID, revision.ID, "operator")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publication.RevisionID != revision.ID || publication.Status != biz.PublicationPublished {
		t.Fatalf("publication: %+v", publication)
	}
	if publication.AuthorizationGrantID == 0 {
		t.Fatal("the publication did not freeze the authorization")
	}
	series, _ := client.ContentSeries.Get(ctx, revision.SeriesID)
	if series.PublishedRevisionID == nil || *series.PublishedRevisionID != revision.ID {
		t.Fatalf("series published revision: %v", series.PublishedRevisionID)
	}
	// Publishing again is idempotent.
	again, err := review.Publish(ctx, revision.SeriesID, revision.ID, "operator")
	if err != nil || again.ID != publication.ID {
		t.Fatalf("repeat publish: %v id=%d", err, again.ID)
	}
	// A new draft of the series does not touch the published reference.
	draft, err := client.ContentRevision.Create().
		SetSeriesID(revision.SeriesID).
		SetRevisionNo(revision.RevisionNo + 1).
		SetState(biz.RevisionStateDraft).
		SetVersion(1).
		SetTitle(revision.Title).
		SetDescription(revision.Description).
		Save(ctx)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	stored, _ := review.Publication(ctx, revision.SeriesID)
	if stored.RevisionID != revision.ID || stored.Status != biz.PublicationPublished {
		t.Fatalf("publication changed by a draft: %+v", stored)
	}
	if draft.ID == stored.RevisionID {
		t.Fatal("a draft became the published revision")
	}
	// Unpublish needs the current version, a stated reason, and keeps the
	// history row.
	if _, err := review.Unpublish(ctx, revision.SeriesID, stored.Version+3, "operator", "下架验证"); !errors.Is(err, biz.ErrPublicationVersion) {
		t.Fatalf("stale unpublish: %v", err)
	}
	if _, err := review.Unpublish(ctx, revision.SeriesID, stored.Version, "operator", "   "); !errors.Is(err, biz.ErrUnpublishReason) {
		t.Fatalf("unpublish without a reason: %v", err)
	}
	taken, err := review.Unpublish(ctx, revision.SeriesID, stored.Version, "operator", "下架验证")
	if err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if taken.Status != biz.PublicationUnpublished || taken.UnpublishedBy != "operator" {
		t.Fatalf("unpublished: %+v", taken)
	}
	series, _ = client.ContentSeries.Get(ctx, revision.SeriesID)
	if series.PublishedRevisionID != nil && *series.PublishedRevisionID != 0 {
		t.Fatalf("series still points at %v", series.PublishedRevisionID)
	}
}

func TestLoginThrottleBlocksAndRecovers(t *testing.T) {
	client, _, accounts := newAccounts(t)
	ctx := context.Background()
	if _, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "throttled", Role: biz.RoleAdmin, Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Unknown usernames count the same way, so the throttle does not reveal
	// which accounts exist.
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := accounts.Login(ctx, "ghost", "whatever-password"); !errors.Is(err, biz.ErrInvalidCredentials) {
			t.Fatalf("unknown user attempt %d: %v", attempt, err)
		}
	}
	if _, err := accounts.Login(ctx, "ghost", "whatever-password"); !errors.Is(err, biz.ErrTooManyAttempts) {
		t.Fatalf("unknown user after the limit: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := accounts.Login(ctx, "throttled", "wrong password"); !errors.Is(err, biz.ErrInvalidCredentials) {
			t.Fatalf("wrong password attempt %d: %v", attempt, err)
		}
	}
	// The correct password is refused while the window is full.
	if _, err := accounts.Login(ctx, "throttled", "correct horse battery"); !errors.Is(err, biz.ErrTooManyAttempts) {
		t.Fatalf("correct password during the window: %v", err)
	}
	// Moving the failures out of the window recovers the account; there is no
	// permanent lock.
	if _, err := client.LoginAttempt.Update().
		Where(loginattempt.UsernameEQ("throttled")).
		SetAttemptedAt(time.Now().UTC().Add(-20 * time.Minute)).
		Save(ctx); err != nil {
		t.Fatalf("age attempts: %v", err)
	}
	if _, err := accounts.Login(ctx, "throttled", "correct horse battery"); err != nil {
		t.Fatalf("login after the window: %v", err)
	}
}

func TestReviewApprovalRequiresTheChecklist(t *testing.T) {
	client := openMediaClient(t)
	task, revision, _ := seedReviewTask(t, client, "operator")
	repo := data.NewReviewRepoWithDB(client)
	review := biz.NewReviews(repo, nil)
	ctx := context.Background()
	claimed, err := review.Claim(ctx, task.ID, task.Version, "reviewer")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// An approval without the checklist never reaches the repository.
	if _, err := review.Decide(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "reviewer",
	}); !errors.Is(err, biz.ErrReviewChecklist) {
		t.Fatalf("approval without a checklist: %v", err)
	}
	// Claiming more episodes than the batch holds is refused as well.
	over := approvedChecklist()
	over.ReviewedEpisodes = 9
	if _, err := review.Decide(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: over,
	}); err == nil {
		t.Fatal("an impossible episode count was accepted")
	}
	stored, _ := client.ContentRevision.Get(ctx, revision.ID)
	if stored.State != biz.RevisionStatePendingReview {
		t.Fatalf("revision after refused approvals: %s", stored.State)
	}
	if _, err := review.Decide(ctx, biz.DecideReviewRequest{
		TaskID: task.ID, ExpectedVersion: claimed.Version, Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	}); err != nil {
		t.Fatalf("approval with the checklist: %v", err)
	}
	row, err := client.ReviewDecision.Query().Only(ctx)
	if err != nil {
		t.Fatalf("decision row: %v", err)
	}
	if row.Checklist == "" || !strings.Contains(row.Checklist, "title_cover_intro") {
		t.Fatalf("the decision did not freeze the checklist: %q", row.Checklist)
	}
}
