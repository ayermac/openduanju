package data

import (
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"

	"google.golang.org/protobuf/types/known/durationpb"
)

// MediaWorkerProvider builds the persistent media worker from configuration.
//
// A deployment that has no media configuration yet still has to serve the
// admin API, so an incomplete configuration returns a nil worker instead of an
// error: the admin API keeps working and the operator sees the reason in the
// log. A configuration that names an unknown driver is a deployment mistake
// and fails loudly.
func NewMediaWorkerProvider(c *conf.Media, repo *MediaBatchRepo, uploads *UploadRepo, logger *slog.Logger) (*biz.MediaWorker, error) {
	if c == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	profile := c.GetProfileVersion()
	if profile == "" {
		logger.Warn("media worker is disabled: no processing profile is configured")
		return nil, nil
	}
	bundle, err := buildMediaStorage(c, logger)
	if err != nil {
		return nil, err
	}
	if bundle == nil || bundle.storage == nil {
		return nil, nil
	}
	processorCfg := c.GetProcessor()
	processor, err := NewFFmpegProcessor(FFmpegConfig{
		FFmpegPath:   processorCfg.GetFfmpegPath(),
		FFprobePath:  processorCfg.GetFfprobePath(),
		Preset:       processorCfg.GetPreset(),
		CRF:          processorCfg.GetCrf(),
		AudioBitrate: processorCfg.GetAudioBitrate(),
		Timeout:      mediaDuration(processorCfg.GetTimeout(), 10*time.Minute),
	}, logger)
	if err != nil {
		logger.Warn("media worker is disabled: the processor is not configured", "err", err.Error())
		return nil, nil
	}
	downloadCfg := c.GetDownload()
	downloader, err := NewHTTPDownloader(HTTPDownloaderConfig{
		Timeout:  mediaDuration(downloadCfg.GetTimeout(), 5*time.Minute),
		MaxBytes: downloadCfg.GetMaxBytes(),
	})
	if err != nil {
		logger.Warn("media worker is disabled: the downloader is not configured", "err", err.Error())
		return nil, nil
	}
	resolver := buildMediaResolver(c, logger)
	if resolver == nil {
		logger.Warn("media worker runs without a resolver: external episodes cannot be resolved")
	}
	worker, err := biz.NewMediaWorker(repo, resolver, downloader, processor, bundle.storage, biz.MediaWorkerOptions{
		Worker:         "media-worker-1",
		Lease:          90 * time.Second,
		BatchSize:      4,
		PollInterval:   2 * time.Second,
		SourceCode:     c.GetSourceCode(),
		Quality:        c.GetResolver().GetQuality(),
		ProfileKey:     profile,
		KeyPrefix:      bundle.prefix,
		TempDir:        c.GetTempDir(),
		MaxInputBytes:  downloadCfg.GetMaxBytes(),
		MaxOutputBytes: c.GetMaxOutputBytes(),
		RetryBackoff:   time.Minute,
	}, logger)
	if err != nil {
		return nil, err
	}
	if uploads != nil {
		// A manual task reads its confirmed object instead of the resolver.
		worker = worker.WithUploads(uploads)
	}
	return worker, nil
}

// mediaStorageBundle is the configured object storage with the identity a
// session needs to persist: the driver name, bucket and region are part of
// every stored object reference.
type mediaStorageBundle struct {
	storage  biz.ObjectStorage
	prefix   string
	provider string
	bucket   string
	region   string
}

// buildMediaStorage returns the configured object storage and its identity.
// It returns a nil bundle when the driver is not configured yet.
func buildMediaStorage(c *conf.Media, logger *slog.Logger) (*mediaStorageBundle, error) {
	storageCfg := c.GetStorage()
	switch storageCfg.GetDriver() {
	case "":
		logger.Warn("media worker is disabled: no storage driver is configured")
		return nil, nil
	case "cos":
		cosCfg := storageCfg.GetCos()
		storage, err := NewCOSStorage(COSStorageConfig{
			Bucket:       cosCfg.GetBucket(),
			Region:       cosCfg.GetRegion(),
			SecretID:     cosCfg.GetSecretId(),
			SecretKey:    cosCfg.GetSecretKey(),
			SessionToken: cosCfg.GetSessionToken(),
		})
		if err != nil {
			logger.Warn("media worker is disabled: the cos storage is not configured", "err", err.Error())
			return nil, nil
		}
		return &mediaStorageBundle{
			storage:  storage,
			prefix:   cosCfg.GetPrefix(),
			provider: "cos",
			bucket:   cosCfg.GetBucket(),
			region:   cosCfg.GetRegion(),
		}, nil
	case "local":
		localCfg := storageCfg.GetLocal()
		storage, err := NewLocalObjectStorage(localCfg.GetRoot())
		if err != nil {
			return nil, err
		}
		return &mediaStorageBundle{
			storage:  storage,
			prefix:   localCfg.GetPrefix(),
			provider: "local",
			bucket:   filepath.Base(storage.root),
			region:   "local",
		}, nil
	default:
		return nil, errors.New("unsupported media storage driver: " + storageCfg.GetDriver())
	}
}

// NewUploadsProvider builds the manual upload use case. An incomplete media
// configuration disables uploads instead of failing the admin API: the reason
// is logged, and every upload call answers with a configuration error.
func NewUploadsProvider(c *conf.Media, repo *UploadRepo, logger *slog.Logger) (*biz.Uploads, error) {
	if c == nil {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	bundle, err := buildMediaStorage(c, logger)
	if err != nil {
		return nil, err
	}
	if bundle == nil || bundle.storage == nil {
		logger.Warn("manual upload is disabled: no object storage is configured")
		return nil, nil
	}
	return biz.NewUploads(repo, repo, bundle.storage, biz.UploadOptions{
		TTL:             15 * time.Minute,
		MaxEpisodeBytes: c.GetDownload().GetMaxBytes(),
		MaxCoverBytes:   10 << 20,
		KeyPrefix:       bundle.prefix,
		TempDir:         c.GetTempDir(),
		MinCoverSide:    240,
		MaxCoverSide:    10000,
		Provider:        bundle.provider,
		Bucket:          bundle.bucket,
		Region:          bundle.region,
	}), nil
}

// NewStagingHandler exposes the local driver's signed PUT endpoint. The COS
// driver returns nil: it issues a real presigned URL instead.
func NewStagingHandler(c *conf.Media, logger *slog.Logger) (http.Handler, error) {
	if c == nil || c.GetStorage().GetDriver() != "local" {
		return nil, nil
	}
	storage, err := NewLocalObjectStorage(c.GetStorage().GetLocal().GetRoot())
	if err != nil {
		return nil, err
	}
	if logger != nil {
		logger.Info("local staging upload endpoint is enabled", "path", "/api/admin/v1/uploads/staging/{token}")
	}
	return storage, nil
}

// buildMediaResolver returns the configured resolver. It returns nil when no
// resolver is configured; the worker then rejects external episodes with a
// configuration error instead of guessing an address.
func buildMediaResolver(c *conf.Media, logger *slog.Logger) biz.Resolver {
	resolverCfg := c.GetResolver()
	switch resolverCfg.GetDriver() {
	case "":
		return nil
	case "command":
		resolver, err := NewCommandResolver(CommandResolverConfig{
			Command: resolverCfg.GetCommand(),
			Args:    resolverCfg.GetArgs(),
			Timeout: mediaDuration(resolverCfg.GetTimeout(), 30*time.Second),
		})
		if err != nil {
			logger.Warn("media resolver is unavailable", "err", err.Error())
			return nil
		}
		return resolver
	case "hongguo_sdk":
		resolver, err := NewSDKResolver(SDKResolverConfig{
			ProfilePath: resolverCfg.GetProfilePath(),
			Quality:     resolverCfg.GetQuality(),
			Timeout:     mediaDuration(resolverCfg.GetTimeout(), 30*time.Second),
		})
		if err != nil {
			logger.Warn("media resolver is unavailable", "err", err.Error())
			return nil
		}
		return resolver
	default:
		logger.Warn("media resolver is unavailable: unsupported driver", "driver", resolverCfg.GetDriver())
		return nil
	}
}

// mediaDuration returns the configured duration or the fallback.
func mediaDuration(value *durationpb.Duration, fallback time.Duration) time.Duration {
	if value == nil {
		return fallback
	}
	parsed := value.AsDuration()
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

// NewPreviewStorage builds the storage the review screen uses to sign private
// preview URLs. It uses the same configuration as the worker, so a deployment
// cannot preview from a different bucket by accident.
func NewPreviewStorage(c *conf.Media) (biz.ObjectStorage, error) {
	storageCfg := c.GetStorage()
	switch storageCfg.GetDriver() {
	case "cos":
		cosCfg := storageCfg.GetCos()
		return NewCOSStorage(COSStorageConfig{
			Bucket:    cosCfg.GetBucket(),
			Region:    cosCfg.GetRegion(),
			SecretID:  cosCfg.GetSecretId(),
			SecretKey: cosCfg.GetSecretKey(),
		})
	case "local":
		return NewLocalObjectStorage(storageCfg.GetLocal().GetRoot())
	case "":
		return nil, errors.New("media storage driver is not configured")
	default:
		return nil, errors.New("unsupported media storage driver: " + storageCfg.GetDriver())
	}
}
