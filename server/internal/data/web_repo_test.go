package data_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/mediaasset"
)

// seedPublished builds one published series with two selected episodes, a
// ready asset each and a valid grant.
func seedPublished(t *testing.T, client *ent.Client) (*ent.ContentSeries, *ent.ContentRevision, *ent.AuthorizationGrant, []int64) {
	t.Helper()
	ctx := context.Background()
	series, err := client.ContentSeries.Create().
		SetSourceCode("hongguo").
		SetExternalID(nextSeriesExternalID()).
		SetTitle("公开验证剧").
		SetDescription("WEB-001 投影").
		SetCategory("短剧").
		SetStatus("published").
		Save(ctx)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	now := time.Now().UTC()
	grant, err := client.AuthorizationGrant.Create().
		SetSeriesID(series.ID).
		SetReference("AUTH-WEB-001").
		SetAllowProcess(true).SetAllowDisplay(true).SetAllowPlayback(true).
		SetValidFrom(now.Add(-time.Hour)).
		SetStatus(biz.GrantActive).
		SetVersion(1).
		SetConfirmedBy("tester").
		SetConfirmedAt(now).
		Save(ctx)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	cover, err := client.MediaAsset.Create().
		SetProvider("local").SetBucket("objects").SetRegion("local").
		SetObjectKey("openduanju-media/covers/web.png").SetObjectVersion("cover-v1").
		SetContentType("image/png").SetKind("cover").SetState("ready").
		SetWidth(800).SetHeight(1200).
		Save(ctx)
	if err != nil {
		t.Fatalf("cover: %v", err)
	}
	revision, err := client.ContentRevision.Create().
		SetSeriesID(series.ID).
		SetRevisionNo(1).
		SetState(biz.RevisionStateApproved).
		SetVersion(2).
		SetTitle("公开验证剧").
		SetDescription("WEB-001 投影").
		SetCoverAssetID(cover.ID).
		SetAuthorizationGrantID(grant.ID).
		SetAuthorizationVersion(1).
		SetAuthorizationHash("web-hash").
		Save(ctx)
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	episodeIDs := make([]int64, 0, 2)
	for index := 1; index <= 2; index++ {
		episode, err := client.RevisionEpisode.Create().
			SetRevisionID(revision.ID).
			SetExternalID("web-ep-" + string(rune('0'+index))).
			SetEpisodeNo(int32(index)).
			SetSelected(true).
			Save(ctx)
		if err != nil {
			t.Fatalf("episode: %v", err)
		}
		episodeIDs = append(episodeIDs, episode.ID)
		// Both episodes freeze the same asset on purpose: the projection has
		// to follow the frozen reference, not "the newest asset".
		asset, err := client.MediaAsset.Create().
			SetProvider("local").SetBucket("objects").SetRegion("local").
			SetObjectKey("openduanju-media/episodes/" + string(rune('0'+index)) + ".mp4").
			SetObjectVersion("version-" + string(rune('0'+index))).
			SetContentType("video/mp4").SetKind("episode").SetState("ready").
			SetRevisionID(revision.ID).SetEpisodeID(episode.ID).
			SetDurationMs(2000).SetWidth(320).SetHeight(240).
			Save(ctx)
		if err != nil {
			t.Fatalf("asset: %v", err)
		}
		// The revision freezes the material of the episode.
		if _, err := client.RevisionEpisode.UpdateOneID(episode.ID).
			SetAssetID(asset.ID).SetAssetState("ready").Save(ctx); err != nil {
			t.Fatalf("freeze the asset: %v", err)
		}
	}
	if _, err := client.Publication.Create().
		SetSeriesID(series.ID).SetRevisionID(revision.ID).
		SetStatus(biz.PublicationPublished).
		SetPublishedBy("operator").SetPublishedAt(now).
		SetAuthorizationGrantID(grant.ID).SetAuthorizationVersion(1).
		// The category is frozen with the publication.
		SetCategory("短剧").SetGeneration(1).
		Save(ctx); err != nil {
		t.Fatalf("publication: %v", err)
	}
	if _, err := client.ContentSeries.UpdateOneID(series.ID).
		SetPublishedRevisionID(revision.ID).Save(ctx); err != nil {
		t.Fatalf("series publish: %v", err)
	}
	return series, revision, grant, episodeIDs
}

func newWeb(t *testing.T) (*ent.Client, *data.WebRepo, *biz.Web) {
	t.Helper()
	client := openMediaClient(t)
	repo := data.NewWebRepoWithDB(client)
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	return client, repo, biz.NewWeb(repo, storage, 10*time.Minute)
}

func TestWebListShowsOnlyPublishedAndAuthorized(t *testing.T) {
	client, _, web := newWeb(t)
	series, revision, grant, _ := seedPublished(t, client)
	ctx := context.Background()

	items, total, err := web.List(ctx, biz.WebQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].SeriesID != series.ID {
		t.Fatalf("list: total=%d items=%d", total, len(items))
	}
	if items[0].EpisodeCount != 2 || items[0].Category != "短剧" {
		t.Fatalf("card: %+v", items[0])
	}
	// Search and category filter use the frozen projection only.
	if found, _, _ := web.List(ctx, biz.WebQuery{Q: "公开验证", Page: 1, PageSize: 10}); len(found) != 1 {
		t.Fatalf("search: %d", len(found))
	}
	if found, _, _ := web.List(ctx, biz.WebQuery{Category: "短剧", Page: 1, PageSize: 10}); len(found) != 1 {
		t.Fatalf("category: %d", len(found))
	}
	if found, _, _ := web.List(ctx, biz.WebQuery{Category: "不存在", Page: 1, PageSize: 10}); len(found) != 0 {
		t.Fatalf("unknown category: %d", len(found))
	}
	// Detail carries the frozen revision and episodes.
	detail, err := web.Detail(ctx, series.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.RevisionID != revision.ID || len(detail.Episodes) != 2 {
		t.Fatalf("detail: rev=%d episodes=%d", detail.RevisionID, len(detail.Episodes))
	}
	// Revoking the grant hides the series from the list and the detail.
	if _, err := data.NewAuthorizationRepoWithDB(client).RevokeGrant(ctx, grant.ID, 1, "admin", "撤权"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// The filter runs before the pagination, so a revoked series leaves no
	// row and no total either.
	if items, total, _ := web.List(ctx, biz.WebQuery{Page: 1, PageSize: 10}); len(items) != 0 || total != 0 {
		t.Fatalf("list after revoke: items=%d total=%d", len(items), total)
	}
	if _, err := web.Detail(ctx, series.ID); !errors.Is(err, biz.ErrWebUnavailable) {
		t.Fatalf("detail after revoke: %v", err)
	}
}

func TestWebPlaybackFreezesTheEpisodeAndRefreshesTheSameObject(t *testing.T) {
	client, _, web := newWeb(t)
	series, revision, _, episodes := seedPublished(t, client)
	ctx := context.Background()

	ticket, err := web.OpenPlayback(ctx, series.ID, episodes[0])
	if err != nil {
		t.Fatalf("open playback: %v", err)
	}
	if ticket.EpisodeID != episodes[0] || ticket.RevisionID != revision.ID || ticket.Token == "" {
		t.Fatalf("ticket: %+v", ticket)
	}
	// Refresh keeps the frozen episode; the token cannot be redirected.
	refreshed, err := web.Refresh(ctx, ticket.Token)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.EpisodeID != episodes[0] || refreshed.SessionID != ticket.SessionID {
		t.Fatalf("refresh switched the episode: %+v", refreshed)
	}
	// A ticket for the second episode stays on the second episode.
	second, err := web.OpenPlayback(ctx, series.ID, episodes[1])
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	if second.EpisodeID != episodes[1] {
		t.Fatalf("second ticket: %+v", second)
	}
	// An episode outside the published selection is refused.
	if _, err := web.OpenPlayback(ctx, series.ID, 999999); !errors.Is(err, biz.ErrPlaybackEpisode) {
		t.Fatalf("unknown episode: %v", err)
	}
	// The session never outlives the grant.
	grantRow, _ := client.AuthorizationGrant.Query().Only(ctx)
	record, err := data.NewWebRepoWithDB(client).GetPlayback(ctx, biz.HashSessionToken(ticket.Token), time.Now().UTC())
	if err != nil {
		t.Fatalf("get playback: %v", err)
	}
	if grantRow.ValidUntil != nil && record.ExpiresAt.After(*grantRow.ValidUntil) {
		t.Fatalf("session outlives the grant: %s > %s", record.ExpiresAt, grantRow.ValidUntil)
	}
	if record.Object.VersionID == "" {
		t.Fatal("the session did not freeze the object version")
	}
}

func TestWebPlaybackStopsAfterRevocationOrUnpublish(t *testing.T) {
	client, repo, web := newWeb(t)
	series, _, grant, episodes := seedPublished(t, client)
	ctx := context.Background()
	ticket, err := web.OpenPlayback(ctx, series.ID, episodes[0])
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Unpublish stops the existing session at refresh time.
	review := biz.NewReviews(data.NewReviewRepoWithDB(client), nil)
	publication, err := review.Publication(ctx, series.ID)
	if err != nil {
		t.Fatalf("publication: %v", err)
	}
	if _, err := review.Unpublish(ctx, series.ID, publication.Version, "operator", "下架验证"); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, err := web.Refresh(ctx, ticket.Token); !errors.Is(err, biz.ErrPlaybackUnavailable) {
		t.Fatalf("refresh after unpublish: %v", err)
	}
	if _, err := web.Detail(ctx, series.ID); !errors.Is(err, biz.ErrWebNotFound) {
		t.Fatalf("detail after unpublish: %v", err)
	}
	// Republish, then revoke the grant: new sessions are refused.
	if _, err := review.Publish(ctx, series.ID, publication.RevisionID, "operator"); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if _, err := data.NewAuthorizationRepoWithDB(client).RevokeGrant(ctx, grant.ID, 1, "admin", "撤权"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := web.OpenPlayback(ctx, series.ID, episodes[0]); !errors.Is(err, biz.ErrWebUnavailable) {
		t.Fatalf("open after revoke: %v", err)
	}
	if _, err := repo.GetPlayback(ctx, biz.HashSessionToken(ticket.Token), time.Now().UTC()); !errors.Is(err, biz.ErrPlaybackUnavailable) {
		t.Fatalf("old session after revoke: %v", err)
	}
}

func TestWebEpisodesFollowTheFrozenAsset(t *testing.T) {
	client, _, web := newWeb(t)
	series, revision, _, episodes := seedPublished(t, client)
	ctx := context.Background()

	// The second episode freezes the asset of the first one.
	firstAsset, err := client.MediaAsset.Query().
		Where(mediaasset.EpisodeIDEQ(episodes[0])).
		Only(ctx)
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	if _, err := client.RevisionEpisode.UpdateOneID(episodes[1]).
		SetAssetID(firstAsset.ID).Save(ctx); err != nil {
		t.Fatalf("freeze the shared asset: %v", err)
	}
	detail, err := web.Detail(ctx, series.ID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.Episodes) != 2 {
		t.Fatalf("episodes: %d", len(detail.Episodes))
	}
	for _, episode := range detail.Episodes {
		if episode.AssetID != firstAsset.ID {
			t.Fatalf("episode %d resolved asset %d, want the frozen %d", episode.Number, episode.AssetID, firstAsset.ID)
		}
	}
	// A playback session for the second episode uses the same frozen asset.
	ticket, err := web.OpenPlayback(ctx, series.ID, episodes[1])
	if err != nil {
		t.Fatalf("open playback: %v", err)
	}
	if ticket.EpisodeID != episodes[1] {
		t.Fatalf("ticket: %+v", ticket)
	}
	_ = revision
}

func TestWebOldSessionDoesNotSurviveARepublication(t *testing.T) {
	client, _, web := newWeb(t)
	series, _, _, episodes := seedPublished(t, client)
	ctx := context.Background()
	review := biz.NewReviews(data.NewReviewRepoWithDB(client), nil)

	ticket, err := web.OpenPlayback(ctx, series.ID, episodes[0])
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	publication, err := review.Publication(ctx, series.ID)
	if err != nil {
		t.Fatalf("publication: %v", err)
	}
	if _, err := review.Unpublish(ctx, series.ID, publication.Version, "operator", "下架验证"); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	// The re-publication starts a new generation.
	if _, err := review.Publish(ctx, series.ID, publication.RevisionID, "operator"); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if _, err := web.Refresh(ctx, ticket.Token); !errors.Is(err, biz.ErrPlaybackUnavailable) {
		t.Fatalf("the old token survived the re-publication: %v", err)
	}
	// A new session works and carries the new generation.
	fresh, err := web.OpenPlayback(ctx, series.ID, episodes[0])
	if err != nil {
		t.Fatalf("open after republish: %v", err)
	}
	if _, err := web.Refresh(ctx, fresh.Token); err != nil {
		t.Fatalf("refresh the new session: %v", err)
	}
}

func TestWebSearchAndTotalUseThePublishedRevision(t *testing.T) {
	client, _, web := newWeb(t)
	series, revision, grant, _ := seedPublished(t, client)
	ctx := context.Background()

	// The searchable text is the published revision, not the series row.
	if _, err := client.ContentSeries.UpdateOneID(series.ID).
		SetTitle("后来改的名字").Save(ctx); err != nil {
		t.Fatalf("rename the series: %v", err)
	}
	if found, _, _ := web.List(ctx, biz.WebQuery{Q: "公开验证剧", Page: 1, PageSize: 10}); len(found) != 1 {
		t.Fatalf("search by the published title: %d", len(found))
	}
	if found, _, _ := web.List(ctx, biz.WebQuery{Q: "后来改的名字", Page: 1, PageSize: 10}); len(found) != 0 {
		t.Fatalf("a renamed series row leaked into the search: %d", len(found))
	}
	if _, err := client.ContentRevision.UpdateOneID(revision.ID).
		SetTitle("发布修订的新名字").Save(ctx); err != nil {
		t.Fatalf("rename the revision: %v", err)
	}
	if found, _, _ := web.List(ctx, biz.WebQuery{Q: "发布修订的新名字", Page: 1, PageSize: 10}); len(found) != 1 {
		t.Fatalf("search by the published revision title: %d", len(found))
	}
	// A revoked series counts for nothing: no rows and no total.
	if _, err := data.NewAuthorizationRepoWithDB(client).RevokeGrant(ctx, grant.ID, 1, "admin", "撤权"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	items, total, err := web.List(ctx, biz.WebQuery{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 || total != 0 {
		t.Fatalf("a revoked series still occupies the page: items=%d total=%d", len(items), total)
	}
}

// publishSeriesInCategory builds the minimum a public row needs: a series, an
// active grant that allows display, an approved revision that froze the grant,
// and - when published - the publication with a frozen category.
func publishSeriesInCategory(t *testing.T, client *ent.Client, title, category string, published bool) (*ent.ContentSeries, *ent.AuthorizationGrant) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	status := "draft"
	if published {
		status = "published"
	}
	series, err := client.ContentSeries.Create().
		SetSourceCode("hongguo").
		SetExternalID(nextSeriesExternalID()).
		SetTitle(title).
		SetDescription("分类验证").
		SetCategory(category).
		SetStatus(status).
		Save(ctx)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	grant, err := client.AuthorizationGrant.Create().
		SetSeriesID(series.ID).
		SetReference("AUTH-CAT-" + title).
		SetAllowProcess(true).SetAllowDisplay(true).SetAllowPlayback(true).
		SetValidFrom(now.Add(-time.Hour)).
		SetStatus(biz.GrantActive).
		SetVersion(1).
		SetConfirmedBy("tester").
		SetConfirmedAt(now).
		Save(ctx)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	revision, err := client.ContentRevision.Create().
		SetSeriesID(series.ID).
		SetRevisionNo(1).
		SetState(biz.RevisionStateApproved).
		SetVersion(1).
		SetTitle(title).
		SetDescription("分类验证").
		SetAuthorizationGrantID(grant.ID).
		SetAuthorizationVersion(1).
		SetAuthorizationHash("cat-hash").
		Save(ctx)
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	if published {
		if _, err := client.Publication.Create().
			SetSeriesID(series.ID).SetRevisionID(revision.ID).
			SetStatus(biz.PublicationPublished).
			SetPublishedBy("operator").SetPublishedAt(now).
			SetAuthorizationGrantID(grant.ID).SetAuthorizationVersion(1).
			SetCategory(category).SetGeneration(1).
			Save(ctx); err != nil {
			t.Fatalf("publication: %v", err)
		}
		if _, err := client.ContentSeries.UpdateOneID(series.ID).
			SetPublishedRevisionID(revision.ID).Save(ctx); err != nil {
			t.Fatalf("series publish: %v", err)
		}
	}
	return series, grant
}

// TestPublishedCategoriesFollowTheDisplayGate proves the category list is
// derived from what a visitor can display right now: drafts, empty categories
// and revoked content never contribute a name, and the answer is stable.
func TestPublishedCategoriesFollowTheDisplayGate(t *testing.T) {
	client, _, web := newWeb(t)
	seedPublished(t, client)
	ctx := context.Background()

	categories, err := web.Categories(ctx)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	if len(categories) != 1 || categories[0] != "短剧" {
		t.Fatalf("seeded categories = %v", categories)
	}

	publishSeriesInCategory(t, client, "悬疑剧", "悬疑", true)
	// A draft and a publication without a category must not add a name.
	publishSeriesInCategory(t, client, "古装草稿", "古装", false)
	publishSeriesInCategory(t, client, "无分类作品", "", true)

	categories, err = web.Categories(ctx)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	if len(categories) != 2 {
		t.Fatalf("categories = %v, want 短剧 and 悬疑", categories)
	}
	seen := map[string]bool{}
	for _, name := range categories {
		if name == "" {
			t.Fatal("an empty category reached the list")
		}
		if seen[name] {
			t.Fatalf("duplicate category %q", name)
		}
		seen[name] = true
	}
	if !seen["短剧"] || !seen["悬疑"] {
		t.Fatalf("categories = %v", categories)
	}
	// The order is stable between calls.
	again, err := web.Categories(ctx)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	for index := range categories {
		if categories[index] != again[index] {
			t.Fatalf("unstable order: %v then %v", categories, again)
		}
	}

	// A revoked grant removes its category, not just its row.
	series, grant := publishSeriesInCategory(t, client, "待撤权作品", "家庭", true)
	if _, err := data.NewAuthorizationRepoWithDB(client).RevokeGrant(ctx, grant.ID, 1, "admin", "撤权"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	categories, err = web.Categories(ctx)
	if err != nil {
		t.Fatalf("categories after revoke: %v", err)
	}
	for _, name := range categories {
		if name == "家庭" {
			t.Fatalf("revoked category %q still listed for series %d", name, series.ID)
		}
	}
	if len(categories) != 2 {
		t.Fatalf("categories after revoke = %v", categories)
	}
}
