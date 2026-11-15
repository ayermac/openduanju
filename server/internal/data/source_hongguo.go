package data

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"
)

const (
	hongguoSourceKey = "hongguo_web"
	defaultSourceUA  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var channelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// HongguoSourceConfig pins one web origin and its bounds.
type HongguoSourceConfig struct {
	Origin    string
	Timeout   time.Duration
	MaxBytes  int64
	UserAgent string
	// Transport overrides the HTTPS transport for tests only.
	Transport http.RoundTripper
}

// hongguoCanonicalHosts are the hosts the parser is allowed to read.
var hongguoCanonicalHosts = []string{"hongguoduanju.com", "www.hongguoduanju.com"}

// HongguoSource parses the server-rendered _ROUTER_DATA payload of the
// hongguoduanju.com rank and detail pages. It returns only metadata and real
// episode identities; playback URLs stay in the media resolver boundary.
type HongguoSource struct {
	origin    string
	maxBytes  int64
	userAgent string
	client    *http.Client
}

// NewHongguoSource validates the configured origin without any network call.
func NewHongguoSource(cfg HongguoSourceConfig) (*HongguoSource, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.Origin))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("source origin must be an absolute HTTPS URL without credentials"))
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("source origin must not contain a path"))
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 8 << 20
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultSourceUA
	}
	transport := cfg.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			IdleConnTimeout:     30 * time.Second,
			MaxIdleConns:        4,
			MaxIdleConnsPerHost: 2,
		}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many source redirects")
			}
			if req.URL.Scheme != "https" {
				return errUnsafeRedirect
			}
			return nil
		},
	}
	return &HongguoSource{
		origin:    strings.TrimRight(parsed.String(), "/"),
		maxBytes:  cfg.MaxBytes,
		userAgent: cfg.UserAgent,
		client:    client,
	}, nil
}

// NewWebMetadataAdapter builds the configured web metadata adapter for Wire.
func NewWebMetadataAdapter(c *conf.Source) (biz.WebMetadataAdapter, error) {
	hongguo := c.GetHongguo()
	if hongguo == nil {
		return nil, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("source.hongguo configuration is missing"))
	}
	timeout := 20 * time.Second
	if configured := hongguo.GetTimeout(); configured != nil && configured.AsDuration() > 0 {
		timeout = configured.AsDuration()
	}
	return NewHongguoSource(HongguoSourceConfig{
		Origin:    hongguo.GetOrigin(),
		Timeout:   timeout,
		MaxBytes:  hongguo.GetMaxBytes(),
		UserAgent: hongguo.GetUserAgent(),
	})
}

// Key identifies the adapter in source configuration.
func (s *HongguoSource) Key() string { return hongguoSourceKey }

// ListSeries reads one ranking channel page.
func (s *HongguoSource) ListSeries(ctx context.Context, req biz.ListSeriesRequest) (*biz.SeriesPage, error) {
	channel := strings.TrimSpace(req.Channel)
	if !channelPattern.MatchString(channel) {
		return nil, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, fmt.Errorf("channel %q is not allowed", req.Channel))
	}
	page := req.Page
	if page <= 0 {
		page = 1
	}
	if page > 1000 {
		return nil, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("page is out of range"))
	}
	origin, originErr := s.originFor(req.Origin)
	if originErr != nil {
		return nil, originErr
	}
	target := fmt.Sprintf("%s/rank/%s?page=%d", origin, channel, page)
	body, fetchedAt, err := s.fetch(ctx, target)
	if err != nil {
		return nil, err
	}
	loader, err := extractRouterData(body)
	if err != nil {
		return nil, err
	}
	rank, err := pickRankPage(loader)
	if err != nil {
		return nil, err
	}
	if rank.Content.Pagination.PageNum != 0 && rank.Content.Pagination.PageNum != page {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false,
			fmt.Errorf("source returned page %d for a request of page %d", rank.Content.Pagination.PageNum, page))
	}
	items, dropped := normalizeRankItems(rank.Content.RankList)
	if len(items) == 0 {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("ranking page contained no usable entries"))
	}
	return &biz.SeriesPage{
		Channel:    channel,
		Page:       page,
		TotalPages: rank.Content.Pagination.TotalPages,
		SourceURL:  target,
		FetchedAt:  fetchedAt,
		Items:      items,
		Dropped:    dropped,
	}, nil
}

// GetSeries reads the full detail page and its real episode identities.
func (s *HongguoSource) GetSeries(ctx context.Context, externalID string) (*biz.SeriesDetail, error) {
	seriesID := strings.TrimSpace(externalID)
	if !digitsPattern.MatchString(seriesID) {
		return nil, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("series id must be numeric"))
	}
	target := s.origin + "/detail?series_id=" + seriesID
	body, fetchedAt, err := s.fetch(ctx, target)
	if err != nil {
		return nil, err
	}
	loader, err := extractRouterData(body)
	if err != nil {
		return nil, err
	}
	raw, ok := loader["detail_page"]
	if !ok {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("detail page payload is missing"))
	}
	var page detailPagePayload
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, fmt.Errorf("decode detail payload: %w", err))
	}
	detail := page.SeriesDetail
	if detail.SeriesID != seriesID {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false,
			fmt.Errorf("detail page returned series %q instead of %q", detail.SeriesID, seriesID))
	}
	title := strings.TrimSpace(detail.SeriesName)
	if title == "" {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("detail page has no title"))
	}
	episodes := make([]biz.EpisodeRef, 0, len(detail.VidList))
	seen := make(map[string]struct{}, len(detail.VidList))
	for index, rawID := range detail.VidList {
		id := strings.TrimSpace(rawID)
		if !digitsPattern.MatchString(id) {
			return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, fmt.Errorf("episode %d has an invalid id", index+1))
		}
		if _, exists := seen[id]; exists {
			return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, fmt.Errorf("episode id %s is duplicated", id))
		}
		seen[id] = struct{}{}
		episodes = append(episodes, biz.EpisodeRef{ExternalID: id, Number: index + 1})
	}
	if len(episodes) == 0 {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("detail page has no episodes"))
	}
	return &biz.SeriesDetail{
		ExternalID:      detail.SeriesID,
		Title:           title,
		CoverURL:        httpsOrEmpty(detail.SeriesCover),
		Description:     strings.TrimSpace(detail.SeriesIntro),
		Tags:            cleanStrings(detail.Tags),
		EpisodeCount:    detail.EpisodeCnt,
		AccessibleCount: detail.AccessibleEpisodeCnt,
		SourceURL:       target,
		FetchedAt:       fetchedAt,
		Episodes:        episodes,
	}, nil
}

func (s *HongguoSource) fetch(ctx context.Context, target string) ([]byte, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeConfigInvalid, false, err)
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, errUnsafeRedirect) {
			return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeRejected, false, errUnsafeRedirect)
		}
		if ctx.Err() != nil {
			return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeUnavailable, true, ctx.Err())
		}
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeUnavailable, true, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeRejected, false, fmt.Errorf("source returned status %d", resp.StatusCode))
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeNotFound, false, fmt.Errorf("source returned status %d", resp.StatusCode))
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeRateLimited, true, errors.New("source rate limited the request"))
	case resp.StatusCode >= 500:
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeUnavailable, true, fmt.Errorf("source returned status %d", resp.StatusCode))
	default:
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeRejected, false, fmt.Errorf("source returned status %d", resp.StatusCode))
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeRejected, false, fmt.Errorf("unexpected content type %q", contentType))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.maxBytes+1))
	if err != nil {
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeUnavailable, true, err)
	}
	if int64(len(body)) > s.maxBytes {
		return nil, time.Time{}, biz.NewSourceError(biz.SourceCodeBudget, false, fmt.Errorf("source page exceeds %d bytes", s.maxBytes))
	}
	return body, time.Now().UTC(), nil
}

// extractRouterData reads the first complete JSON object assigned to
// _ROUTER_DATA. The decoder stops at the end of that value instead of
// requiring the surrounding HTML to be valid JSON.
func extractRouterData(body []byte) (map[string]json.RawMessage, error) {
	// The route manifest may also mention _ROUTER_DATA; only the assignment is
	// the payload, so scan forward until an '=' is followed by a JSON object.
	var rest []byte
	for offset := 0; ; {
		marker := bytes.Index(body[offset:], []byte("_ROUTER_DATA"))
		if marker < 0 {
			return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("page has no _ROUTER_DATA payload"))
		}
		candidate := body[offset+marker:]
		equals := bytes.IndexByte(candidate, '=')
		if equals < 0 {
			return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("_ROUTER_DATA has no assignment"))
		}
		value := bytes.TrimLeft(candidate[equals+1:], " \t\r\n")
		if len(value) > 0 && value[0] == '{' {
			rest = value
			break
		}
		offset += marker + len("_ROUTER_DATA")
		if offset >= len(body) {
			return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("_ROUTER_DATA is not a JSON object"))
		}
	}
	var root struct {
		LoaderData map[string]json.RawMessage `json:"loaderData"`
	}
	decoder := json.NewDecoder(bytes.NewReader(rest))
	if err := decoder.Decode(&root); err != nil {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, fmt.Errorf("decode _ROUTER_DATA: %w", err))
	}
	if len(root.LoaderData) == 0 {
		return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("_ROUTER_DATA has no loaderData"))
	}
	return root.LoaderData, nil
}

type rankItem struct {
	ID               string   `json:"id"`
	SeriesID         string   `json:"seriesId"`
	Rank             int      `json:"rank"`
	Title            string   `json:"title"`
	Cover            string   `json:"cover"`
	Tags             []string `json:"tags"`
	Description      string   `json:"description"`
	EpisodeCount     int      `json:"episodeCount"`
	OpenEpisodeCount int      `json:"openEpisodeCount"`
	HeatText         string   `json:"heatText"`
	ScoreText        string   `json:"scoreText"`
	FavoriteText     string   `json:"favoriteText"`
	LikeText         string   `json:"likeText"`
	ShareText        string   `json:"shareText"`
}

type rankPagePayload struct {
	Content struct {
		IsSuccess  bool       `json:"isSuccess"`
		RankList   []rankItem `json:"rankList"`
		Pagination struct {
			PageNum    int    `json:"pageNum"`
			TotalPages int    `json:"totalPages"`
			NextURL    string `json:"nextUrl"`
		} `json:"pagination"`
	} `json:"content"`
}

func pickRankPage(loader map[string]json.RawMessage) (*rankPagePayload, error) {
	for _, raw := range loader {
		var page rankPagePayload
		if err := json.Unmarshal(raw, &page); err != nil {
			continue
		}
		if page.Content.IsSuccess && page.Content.Pagination.TotalPages > 0 {
			return &page, nil
		}
	}
	return nil, biz.NewSourceError(biz.SourceCodeParseFailed, false, errors.New("ranking payload is missing or unsuccessful"))
}

func normalizeRankItems(items []rankItem) ([]biz.SeriesSummary, int) {
	out := make([]biz.SeriesSummary, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	dropped := 0
	for _, item := range items {
		id := strings.TrimSpace(item.SeriesID)
		title := strings.TrimSpace(item.Title)
		if !digitsPattern.MatchString(id) || title == "" {
			dropped++
			continue
		}
		if _, exists := seen[id]; exists {
			dropped++
			continue
		}
		seen[id] = struct{}{}
		metrics := map[string]string{}
		for key, value := range map[string]string{
			"heat":     item.HeatText,
			"score":    item.ScoreText,
			"favorite": item.FavoriteText,
			"like":     item.LikeText,
			"share":    item.ShareText,
		} {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				metrics[key] = trimmed
			}
		}
		out = append(out, biz.SeriesSummary{
			ExternalID:       id,
			Title:            title,
			CoverURL:         httpsOrEmpty(item.Cover),
			Description:      strings.TrimSpace(item.Description),
			Rank:             item.Rank,
			Tags:             cleanStrings(item.Tags),
			EpisodeCount:     item.EpisodeCount,
			OpenEpisodeCount: item.OpenEpisodeCount,
			Metrics:          metrics,
		})
	}
	return out, dropped
}

type detailPagePayload struct {
	SeriesDetail struct {
		SeriesID             string   `json:"series_id"`
		SeriesName           string   `json:"series_name"`
		SeriesCover          string   `json:"series_cover"`
		SeriesIntro          string   `json:"series_intro"`
		Tags                 []string `json:"tags"`
		EpisodeCnt           int      `json:"episode_cnt"`
		AccessibleEpisodeCnt int      `json:"accessible_episode_cnt"`
		VidList              []string `json:"vid_list"`
	} `json:"seriesDetail"`
}

var digitsPattern = regexp.MustCompile(`^\d{1,32}$`)

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func httpsOrEmpty(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

// Entries declares the one entry the deployed adapter really reads. A channel
// may choose only from these, so the console cannot save an entry that never
// runs.
func (s *HongguoSource) Entries() []biz.SourceEntry {
	hosts := make([]string, 0, len(hongguoCanonicalHosts)+1)
	hosts = append(hosts, hongguoCanonicalHosts...)
	if parsed, err := url.Parse(s.origin); err == nil {
		if host := strings.ToLower(parsed.Host); host != "" && !containsHost(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	return []biz.SourceEntry{{
		SourceCode:   "hongguo",
		AdapterKey:   hongguoSourceKey,
		Kind:         "rank",
		Channel:      "hot-drama",
		Label:        "红果热播榜",
		Description:  "红果短剧热播榜；只读服务端渲染页面里的元数据与分集身份，不取播放地址",
		BaseURL:      s.origin,
		AllowedHosts: hosts,
	}}
}

// originFor resolves the origin of one request. An empty value keeps the
// adapter's own origin; an explicit one must stay on a declared host.
func (s *HongguoSource) originFor(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return s.origin, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("channel origin must be an absolute HTTPS URL without credentials"))
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", biz.NewSourceError(biz.SourceCodeConfigInvalid, false, errors.New("channel origin must not carry a path, query or fragment"))
	}
	if !containsHost(hongguoCanonicalHosts, strings.ToLower(parsed.Host)) && !strings.EqualFold(parsed.Host, hostOf(s.origin)) {
		return "", biz.NewSourceError(biz.SourceCodeConfigInvalid, false, fmt.Errorf("channel origin %q is not a declared host", parsed.Host))
	}
	return strings.TrimSuffix(strings.TrimSpace(raw), "/"), nil
}

func hostOf(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

func containsHost(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
