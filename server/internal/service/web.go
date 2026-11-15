package service

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	v1 "openduanju-server/api/web/v1"
	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// WebService is the public, read-only surface. No session is required and no
// back-office data is exposed.
type WebService struct {
	v1.UnimplementedWebServiceServer
	uc *biz.Web
}

// PlaybackTicketTTL reads the configured credential lifetime. The
// environment wins over the file, so a deployment can shorten the lifetime
// without editing the configuration.
func PlaybackTicketTTL(c *conf.Media) time.Duration {
	raw := strings.TrimSpace(os.Getenv("MEDIA_PLAYBACK_TICKET_TTL"))
	if raw == "" {
		raw = strings.TrimSpace(c.GetPlaybackTicketTtl())
	}
	if raw == "" {
		return 10 * time.Minute
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 10 * time.Minute
	}
	return value
}

// NewWebService builds the public service.
func NewWebService(uc *biz.Web) *WebService {
	return &WebService{uc: uc}
}

// Home returns the most recently published series.
func (s *WebService) Home(ctx context.Context, req *v1.HomeRequest) (*v1.SeriesSet, error) {
	limit := int(req.GetLimit())
	if limit <= 0 || limit > 30 {
		limit = 12
	}
	items, total, err := s.uc.List(ctx, biz.WebQuery{Page: 1, PageSize: limit})
	if err != nil {
		return nil, toWebError(err)
	}
	return toSeriesSet(items, total, 1, limit), nil
}

// ListSeries filters the published list.
func (s *WebService) ListSeries(ctx context.Context, req *v1.ListSeriesRequest) (*v1.SeriesSet, error) {
	items, total, err := s.uc.List(ctx, biz.WebQuery{
		Q: req.GetQ(), Category: req.GetCategory(),
		Page: int(req.GetPage()), PageSize: int(req.GetPageSize()),
	})
	if err != nil {
		return nil, toWebError(err)
	}
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	return toSeriesSet(items, total, page, size), nil
}

// ListCategories returns the categories a visitor can filter by. The list is
// derived from displayable published content, so it is never empty-but-stale.
func (s *WebService) ListCategories(ctx context.Context, _ *v1.ListCategoriesRequest) (*v1.CategorySet, error) {
	categories, err := s.uc.Categories(ctx)
	if err != nil {
		return nil, toWebError(err)
	}
	return &v1.CategorySet{Categories: categories}, nil
}

// GetSeries returns one published detail page.
func (s *WebService) GetSeries(ctx context.Context, req *v1.GetSeriesRequest) (*v1.SeriesDetail, error) {
	seriesID, err := strconv.ParseInt(req.GetId(), 10, 64)
	if err != nil {
		return nil, kratoserrors.BadRequest("INVALID_ID", "作品 ID 无效")
	}
	detail, err := s.uc.Detail(ctx, seriesID)
	if err != nil {
		return nil, toWebError(err)
	}
	out := &v1.SeriesDetail{
		Series:        toSeriesCard(&detail.WebSeries),
		RevisionId:    strconv.FormatInt(detail.RevisionID, 10),
		PublicationId: strconv.FormatInt(detail.PublicationID, 10),
	}
	for index := range detail.Episodes {
		episode := detail.Episodes[index]
		out.Episodes = append(out.Episodes, &v1.EpisodeCard{
			EpisodeId:  strconv.FormatInt(episode.EpisodeID, 10),
			Number:     int32(episode.Number),
			ExternalId: episode.ExternalID,
			AssetId:    strconv.FormatInt(episode.AssetID, 10),
			DurationMs: episode.DurationMS,
			Width:      int32(episode.Width),
			Height:     int32(episode.Height),
		})
	}
	return out, nil
}

// OpenPlayback opens one anonymous session for a single episode.
func (s *WebService) OpenPlayback(ctx context.Context, req *v1.OpenPlaybackRequest) (*v1.PlaybackTicket, error) {
	seriesID, err := strconv.ParseInt(req.GetSeriesId(), 10, 64)
	if err != nil {
		return nil, kratoserrors.BadRequest("INVALID_ID", "作品 ID 无效")
	}
	episodeID, err := strconv.ParseInt(req.GetEpisodeId(), 10, 64)
	if err != nil {
		return nil, kratoserrors.BadRequest("INVALID_ID", "分集 ID 无效")
	}
	ticket, err := s.uc.OpenPlayback(ctx, seriesID, episodeID)
	if err != nil {
		return nil, toWebError(err)
	}
	SetCreated(ctx)
	return toTicket(ticket), nil
}

// RefreshPlayback re-signs the same frozen episode.
func (s *WebService) RefreshPlayback(ctx context.Context, req *v1.RefreshPlaybackRequest) (*v1.PlaybackTicket, error) {
	ticket, err := s.uc.Refresh(ctx, req.GetToken())
	if err != nil {
		return nil, toWebError(err)
	}
	return toTicket(ticket), nil
}

// toSeriesCard maps one list row.
func toSeriesCard(item *biz.WebSeries) *v1.SeriesCard {
	if item == nil {
		return nil
	}
	return &v1.SeriesCard{
		SeriesId:     strconv.FormatInt(item.SeriesID, 10),
		Title:        item.Title,
		Description:  item.Description,
		CoverUrl:     item.CoverURL,
		EpisodeCount: int32(item.EpisodeCount),
		Category:     item.Category,
		PublishedAt:  formatMediaTime(item.PublishedAt),
	}
}

// toSeriesSet maps a page.
func toSeriesSet(items []biz.WebSeries, total int, page int, size int) *v1.SeriesSet {
	out := &v1.SeriesSet{Total: int32(total), Page: int32(page), PageSize: int32(size)}
	for index := range items {
		out.Items = append(out.Items, toSeriesCard(&items[index]))
	}
	return out
}

// toTicket maps a playback ticket.
func toTicket(ticket *biz.PlaybackTicket) *v1.PlaybackTicket {
	if ticket == nil {
		return nil
	}
	return &v1.PlaybackTicket{
		SessionId:  strconv.FormatInt(ticket.SessionID, 10),
		Token:      ticket.Token,
		Url:        ticket.URL,
		ExpiresAt:  formatMediaTime(ticket.ExpiresAt),
		EpisodeId:  strconv.FormatInt(ticket.EpisodeID, 10),
		RevisionId: strconv.FormatInt(ticket.RevisionID, 10),
	}
}

// toWebError maps public failures. An unavailable title answers 410 with the
// reason, so the page can explain why it disappeared.
func toWebError(err error) error {
	switch {
	case errors.Is(err, biz.ErrWebNotFound):
		return kratoserrors.NotFound("SERIES_NOT_PUBLISHED", "作品不存在或未发布")
	case errors.Is(err, biz.ErrWebUnavailable):
		return kratoserrors.New(http.StatusGone, "CONTENT_UNAVAILABLE", "作品当前不可用：授权失效、到期或已下架")
	case errors.Is(err, biz.ErrPlaybackEpisode):
		return kratoserrors.BadRequest("EPISODE_NOT_PUBLISHED", "该分集不在当前发布版本中")
	case errors.Is(err, biz.ErrPlaybackNotFound):
		return kratoserrors.NotFound("PLAYBACK_NOT_FOUND", "播放会话不存在")
	case errors.Is(err, biz.ErrPlaybackUnavailable):
		return kratoserrors.New(http.StatusGone, "PLAYBACK_UNAVAILABLE", "播放不可用：发布版本或授权已变化")
	}
	return kratoserrors.InternalServer("WEB_INTERNAL", "请求失败")
}
