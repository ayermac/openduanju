package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Public projection failures.
var (
	ErrWebNotFound         = errors.New("published content not found")
	ErrWebUnavailable      = errors.New("published content is currently unavailable")
	ErrPlaybackNotFound    = errors.New("playback session not found")
	ErrPlaybackUnavailable = errors.New("playback is no longer available")
	ErrPlaybackEpisode     = errors.New("the episode is not part of the published revision")
)

// WebEpisode is one playable episode of the published revision.
type WebEpisode struct {
	EpisodeID  int64
	Number     int
	ExternalID string
	AssetID    int64
	DurationMS int64
	Width      int
	Height     int
}

// WebSeries is one row of the public list.
type WebSeries struct {
	SeriesID     int64
	Title        string
	Description  string
	CoverURL     string
	EpisodeCount int
	PublishedAt  time.Time
	Category     string
}

// WebSeriesDetail is the public detail page.
type WebSeriesDetail struct {
	WebSeries
	RevisionID    int64
	PublicationID int64
	Episodes      []WebEpisode
}

// WebQuery filters the public list.
type WebQuery struct {
	Q        string
	Category string
	Page     int
	PageSize int
}

// PlaybackRecord is a stored playback session.
type PlaybackRecord struct {
	ID            int64
	PublicationID int64
	// Generation pins the session to one publication run.
	Generation int
	SeriesID   int64
	RevisionID int64
	EpisodeID  int64
	AssetID    int64
	Object     ObjectRef
	ExpiresAt  time.Time
}

// PlaybackTicket is what the player receives.
type PlaybackTicket struct {
	SessionID  int64
	Token      string
	URL        string
	ExpiresAt  time.Time
	EpisodeID  int64
	RevisionID int64
}

// WebRepo reads the published projection and persists playback sessions. The
// gates that depend on the current authorization run inside the repository so
// a cached page cannot skip them.
type WebRepo interface {
	ListPublished(ctx context.Context, query WebQuery) ([]WebSeries, int, error)
	// ListPublishedCategories returns the non-empty categories of the series a
	// visitor can actually display right now, sorted by name. It reads the
	// published projection and applies the same dynamic gate as the list, so a
	// category never points at content the visitor cannot see.
	ListPublishedCategories(ctx context.Context) ([]string, error)
	GetPublished(ctx context.Context, seriesID int64) (*WebSeriesDetail, error)
	CreatePlayback(ctx context.Context, seriesID int64, episodeID int64, tokenHash string, now time.Time) (*PlaybackRecord, error)
	GetPlayback(ctx context.Context, tokenHash string, now time.Time) (*PlaybackRecord, error)
}

// Web is the public read model.
type Web struct {
	repo    WebRepo
	storage ObjectStorage
	// ticketTTL is the lifetime of a public playback credential.
	ticketTTL time.Duration
}

// NewWeb builds the public use case.
func NewWeb(repo WebRepo, storage ObjectStorage, ticketTTL time.Duration) *Web {
	if ticketTTL <= 0 {
		ticketTTL = 10 * time.Minute
	}
	return &Web{repo: repo, storage: storage, ticketTTL: ticketTTL}
}

// List returns published series. An empty query lists the most recent ones.
func (w *Web) List(ctx context.Context, query WebQuery) ([]WebSeries, int, error) {
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 || query.PageSize > 50 {
		query.PageSize = 20
	}
	query.Q = strings.TrimSpace(query.Q)
	items, total, err := w.repo.ListPublished(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	for index := range items {
		items[index].CoverURL = w.coverURL(ctx, items[index].CoverURL)
	}
	return items, total, nil
}

// Categories lists the categories a visitor can filter by. The list is derived
// from the published projection, never from a candidate or a draft, and a
// category without a displayable series is left out.
func (w *Web) Categories(ctx context.Context) ([]string, error) {
	categories, err := w.repo.ListPublishedCategories(ctx)
	if err != nil {
		return nil, err
	}
	if categories == nil {
		categories = []string{}
	}
	return categories, nil
}

// coverURL turns the stored cover reference into a short-lived read URL. The
// list never shows an internal object path.
func (w *Web) coverURL(ctx context.Context, value string) string {
	if value == "" || w.storage == nil {
		return ""
	}
	ref, ok := parseObjectRef(value)
	if !ok {
		return ""
	}
	// The public surface uses the public read path: a visitor has no staff
	// session, so the cover must not depend on one.
	signed, err := w.storage.PlaybackURL(ctx, ref, 15*time.Minute)
	if err != nil {
		return ""
	}
	return signed.URL
}

// Detail returns one published series with its cover URL.
func (w *Web) Detail(ctx context.Context, seriesID int64) (*WebSeriesDetail, error) {
	detail, err := w.repo.GetPublished(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	detail.CoverURL = w.coverURL(ctx, detail.CoverURL)
	return detail, nil
}

// OpenPlayback creates one anonymous session for a single episode and returns
// a signed URL. The session freezes the revision, the episode and the object
// version.
func (w *Web) OpenPlayback(ctx context.Context, seriesID int64, episodeID int64) (*PlaybackTicket, error) {
	token, hash, err := NewSessionToken()
	if err != nil {
		return nil, err
	}
	record, err := w.repo.CreatePlayback(ctx, seriesID, episodeID, hash, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	url, expiresAt, err := w.signPlayback(ctx, record)
	if err != nil {
		return nil, err
	}
	return &PlaybackTicket{
		SessionID:  record.ID,
		Token:      token,
		URL:        url,
		ExpiresAt:  expiresAt,
		EpisodeID:  record.EpisodeID,
		RevisionID: record.RevisionID,
	}, nil
}

// Refresh re-signs the same frozen object. It never accepts a different
// episode: the caller only presents the session token.
func (w *Web) Refresh(ctx context.Context, token string) (*PlaybackTicket, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrPlaybackNotFound
	}
	record, err := w.repo.GetPlayback(ctx, HashSessionToken(token), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	url, expiresAt, err := w.signPlayback(ctx, record)
	if err != nil {
		return nil, err
	}
	return &PlaybackTicket{
		SessionID:  record.ID,
		Token:      token,
		URL:        url,
		ExpiresAt:  expiresAt,
		EpisodeID:  record.EpisodeID,
		RevisionID: record.RevisionID,
	}, nil
}

// signPlayback signs the frozen object with a lifetime that never outlives the
// session or the authorization.
func (w *Web) signPlayback(ctx context.Context, record *PlaybackRecord) (string, time.Time, error) {
	ttl := time.Until(record.ExpiresAt)
	if ttl <= 0 {
		return "", time.Time{}, ErrPlaybackUnavailable
	}
	// The credential never outlives the configured lifetime or the session.
	if ttl > w.ticketTTL {
		ttl = w.ticketTTL
	}
	signed, err := w.storage.PlaybackURL(ctx, record.Object, ttl)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %v", ErrWebUnavailable, err)
	}
	return signed.URL, signed.ExpiresAt, nil
}

// parseObjectRef decodes the pipe form used between the repository and the
// service for a cover object.
func parseObjectRef(value string) (ObjectRef, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 5 {
		return ObjectRef{}, false
	}
	return ObjectRef{
		Provider: parts[0], Bucket: parts[1], Region: parts[2], Key: parts[3], VersionID: parts[4],
	}, true
}
