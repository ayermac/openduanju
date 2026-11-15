package biz

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// PipelineOptions are the tunables of a single-episode run. They are supplied
// from the media configuration block, keeping biz independent from conf.
type PipelineOptions struct {
	// TempDir is the root for per-run working directories.
	TempDir string
	// ProfileKey names the fixed delivery profile recorded on the asset.
	ProfileKey string
	// KeyPrefix is the object-key namespace; keys stay content addressed.
	KeyPrefix string
	// MaxInputBytes bounds any downloaded or supplied input.
	MaxInputBytes int64
	// MaxOutputBytes bounds the normalized output.
	MaxOutputBytes int64
	// SignTTL requests a preview credential when positive.
	SignTTL time.Duration
}

// Pipeline runs the single-episode media path. It performs external IO and
// must not be called inside an HTTP handler or a database transaction.
type Pipeline struct {
	resolver   Resolver
	downloader Downloader
	processor  MediaProcessor
	storage    ObjectStorage
	opts       PipelineOptions
	logger     *slog.Logger
}

// NewPipeline builds the single-episode pipeline. Resolver is optional: a run
// with a local input does not use it. Storage is required.
func NewPipeline(
	resolver Resolver,
	downloader Downloader,
	processor MediaProcessor,
	storage ObjectStorage,
	opts PipelineOptions,
	logger *slog.Logger,
) (*Pipeline, error) {
	if processor == nil {
		return nil, NewMediaError(MediaStageValidate, MediaCodeConfigInvalid, false, errors.New("media processor is not configured"))
	}
	if opts.ProfileKey == "" {
		return nil, NewMediaError(MediaStageProcess, MediaCodeConfigInvalid, false, errors.New("processing profile is not configured"))
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Pipeline{
		resolver:   resolver,
		downloader: downloader,
		processor:  processor,
		storage:    storage,
		opts:       opts,
		logger:     logger,
	}, nil
}

// Run executes acquire → download → validate → process → upload → verify.
// It returns a verified object identity; it never claims the asset is ready
// because no database revision link exists in this slice.
func (p *Pipeline) Run(ctx context.Context, in RunInput) (*RunResult, error) {
	if err := validateRunInput(in); err != nil {
		return nil, err
	}
	root := p.opts.TempDir
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, fmt.Errorf("create media temp root: %w", err))
	}
	jobDir, err := os.MkdirTemp(root, "media-")
	if err != nil {
		return nil, NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, fmt.Errorf("create job directory: %w", err))
	}
	defer func() {
		if removeErr := os.RemoveAll(jobDir); removeErr != nil {
			p.logger.Warn("media job directory cleanup failed", "dir", jobDir, "err", removeErr)
		}
	}()

	result := &RunResult{}
	inputPath := in.LocalInputPath
	var downloaded *DownloadedFile

	// acquire: either a real SDK episode or an authorized local file.
	start := time.Now()
	var src *MediaSource
	if inputPath == "" {
		if p.resolver == nil || p.downloader == nil {
			return nil, NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, errors.New("external episode requires a resolver and a downloader"))
		}
		src, err = p.resolver.Resolve(ctx, ResolveRequest{
			SourceCode:        in.SourceCode,
			ExternalEpisodeID: in.ExternalEpisodeID,
			Quality:           in.Quality,
		})
		if err != nil {
			return nil, p.classify(ctx, MediaStageAcquire, err)
		}
		if src == nil || strings.TrimSpace(src.URL) == "" {
			return nil, NewMediaError(MediaStageAcquire, MediaCodeSourceUnavailable, true, errors.New("resolver returned no media URL"))
		}
		p.record(result, MediaStageAcquire, start, "resolved external episode")
	} else {
		info, statErr := os.Stat(inputPath)
		if statErr != nil {
			return nil, NewMediaError(MediaStageAcquire, MediaCodeMediaNotFound, false, statErr)
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil, NewMediaError(MediaStageAcquire, MediaCodeMediaInvalid, false, errors.New("local input is not a non-empty regular file"))
		}
		if p.opts.MaxInputBytes > 0 && info.Size() > p.opts.MaxInputBytes {
			return nil, NewMediaError(MediaStageAcquire, MediaCodeMediaTooLarge, false, fmt.Errorf("local input is %d bytes, limit is %d", info.Size(), p.opts.MaxInputBytes))
		}
		p.record(result, MediaStageAcquire, start, "accepted local input")
	}

	// download: only for remote inputs.
	start = time.Now()
	if src != nil {
		downloaded, err = p.downloader.Download(ctx, src, jobDir)
		if err != nil {
			return nil, p.classify(ctx, MediaStageDownload, err)
		}
		if downloaded == nil || downloaded.Path == "" || downloaded.SizeBytes <= 0 {
			return nil, NewMediaError(MediaStageDownload, MediaCodeDownloadIncomplete, true, errors.New("downloader returned an empty file"))
		}
		inputPath = downloaded.Path
		if p.opts.MaxInputBytes > 0 && downloaded.SizeBytes > p.opts.MaxInputBytes {
			return nil, NewMediaError(MediaStageDownload, MediaCodeMediaTooLarge, false, fmt.Errorf("downloaded input is %d bytes, limit is %d", downloaded.SizeBytes, p.opts.MaxInputBytes))
		}
		p.record(result, MediaStageDownload, start, fmt.Sprintf("downloaded %d bytes", downloaded.SizeBytes))
	}

	// validate: the input must contain a decodable video track.
	start = time.Now()
	inputInfo, err := p.processor.Probe(ctx, inputPath)
	if err != nil {
		return nil, p.classify(ctx, MediaStageValidate, err)
	}
	if inputInfo == nil || !inputInfo.Playable() {
		return nil, NewMediaError(MediaStageValidate, MediaCodeMediaInvalid, false, errors.New("input has no playable video track"))
	}
	p.record(result, MediaStageValidate, start, fmt.Sprintf("container=%s video=%s audio=%s %dx%d", inputInfo.Container, inputInfo.VideoCodec, inputInfo.AudioCodec, inputInfo.Width, inputInfo.Height))

	// process: normalize to the delivery profile.
	start = time.Now()
	processed, err := p.processor.Prepare(ctx, ProcessRequest{
		InputPath:  inputPath,
		OutputDir:  jobDir,
		ProfileKey: p.opts.ProfileKey,
	})
	if err != nil {
		return nil, p.classify(ctx, MediaStageProcess, err)
	}
	if err := p.checkProcessed(processed); err != nil {
		return nil, err
	}
	mode := "transcode"
	if processed.Remuxed {
		mode = "remux"
	}
	p.record(result, MediaStageProcess, start, fmt.Sprintf("%s to %s (%d bytes, sha256=%s)", mode, p.opts.ProfileKey, processed.SizeBytes, processed.SHA256))

	if in.ProcessOnly {
		result.ProcessedPath = processed.Path
		if in.KeepProcessedDir != "" {
			copied, copyErr := p.copyForPreview(processed.Path, in.KeepProcessedDir, processed.SHA256)
			if copyErr != nil {
				return nil, copyErr
			}
			result.ProcessedPath = copied
		}
		return result, nil
	}

	// upload: reuse a confirmed object with the same content identity.
	if p.storage == nil {
		return nil, NewMediaError(MediaStageUpload, MediaCodeConfigInvalid, false, errors.New("object storage is not configured"))
	}
	start = time.Now()
	ref := ObjectRef{Key: ObjectKey(p.opts.KeyPrefix, processed.SHA256)}
	identity := ref
	existing, headErr := p.storage.Head(ctx, ref)
	reused := false
	switch {
	case headErr == nil && existing != nil:
		if existing.SizeBytes != processed.SizeBytes {
			return nil, NewMediaError(MediaStageUpload, MediaCodeStorageObjectMismatch, false,
				fmt.Errorf("existing object size %d does not match processed size %d", existing.SizeBytes, processed.SizeBytes))
		}
		if existing.MetadataSHA256 != "" {
			if !strings.EqualFold(existing.MetadataSHA256, processed.SHA256) {
				return nil, NewMediaError(MediaStageUpload, MediaCodeStorageObjectMismatch, false,
					errors.New("existing object metadata hash does not match processed content"))
			}
			identity = existing.ObjectRef
			reused = true
		}
		// Missing metadata is repaired by uploading again under the same key;
		// the returned (possibly new) version becomes the identity.
	case headErr != nil && !isCode(headErr, MediaCodeStorageObjectMissing):
		return nil, p.classify(ctx, MediaStageUpload, headErr)
	}
	if reused {
		p.record(result, MediaStageUpload, start, fmt.Sprintf("reused existing object version=%s", identity.VersionID))
	} else {
		stored, putErr := p.storage.PutFile(ctx, PutObjectRequest{
			Key:         ref.Key,
			Path:        processed.Path,
			ContentType: "video/mp4",
			SHA256:      processed.SHA256,
			MD5Base64:   processed.MD5Base64,
			SizeBytes:   processed.SizeBytes,
		})
		if putErr != nil {
			return nil, p.classify(ctx, MediaStageUpload, putErr)
		}
		if stored == nil {
			return nil, NewMediaError(MediaStageUpload, MediaCodeStorageUnavailable, true, errors.New("storage returned no object"))
		}
		if stored.Key == "" {
			stored.Key = ref.Key
		}
		if err := checkStoredObject(stored, processed); err != nil {
			return nil, err
		}
		identity = stored.ObjectRef
		p.record(result, MediaStageUpload, start, fmt.Sprintf("uploaded object version=%s", identity.VersionID))
	}

	// verify: HEAD the exact identity returned by upload/reuse, never "latest".
	start = time.Now()
	confirmed, err := p.storage.Head(ctx, identity)
	if err != nil {
		return nil, p.classify(ctx, MediaStageVerify, err)
	}
	if confirmed == nil {
		return nil, NewMediaError(MediaStageVerify, MediaCodeStorageObjectMissing, true, errors.New("object is missing after upload"))
	}
	if confirmed.Key != identity.Key {
		return nil, NewMediaError(MediaStageVerify, MediaCodeStorageObjectMismatch, false,
			fmt.Errorf("confirmed key %q does not match expected %q", confirmed.Key, identity.Key))
	}
	if identity.VersionID != "" && confirmed.VersionID != identity.VersionID {
		return nil, NewMediaError(MediaStageVerify, MediaCodeStorageObjectMismatch, false,
			fmt.Errorf("confirmed version %q does not match expected %q", confirmed.VersionID, identity.VersionID))
	}
	if err := checkStoredObject(confirmed, processed); err != nil {
		return nil, err
	}
	p.record(result, MediaStageVerify, start, fmt.Sprintf("confirmed object %d bytes version=%s", confirmed.SizeBytes, confirmed.VersionID))

	asset := &MediaAsset{
		Object:         confirmed.ObjectRef,
		State:          MediaAssetVerified,
		SizeBytes:      confirmed.SizeBytes,
		SHA256:         processed.SHA256,
		ContentType:    "video/mp4",
		Info:           processed.Info,
		ProfileKey:     p.opts.ProfileKey,
		SourceProvider: in.SourceCode,
		ExternalID:     in.ExternalEpisodeID,
		Remuxed:        processed.Remuxed,
	}
	if downloaded != nil {
		asset.DownloadedSize = downloaded.SizeBytes
		asset.DownloadedHash = downloaded.SHA256
	}
	result.Asset = asset
	if in.KeepProcessed && in.KeepProcessedDir != "" {
		copied, copyErr := p.copyForPreview(processed.Path, in.KeepProcessedDir, processed.SHA256)
		if copyErr != nil {
			return nil, copyErr
		}
		result.ProcessedPath = copied
	}
	if p.opts.SignTTL > 0 {
		signed, signErr := p.storage.PresignGet(ctx, asset.Object, p.opts.SignTTL)
		if signErr != nil {
			return nil, p.classify(ctx, MediaStageVerify, signErr)
		}
		result.Signed = signed
	}
	p.logger.Info("media pipeline verified",
		"stage", string(MediaStageVerify),
		"source", in.SourceCode,
		"external_id", in.ExternalEpisodeID,
		"key", asset.Object.Key,
		"sha256", asset.SHA256,
		"bytes", asset.SizeBytes,
		"remuxed", asset.Remuxed,
	)
	return result, nil
}

func validateRunInput(in RunInput) error {
	hasEpisode := strings.TrimSpace(in.ExternalEpisodeID) != ""
	hasLocal := strings.TrimSpace(in.LocalInputPath) != ""
	switch {
	case hasEpisode && hasLocal:
		return NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, errors.New("run input has both an external episode and a local file"))
	case !hasEpisode && !hasLocal:
		return NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, errors.New("run input needs an external episode or a local file"))
	case hasEpisode && strings.TrimSpace(in.SourceCode) == "":
		return NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, errors.New("external episode requires a source code"))
	}
	if hasLocal && in.KeepProcessedDir != "" && !in.ProcessOnly && !in.KeepProcessed {
		return NewMediaError(MediaStageProcess, MediaCodeConfigInvalid, false, errors.New("keep-processed directory requires keeping the processed file"))
	}
	if in.KeepProcessed && strings.TrimSpace(in.KeepProcessedDir) == "" {
		return NewMediaError(MediaStageProcess, MediaCodeConfigInvalid, false, errors.New("keeping the processed file requires a directory"))
	}
	return nil
}

func (p *Pipeline) checkProcessed(processed *ProcessedMedia) error {
	if processed == nil || processed.Path == "" {
		return NewMediaError(MediaStageProcess, MediaCodeProcessFailed, true, errors.New("processor returned no output file"))
	}
	if processed.Info == nil || !processed.Info.Playable() {
		return NewMediaError(MediaStageProcess, MediaCodeMediaUnsupported, false, errors.New("processed output has no playable video track"))
	}
	if !strings.EqualFold(processed.Info.VideoCodec, "h264") {
		return NewMediaError(MediaStageProcess, MediaCodeMediaUnsupported, false, fmt.Errorf("processed video codec is %q, want h264", processed.Info.VideoCodec))
	}
	if processed.Info.AudioCodec != "" && !strings.EqualFold(processed.Info.AudioCodec, "aac") {
		return NewMediaError(MediaStageProcess, MediaCodeMediaUnsupported, false, fmt.Errorf("processed audio codec is %q, want aac", processed.Info.AudioCodec))
	}
	if len(processed.SHA256) != 64 {
		return NewMediaError(MediaStageProcess, MediaCodeProcessFailed, false, fmt.Errorf("processed file has no sha256"))
	}
	if decoded, err := base64.StdEncoding.DecodeString(processed.MD5Base64); err != nil || len(decoded) != md5.Size {
		return NewMediaError(MediaStageProcess, MediaCodeProcessFailed, false, errors.New("processed file has no valid upload checksum"))
	}
	if processed.SizeBytes <= 0 {
		return NewMediaError(MediaStageProcess, MediaCodeProcessFailed, false, errors.New("processed file is empty"))
	}
	if p.opts.MaxOutputBytes > 0 && processed.SizeBytes > p.opts.MaxOutputBytes {
		return NewMediaError(MediaStageProcess, MediaCodeMediaTooLarge, false, fmt.Errorf("processed file is %d bytes, limit is %d", processed.SizeBytes, p.opts.MaxOutputBytes))
	}
	return nil
}

// checkStoredObject compares the uploader's custom SHA-256 metadata with the
// local content identity. It detects a wrong/overwritten object but does not
// prove the received bytes: transport integrity comes from Content-MD5 and,
// for real acceptance, a post-download SHA-256 comparison.
func checkStoredObject(stored *StoredObject, processed *ProcessedMedia) error {
	if stored == nil {
		return NewMediaError(MediaStageVerify, MediaCodeStorageObjectMissing, true, errors.New("stored object is missing"))
	}
	if stored.SizeBytes != processed.SizeBytes {
		return NewMediaError(MediaStageVerify, MediaCodeStorageObjectMismatch, false, fmt.Errorf("stored size %d does not match processed size %d", stored.SizeBytes, processed.SizeBytes))
	}
	if stored.MetadataSHA256 == "" {
		return NewMediaError(MediaStageVerify, MediaCodeStorageObjectMismatch, false, errors.New("stored object has no sha256 metadata"))
	}
	if !strings.EqualFold(stored.MetadataSHA256, processed.SHA256) {
		return NewMediaError(MediaStageVerify, MediaCodeStorageObjectMismatch, false, errors.New("stored metadata hash does not match processed content"))
	}
	return nil
}

func (p *Pipeline) classify(ctx context.Context, stage MediaStage, err error) error {
	return classifyMediaError(ctx, stage, err)
}

// classifyMediaError turns an adapter failure into a classified media
// error. It is shared by the one-shot pipeline and the persistent worker.
func classifyMediaError(ctx context.Context, stage MediaStage, err error) error {
	var me *MediaError
	if errors.As(err, &me) {
		return me
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return NewMediaError(stage, MediaCodeCanceled, false, ctxErr)
		}
		return NewMediaError(stage, MediaCodeProcessTimeout, true, ctxErr)
	}
	if errors.Is(err, context.Canceled) {
		return NewMediaError(stage, MediaCodeCanceled, false, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewMediaError(stage, MediaCodeProcessTimeout, true, err)
	}
	// Unclassified failures fall back to a stage-appropriate code instead of
	// reporting every I/O problem as a download failure.
	switch stage {
	case MediaStageAcquire:
		return NewMediaError(stage, MediaCodeSourceUnavailable, true, err)
	case MediaStageValidate:
		return NewMediaError(stage, MediaCodeMediaInvalid, false, err)
	case MediaStageProcess:
		return NewMediaError(stage, MediaCodeProcessFailed, false, err)
	case MediaStageUpload, MediaStageVerify:
		return NewMediaError(stage, MediaCodeStorageUnavailable, true, err)
	default:
		return NewMediaError(stage, MediaCodeDownloadFailed, true, err)
	}
}

func (p *Pipeline) record(result *RunResult, stage MediaStage, start time.Time, detail string) {
	result.Stages = append(result.Stages, StageRecord{
		Stage:      stage,
		StartedAt:  start,
		FinishedAt: time.Now(),
		Detail:     detail,
	})
	p.logger.Info("media stage", "stage", string(stage), "detail", detail, "elapsed_ms", time.Since(start).Milliseconds())
}

// ObjectKey is content addressed: the same processed bytes always map to the
// same immutable key, which makes a retry idempotent.
func ObjectKey(prefix, sha256 string) string {
	if len(sha256) < 2 {
		return path.Join(strings.Trim(prefix, "/"), sha256+".mp4")
	}
	return path.Join(strings.Trim(prefix, "/"), sha256[:2], sha256+".mp4")
}

func isCode(err error, code MediaErrorCode) bool {
	got, ok := MediaErrorCodeOf(err)
	return ok && got == code
}

func (p *Pipeline) copyForPreview(path, dir, sha256 string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", NewMediaError(MediaStageProcess, MediaCodeProcessFailed, false, fmt.Errorf("create preview directory: %w", err))
	}
	dst := filepath.Join(dir, sha256+".mp4")
	if err := copyFile(path, dst); err != nil {
		return "", NewMediaError(MediaStageProcess, MediaCodeProcessFailed, false, fmt.Errorf("copy preview file: %w", err))
	}
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
