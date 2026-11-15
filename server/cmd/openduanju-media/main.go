// Command openduanju-media runs one media episode through the processing and
// object-storage path. It is an operator/verification entrypoint, not an HTTP
// endpoint: long downloads and transcodes never run inside a request.
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
	flagConf      string
	flagInput     string
	flagEpisodeID string
	flagSource    string
	flagQuality   string
	flagProcess   bool
	flagKeep      string
	flagPrintURL  bool
)

func init() {
	flag.StringVar(&flagConf, "conf", "../../configs", "config path")
	flag.StringVar(&flagInput, "input", "", "already-authorized local input file")
	flag.StringVar(&flagEpisodeID, "episode-id", "", "real external episode id resolved through the SDK bridge")
	flag.StringVar(&flagSource, "source", "", "source code, e.g. hongguo")
	flag.StringVar(&flagQuality, "quality", "", "requested stream quality (empty uses resolver default)")
	flag.BoolVar(&flagProcess, "process-only", false, "stop after processing; do not upload")
	flag.StringVar(&flagKeep, "keep", "", "copy the processed file into this directory for local playback checks")
	flag.BoolVar(&flagPrintURL, "print-url", false, "include a short-lived preview URL in the JSON result")
}

type stageOutput struct {
	Stage     string `json:"stage"`
	Detail    string `json:"detail"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type assetOutput struct {
	State      string `json:"state"`
	Provider   string `json:"provider"`
	Bucket     string `json:"bucket"`
	Region     string `json:"region"`
	Key        string `json:"key"`
	VersionID  string `json:"version_id,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	VideoCodec string `json:"video_codec"`
	AudioCodec string `json:"audio_codec"`
	DurationMS int64  `json:"duration_ms"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Remuxed    bool   `json:"remuxed"`
	ProfileKey string `json:"profile_key"`
	SourceID   string `json:"source_id,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
}

type errorOutput struct {
	Code      string `json:"code"`
	Stage     string `json:"stage"`
	Retryable bool   `json:"retryable"`
}

type runOutput struct {
	OK               bool          `json:"ok"`
	Stages           []stageOutput `json:"stages"`
	Asset            *assetOutput  `json:"asset,omitempty"`
	ProcessedPath    string        `json:"processed_path,omitempty"`
	PreviewURL       string        `json:"preview_url,omitempty"`
	PreviewExpiresAt string        `json:"preview_expires_at,omitempty"`
	Error            *errorOutput  `json:"error,omitempty"`
}

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if flagProcess && flagPrintURL {
		emit(runOutput{OK: false, Error: &errorOutput{Code: string(biz.MediaCodeConfigInvalid), Stage: string(biz.MediaStageUpload)}})
		logger.Error("print-url is not available with process-only")
		return 2
	}

	c := config.New(config.WithSource(file.NewSource(flagConf), env.NewSource("KRATOS")))
	defer c.Close()
	if err := c.Load(); err != nil {
		emit(runOutput{OK: false, Error: &errorOutput{Code: string(biz.MediaCodeConfigInvalid), Stage: string(biz.MediaStageAcquire)}})
		logger.Error("load config", "err", err)
		return 2
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		emit(runOutput{OK: false, Error: &errorOutput{Code: string(biz.MediaCodeConfigInvalid), Stage: string(biz.MediaStageAcquire)}})
		logger.Error("scan config", "err", err)
		return 2
	}
	media := bc.GetMedia()
	if media == nil {
		emit(runOutput{OK: false, Error: &errorOutput{Code: string(biz.MediaCodeConfigInvalid), Stage: string(biz.MediaStageAcquire)}})
		logger.Error("media configuration is missing")
		return 2
	}
	if err := runPipeline(ctx, media, logger); err != nil {
		out := runOutput{OK: false}
		code, ok := biz.MediaErrorCodeOf(err)
		if !ok {
			code = biz.MediaCodeDownloadFailed
		}
		out.Error = &errorOutput{Code: string(code), Stage: string(stageOf(err)), Retryable: biz.Retryable(err)}
		emit(out)
		logger.Error("media run failed", "code", code, "retryable", biz.Retryable(err), "err", err)
		return 1
	}
	return 0
}

func runPipeline(ctx context.Context, media *conf.Media, logger *slog.Logger) error {
	processorCfg := media.GetProcessor()
	processor, err := data.NewFFmpegProcessor(data.FFmpegConfig{
		FFmpegPath:   processorCfg.GetFfmpegPath(),
		FFprobePath:  processorCfg.GetFfprobePath(),
		Preset:       processorCfg.GetPreset(),
		CRF:          processorCfg.GetCrf(),
		AudioBitrate: processorCfg.GetAudioBitrate(),
		Timeout:      durationOrDefault(processorCfg.GetTimeout(), 10*time.Minute),
	}, logger)
	if err != nil {
		return err
	}
	downloadCfg := media.GetDownload()
	downloader, err := data.NewHTTPDownloader(data.HTTPDownloaderConfig{
		Timeout:  durationOrDefault(downloadCfg.GetTimeout(), 5*time.Minute),
		MaxBytes: downloadCfg.GetMaxBytes(),
	})
	if err != nil {
		return err
	}

	opts := biz.PipelineOptions{
		TempDir:        media.GetTempDir(),
		ProfileKey:     media.GetProfileVersion(),
		KeyPrefix:      media.GetStorage().GetCos().GetPrefix(),
		MaxInputBytes:  downloadCfg.GetMaxBytes(),
		MaxOutputBytes: media.GetMaxOutputBytes(),
	}
	var resolver biz.Resolver
	var storage biz.ObjectStorage
	if !flagProcess {
		if driver := media.GetStorage().GetDriver(); driver != "" && driver != "cos" {
			return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("only the cos storage driver is implemented"))
		}
		storageCfg := media.GetStorage().GetCos()
		cosStorage, err := data.NewCOSStorage(data.COSStorageConfig{
			Bucket:       storageCfg.GetBucket(),
			Region:       storageCfg.GetRegion(),
			SecretID:     storageCfg.GetSecretId(),
			SecretKey:    storageCfg.GetSecretKey(),
			SessionToken: storageCfg.GetSessionToken(),
		})
		if err != nil {
			return err
		}
		storage = cosStorage
		if flagPrintURL {
			opts.SignTTL = durationOrDefault(media.GetStorage().GetSignTtl(), 10*time.Minute)
		}
	}
	if flagInput == "" {
		resolverCfg := media.GetResolver()
		switch resolverCfg.GetDriver() {
		case "command":
			commandResolver, err := data.NewCommandResolver(data.CommandResolverConfig{
				Command: resolverCfg.GetCommand(),
				Args:    resolverCfg.GetArgs(),
				Timeout: durationOrDefault(resolverCfg.GetTimeout(), 30*time.Second),
			})
			if err != nil {
				return err
			}
			resolver = commandResolver
		case "hongguo_sdk":
			sdkResolver, err := data.NewSDKResolver(data.SDKResolverConfig{
				ProfilePath: resolverCfg.GetProfilePath(),
				Quality:     resolverCfg.GetQuality(),
				Timeout:     durationOrDefault(resolverCfg.GetTimeout(), 30*time.Second),
			})
			if err != nil {
				return err
			}
			resolver = sdkResolver
		default:
			return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, errors.New("unsupported media resolver driver"))
		}
	}

	pipeline, err := biz.NewPipeline(resolver, downloader, processor, storage, opts, logger)
	if err != nil {
		return err
	}
	result, err := pipeline.Run(ctx, biz.RunInput{
		SourceCode:        flagSource,
		ExternalEpisodeID: flagEpisodeID,
		LocalInputPath:    flagInput,
		Quality:           flagQuality,
		ProcessOnly:       flagProcess,
		KeepProcessed:     flagKeep != "",
		KeepProcessedDir:  flagKeep,
	})
	if err != nil {
		return err
	}
	out := runOutput{OK: true, ProcessedPath: result.ProcessedPath}
	for _, stage := range result.Stages {
		out.Stages = append(out.Stages, stageOutput{
			Stage:     string(stage.Stage),
			Detail:    stage.Detail,
			ElapsedMS: stage.FinishedAt.Sub(stage.StartedAt).Milliseconds(),
		})
	}
	if result.Asset != nil {
		out.Asset = &assetOutput{
			State:      string(result.Asset.State),
			Provider:   result.Asset.Object.Provider,
			Bucket:     result.Asset.Object.Bucket,
			Region:     result.Asset.Object.Region,
			Key:        result.Asset.Object.Key,
			VersionID:  result.Asset.Object.VersionID,
			SizeBytes:  result.Asset.SizeBytes,
			SHA256:     result.Asset.SHA256,
			Remuxed:    result.Asset.Remuxed,
			ProfileKey: result.Asset.ProfileKey,
			SourceID:   result.Asset.SourceProvider,
			ExternalID: result.Asset.ExternalID,
		}
		if info := result.Asset.Info; info != nil {
			out.Asset.VideoCodec = info.VideoCodec
			out.Asset.AudioCodec = info.AudioCodec
			out.Asset.DurationMS = info.DurationMS
			out.Asset.Width = info.Width
			out.Asset.Height = info.Height
		}
	}
	if result.Signed != nil {
		out.PreviewURL = result.Signed.URL
		out.PreviewExpiresAt = result.Signed.ExpiresAt.UTC().Format(time.RFC3339)
	}
	emit(out)
	return nil
}

func emit(out runOutput) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(out)
}

func stageOf(err error) biz.MediaStage {
	var me *biz.MediaError
	if errors.As(err, &me) {
		return me.Stage
	}
	return biz.MediaStageAcquire
}

func durationOrDefault(value *durationpb.Duration, fallback time.Duration) time.Duration {
	if value == nil {
		return fallback
	}
	parsed := value.AsDuration()
	if parsed <= 0 {
		return fallback
	}
	return parsed
}
