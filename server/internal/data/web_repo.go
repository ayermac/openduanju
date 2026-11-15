package data

import (
	"context"
	"fmt"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/authorizationgrant"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/playbacksession"
	"openduanju-server/internal/data/ent/publication"
	"openduanju-server/internal/data/ent/revisionepisode"
)

// WebRepo reads the published projection and persists playback sessions.
type WebRepo struct {
	client *ent.Client
}

// NewWebRepo builds the public read repository.
func NewWebRepo(d *Data) *WebRepo {
	return &WebRepo{client: d.Ent()}
}

// NewWebRepoWithDB builds the repository over an explicit client.
func NewWebRepoWithDB(client *ent.Client) *WebRepo {
	return &WebRepo{client: client}
}

// ListPublished returns the published series, newest first.
//
// The filter runs before the pagination: the display right, the search and
// the category are part of the query, so the total and the page describe the
// same set. A row that fails the second check is skipped, but a database
// error is reported instead of being swallowed.
func (r *WebRepo) ListPublished(ctx context.Context, query biz.WebQuery) ([]biz.WebSeries, int, error) {
	now := time.Now().UTC()
	revisionIDs, err := r.displayableRevisions(ctx, query, now)
	if err != nil {
		return nil, 0, err
	}
	if len(revisionIDs) == 0 {
		return nil, 0, nil
	}
	q := r.client.Publication.Query().
		Where(
			publication.StatusEQ(biz.PublicationPublished),
			publication.RevisionIDIn(revisionIDs...),
		)
	if query.Category != "" {
		q = q.Where(publication.CategoryEQ(query.Category))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(publication.FieldPublishedAt)).
		Offset((query.Page - 1) * query.PageSize).
		Limit(query.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.WebSeries, 0, len(rows))
	for _, row := range rows {
		item, visible, err := r.publicRow(ctx, row, now)
		if err != nil {
			return nil, 0, err
		}
		if !visible {
			continue
		}
		out = append(out, *item)
	}
	return out, total, nil
}

// ListPublishedCategories returns the categories of the series a visitor can
// display right now, sorted by name and without duplicates.
//
// The set comes from the published projection of the revisions whose frozen
// grant still allows display, and each category is then confirmed by reading
// one of its rows through the same gate as the list. A category whose only
// content lost its right disappears with it; a category that exists on a
// candidate or a draft is never read at all.
func (r *WebRepo) ListPublishedCategories(ctx context.Context) ([]string, error) {
	now := time.Now().UTC()
	revisionIDs, err := r.displayableRevisions(ctx, biz.WebQuery{}, now)
	if err != nil {
		return nil, err
	}
	if len(revisionIDs) == 0 {
		return nil, nil
	}
	var names []string
	if err := r.client.Publication.Query().
		Where(
			publication.StatusEQ(biz.PublicationPublished),
			publication.RevisionIDIn(revisionIDs...),
			publication.CategoryNEQ(""),
		).
		Order(ent.Asc(publication.FieldCategory)).
		Select(publication.FieldCategory).
		Scan(ctx, &names); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		// One representative row decides whether the category is still
		// displayable. The cost is bounded by the number of categories, not by
		// the number of series.
		row, err := r.client.Publication.Query().
			Where(
				publication.StatusEQ(biz.PublicationPublished),
				publication.RevisionIDIn(revisionIDs...),
				publication.CategoryEQ(name),
			).
			Order(ent.Desc(publication.FieldPublishedAt)).
			First(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		_, visible, err := r.publicRow(ctx, row, now)
		if err != nil {
			return nil, err
		}
		if visible {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

// GetPublished returns one published series with its playable episodes.
func (r *WebRepo) GetPublished(ctx context.Context, seriesID int64) (*biz.WebSeriesDetail, error) {
	row, err := r.client.Publication.Query().
		Where(publication.SeriesIDEQ(seriesID), publication.StatusEQ(biz.PublicationPublished)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrWebNotFound
		}
		return nil, err
	}
	now := time.Now().UTC()
	base, visible, err := r.publicRow(ctx, row, now)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, biz.ErrWebUnavailable
	}
	episodes, err := r.episodes(ctx, row.RevisionID)
	if err != nil {
		return nil, err
	}
	return &biz.WebSeriesDetail{
		WebSeries:     *base,
		RevisionID:    row.RevisionID,
		PublicationID: row.ID,
		Episodes:      episodes,
	}, nil
}

// CreatePlayback freezes one episode and opens an anonymous session. The
// material comes from the asset the revision froze for that episode, never
// from "the newest asset of this episode".
func (r *WebRepo) CreatePlayback(ctx context.Context, seriesID int64, episodeID int64, tokenHash string, now time.Time) (*biz.PlaybackRecord, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	pub, err := tx.Publication.Query().
		Where(publication.SeriesIDEQ(seriesID), publication.StatusEQ(biz.PublicationPublished)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrWebNotFound
		}
		return nil, err
	}
	grant, err := playableGrantTx(ctx, tx, pub.RevisionID, now)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	episode, err := tx.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(pub.RevisionID), revisionepisode.IDEQ(episodeID), revisionepisode.SelectedEQ(true)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrPlaybackEpisode
		}
		return nil, err
	}
	asset, err := frozenAsset(ctx, tx, pub.RevisionID, episode)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// The session never outlives the authorization: an approved grant that
	// ends in an hour cannot produce a two hour ticket.
	expiresAt := now.Add(2 * time.Hour)
	if grant.ValidUntil != nil && grant.ValidUntil.Before(expiresAt) {
		expiresAt = grant.ValidUntil.UTC()
	}
	created, err := tx.PlaybackSession.Create().
		SetTokenHash(tokenHash).
		SetPublicationID(pub.ID).
		SetPublicationGeneration(pub.Generation).
		SetSeriesID(seriesID).
		SetRevisionID(pub.RevisionID).
		SetEpisodeID(episode.ID).
		SetAssetID(asset.ID).
		SetObjectProvider(asset.Provider).
		SetObjectBucket(asset.Bucket).
		SetObjectRegion(asset.Region).
		SetObjectKey(asset.ObjectKey).
		SetObjectVersion(asset.ObjectVersion).
		SetExpiresAt(expiresAt).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &biz.PlaybackRecord{
		ID:            created.ID,
		PublicationID: created.PublicationID,
		Generation:    created.PublicationGeneration,
		SeriesID:      created.SeriesID,
		RevisionID:    created.RevisionID,
		EpisodeID:     created.EpisodeID,
		AssetID:       created.AssetID,
		Object: biz.ObjectRef{
			Provider: created.ObjectProvider, Bucket: created.ObjectBucket, Region: created.ObjectRegion,
			Key: created.ObjectKey, VersionID: created.ObjectVersion,
		},
		ExpiresAt: created.ExpiresAt.UTC(),
	}, nil
}

// GetPlayback re-checks the session before every refresh. A revocation, an
// expiry, an unpublish or a newer publication generation all stop it: an old
// token never comes back to life.
func (r *WebRepo) GetPlayback(ctx context.Context, tokenHash string, now time.Time) (*biz.PlaybackRecord, error) {
	row, err := r.client.PlaybackSession.Query().
		Where(playbacksession.TokenHashEQ(tokenHash)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrPlaybackNotFound
		}
		return nil, err
	}
	if !now.Before(row.ExpiresAt.UTC()) {
		return nil, biz.ErrPlaybackUnavailable
	}
	pub, err := r.client.Publication.Query().
		Where(publication.IDEQ(row.PublicationID), publication.StatusEQ(biz.PublicationPublished)).
		Only(ctx)
	if err != nil {
		return nil, biz.ErrPlaybackUnavailable
	}
	if pub.RevisionID != row.RevisionID || pub.Generation != row.PublicationGeneration {
		return nil, biz.ErrPlaybackUnavailable
	}
	if visible, err := r.ensureDisplay(ctx, pub.RevisionID, now); err != nil {
		return nil, err
	} else if !visible {
		return nil, biz.ErrPlaybackUnavailable
	}
	if _, err := playableGrant(ctx, r.client, pub.RevisionID, now); err != nil {
		return nil, biz.ErrPlaybackUnavailable
	}
	return &biz.PlaybackRecord{
		ID: row.ID, PublicationID: row.PublicationID, Generation: row.PublicationGeneration,
		SeriesID: row.SeriesID, RevisionID: row.RevisionID, EpisodeID: row.EpisodeID, AssetID: row.AssetID,
		Object: biz.ObjectRef{
			Provider: row.ObjectProvider, Bucket: row.ObjectBucket, Region: row.ObjectRegion,
			Key: row.ObjectKey, VersionID: row.ObjectVersion,
		},
		ExpiresAt: row.ExpiresAt.UTC(),
	}, nil
}

// displayableRevisions returns the published revisions whose frozen
// authorization allows display and that match the search text.
func (r *WebRepo) displayableRevisions(ctx context.Context, query biz.WebQuery, now time.Time) ([]int64, error) {
	grantIDs, err := r.client.AuthorizationGrant.Query().
		Where(
			authorizationgrant.StatusEQ(biz.GrantActive),
			authorizationgrant.AllowDisplayEQ(true),
			authorizationgrant.ValidFromLTE(now),
			authorizationgrant.Or(
				authorizationgrant.ValidUntilIsNil(),
				authorizationgrant.ValidUntilGT(now),
			),
		).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(grantIDs) == 0 {
		return nil, nil
	}
	revisions := r.client.ContentRevision.Query().
		Where(contentrevision.AuthorizationGrantIDIn(grantIDs...))
	if query.Q != "" {
		// The search reads the published revision, not the series table: a
		// later rename of the series must not change what is searchable.
		revisions = revisions.Where(contentrevision.Or(
			contentrevision.TitleContains(query.Q),
			contentrevision.DescriptionContains(query.Q),
		))
	}
	return revisions.IDs(ctx)
}

// publicRow builds one public list row.
func (r *WebRepo) publicRow(ctx context.Context, row *ent.Publication, now time.Time) (*biz.WebSeries, bool, error) {
	revision, err := r.client.ContentRevision.Get(ctx, row.RevisionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	visible, err := r.ensureDisplay(ctx, row.RevisionID, now)
	if err != nil {
		return nil, false, err
	}
	if !visible {
		return nil, false, nil
	}
	item := &biz.WebSeries{
		SeriesID:    row.SeriesID,
		Title:       revision.Title,
		Description: revision.Description,
		PublishedAt: row.PublishedAt.UTC(),
		Category:    row.Category,
	}
	if revision.CoverAssetID > 0 {
		cover, err := r.client.MediaAsset.Get(ctx, revision.CoverAssetID)
		if err != nil {
			if !ent.IsNotFound(err) {
				return nil, false, err
			}
		} else {
			item.CoverURL = cover.Provider + "|" + cover.Bucket + "|" + cover.Region + "|" + cover.ObjectKey + "|" + cover.ObjectVersion
		}
	}
	count, err := r.client.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(row.RevisionID), revisionepisode.SelectedEQ(true)).
		Count(ctx)
	if err != nil {
		return nil, false, err
	}
	item.EpisodeCount = count
	return item, true, nil
}

// episodes lists the selected episodes of the published revision. The
// material is the asset the revision froze for that episode.
func (r *WebRepo) episodes(ctx context.Context, revisionID int64) ([]biz.WebEpisode, error) {
	rows, err := r.client.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID), revisionepisode.SelectedEQ(true)).
		Order(ent.Asc(revisionepisode.FieldEpisodeNo)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.WebEpisode, 0, len(rows))
	for _, row := range rows {
		episode := biz.WebEpisode{EpisodeID: row.ID, Number: int(row.EpisodeNo), ExternalID: textValue(row.ExternalID)}
		if row.AssetID <= 0 {
			out = append(out, episode)
			continue
		}
		asset, err := r.client.MediaAsset.Get(ctx, row.AssetID)
		if err != nil {
			if ent.IsNotFound(err) {
				out = append(out, episode)
				continue
			}
			return nil, err
		}
		if asset.State != "ready" || asset.ObjectKey == "" {
			// The frozen material is not usable. The asset may come from an
			// earlier revision: the frozen episode reference decides what
			// is played, not where the asset was first created.
			out = append(out, episode)
			continue
		}
		episode.AssetID = asset.ID
		episode.DurationMS = asset.DurationMs
		episode.Width = asset.Width
		episode.Height = asset.Height
		out = append(out, episode)
	}
	return out, nil
}

// ensureDisplay applies the frozen grant's display right at read time.
func (r *WebRepo) ensureDisplay(ctx context.Context, revisionID int64, now time.Time) (bool, error) {
	revision, err := r.client.ContentRevision.Get(ctx, revisionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if revision.AuthorizationGrantID == 0 {
		return false, nil
	}
	row, err := r.client.AuthorizationGrant.Get(ctx, revision.AuthorizationGrantID)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if err := biz.EnsureForDisplay(toAuthorizationGrant(row), revision.SeriesID, now); err != nil {
		return false, nil
	}
	return true, nil
}

// frozenAsset reads the material the revision froze for one episode.
func frozenAsset(ctx context.Context, tx *ent.Tx, revisionID int64, episode *ent.RevisionEpisode) (*ent.MediaAsset, error) {
	if episode.AssetID <= 0 {
		return nil, biz.ErrWebUnavailable
	}
	asset, err := tx.MediaAsset.Get(ctx, episode.AssetID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrWebUnavailable
		}
		return nil, err
	}
	// The episode freezes this asset. An asset created for an earlier
	// revision stays usable when a new revision references it.
	if asset.State != "ready" || asset.ObjectKey == "" {
		return nil, biz.ErrWebUnavailable
	}
	return asset, nil
}

// playableGrant reads the frozen grant and applies the playback right.
func playableGrant(ctx context.Context, client *ent.Client, revisionID int64, now time.Time) (*biz.AuthorizationGrant, error) {
	revision, err := client.ContentRevision.Get(ctx, revisionID)
	if err != nil {
		return nil, biz.ErrWebUnavailable
	}
	if revision.AuthorizationGrantID == 0 {
		return nil, biz.ErrWebUnavailable
	}
	row, err := client.AuthorizationGrant.Query().
		Where(authorizationgrant.IDEQ(revision.AuthorizationGrantID)).
		Only(ctx)
	if err != nil {
		return nil, biz.ErrWebUnavailable
	}
	grant := toAuthorizationGrant(row)
	if err := biz.EnsureForPlayback(grant, revision.SeriesID, now); err != nil {
		return nil, fmt.Errorf("%w: %v", biz.ErrWebUnavailable, err)
	}
	return grant, nil
}

// playableGrantTx is the transactional variant, so the grant cannot change
// between the check and the session insert.
func playableGrantTx(ctx context.Context, tx *ent.Tx, revisionID int64, now time.Time) (*biz.AuthorizationGrant, error) {
	revision, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(revisionID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		return nil, biz.ErrWebUnavailable
	}
	if revision.AuthorizationGrantID == 0 {
		return nil, biz.ErrWebUnavailable
	}
	row, err := tx.AuthorizationGrant.Query().
		Where(authorizationgrant.IDEQ(revision.AuthorizationGrantID)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		return nil, biz.ErrWebUnavailable
	}
	grant := toAuthorizationGrant(row)
	if err := biz.EnsureForPlayback(grant, revision.SeriesID, now); err != nil {
		return nil, fmt.Errorf("%w: %v", biz.ErrWebUnavailable, err)
	}
	return grant, nil
}

// compile-time guard.
var _ biz.WebRepo = (*WebRepo)(nil)
