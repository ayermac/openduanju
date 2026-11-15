// Command openduanju-source fetches and normalizes source web metadata.
// It is an operator/verification entrypoint; it does not persist candidates
// yet and never requests playback URLs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/data"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	"google.golang.org/protobuf/types/known/durationpb"
)

var (
	flagConf    string
	flagChannel string
	flagPage    int
	flagSeries  string
)

func init() {
	flag.StringVar(&flagConf, "conf", "../../configs", "config path")
	flag.StringVar(&flagChannel, "channel", "hot-drama", "ranking channel key")
	flag.IntVar(&flagPage, "page", 1, "ranking page")
	flag.StringVar(&flagSeries, "series", "", "fetch one series detail by external id")
}

type seriesOutput struct {
	ExternalID       string            `json:"external_id"`
	Title            string            `json:"title"`
	CoverURL         string            `json:"cover_url,omitempty"`
	Description      string            `json:"description,omitempty"`
	Rank             int               `json:"rank,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	EpisodeCount     int               `json:"episode_count"`
	OpenEpisodeCount int               `json:"open_episode_count,omitempty"`
	Metrics          map[string]string `json:"metrics,omitempty"`
}

type episodeOutput struct {
	ExternalID string `json:"external_id"`
	Number     int    `json:"number"`
}

type output struct {
	OK         bool            `json:"ok"`
	SourceURL  string          `json:"source_url,omitempty"`
	FetchedAt  string          `json:"fetched_at,omitempty"`
	Channel    string          `json:"channel,omitempty"`
	Page       int             `json:"page,omitempty"`
	TotalPages int             `json:"total_pages,omitempty"`
	Dropped    int             `json:"dropped,omitempty"`
	Items      []seriesOutput  `json:"items,omitempty"`
	Series     *seriesOutput   `json:"series,omitempty"`
	Episodes   []episodeOutput `json:"episodes,omitempty"`
	Error      string          `json:"error,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Retryable  bool            `json:"retryable,omitempty"`
}

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	c := config.New(config.WithSource(file.NewSource(flagConf), env.NewSource("KRATOS")))
	defer c.Close()
	if err := c.Load(); err != nil {
		return fail(err)
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		return fail(err)
	}
	cfg := bc.GetSource().GetHongguo()
	if cfg == nil {
		return fail(errors.New("source.hongguo configuration is missing"))
	}
	adapter, err := data.NewHongguoSource(data.HongguoSourceConfig{
		Origin:    cfg.GetOrigin(),
		Timeout:   durationOrDefault(cfg.GetTimeout(), 20*time.Second),
		MaxBytes:  cfg.GetMaxBytes(),
		UserAgent: cfg.GetUserAgent(),
	})
	if err != nil {
		return fail(err)
	}

	if flagSeries != "" {
		detail, err := adapter.GetSeries(ctx, flagSeries)
		if err != nil {
			return fail(err)
		}
		out := output{
			OK:        true,
			SourceURL: detail.SourceURL,
			FetchedAt: detail.FetchedAt.Format(time.RFC3339),
			Series: &seriesOutput{
				ExternalID:       detail.ExternalID,
				Title:            detail.Title,
				CoverURL:         detail.CoverURL,
				Description:      detail.Description,
				Tags:             detail.Tags,
				EpisodeCount:     detail.EpisodeCount,
				OpenEpisodeCount: detail.AccessibleCount,
			},
			Episodes: make([]episodeOutput, 0, len(detail.Episodes)),
		}
		for _, episode := range detail.Episodes {
			out.Episodes = append(out.Episodes, episodeOutput{ExternalID: episode.ExternalID, Number: episode.Number})
		}
		emit(out)
		logger.Info("source detail fetched", "series", detail.ExternalID, "episodes", len(detail.Episodes))
		return 0
	}

	page, err := adapter.ListSeries(ctx, biz.ListSeriesRequest{Channel: flagChannel, Page: flagPage})
	if err != nil {
		return fail(err)
	}
	out := output{
		OK:         true,
		SourceURL:  page.SourceURL,
		FetchedAt:  page.FetchedAt.Format(time.RFC3339),
		Channel:    page.Channel,
		Page:       page.Page,
		TotalPages: page.TotalPages,
		Dropped:    page.Dropped,
		Items:      make([]seriesOutput, 0, len(page.Items)),
	}
	for _, item := range page.Items {
		out.Items = append(out.Items, seriesOutput{
			ExternalID:       item.ExternalID,
			Title:            item.Title,
			CoverURL:         item.CoverURL,
			Description:      item.Description,
			Rank:             item.Rank,
			Tags:             item.Tags,
			EpisodeCount:     item.EpisodeCount,
			OpenEpisodeCount: item.OpenEpisodeCount,
			Metrics:          item.Metrics,
		})
	}
	emit(out)
	logger.Info("source ranking fetched", "channel", page.Channel, "page", page.Page, "items", len(page.Items))
	return 0
}

func emit(out output) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(out)
}

func fail(err error) int {
	code, _ := biz.SourceErrorCodeOf(err)
	emit(output{OK: false, Error: err.Error(), ErrorCode: string(code), Retryable: biz.SourceRetryable(err)})
	return 1
}

func durationOrDefault(value *durationpb.Duration, fallback time.Duration) time.Duration {
	if value == nil || value.AsDuration() <= 0 {
		return fallback
	}
	return value.AsDuration()
}
