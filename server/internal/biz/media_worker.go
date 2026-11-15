package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MediaWorkerOptions are the tunables of the persistent media worker.
type MediaWorkerOptions struct {
	// Worker identifies the lease owner; a stale owner cannot commit.
	Worker string
	// Lease is how long a claimed item stays owned. A running task renews it.
	Lease time.Duration
	// BatchSize limits how many items one tick claims.
	BatchSize int
	// PollInterval is the delay between ticks.
	PollInterval time.Duration
	// SourceCode is the source the SDK resolver reads from.
	SourceCode string
	// Quality is the requested stream quality.
	Quality string
	// ProfileKey names the fixed delivery profile.
	ProfileKey string
	// KeyPrefix is the object-key namespace.
	KeyPrefix string
	// TempDir is the root for per-item working directories. The directory of
	// an item is stable, so a resumed task finds its artifacts again.
	TempDir string
	// MaxInputBytes bounds a downloaded input.
	MaxInputBytes int64
	// MaxOutputBytes bounds the normalized output.
	MaxOutputBytes int64
	// RetryBackoff is the delay before a retryable item is picked up again.
	RetryBackoff time.Duration
}

// MediaWorker processes the episode tasks of persistent batches. Every
// stage boundary is a database checkpoint: a crashed worker resumes at the
// last committed stage, and a stale worker is rejected.
type MediaWorker struct {
	repo       MediaBatchRepo
	resolver   Resolver
	downloader Downloader
	processor  MediaProcessor
	storage    ObjectStorage
	// uploads reads the confirmed object of a manual input. It stays nil for
	// a deployment that only resolves external episodes.
	uploads MediaInputReader
	opts    MediaWorkerOptions
	logger  *slog.Logger
}

// WithUploads attaches the manual-upload reader. A task that names an upload
// session without a reader fails as a configuration error instead of silently
// resolving the source.
func (w *MediaWorker) WithUploads(reader MediaInputReader) *MediaWorker {
	w.uploads = reader
	return w
}

// NewMediaWorker builds the persistent media worker.
func NewMediaWorker(
	repo MediaBatchRepo,
	resolver Resolver,
	downloader Downloader,
	processor MediaProcessor,
	storage ObjectStorage,
	opts MediaWorkerOptions,
	logger *slog.Logger,
) (*MediaWorker, error) {
	if repo == nil {
		return nil, errors.New("media worker requires a repository")
	}
	if processor == nil {
		return nil, errors.New("media worker requires a processor")
	}
	if storage == nil {
		return nil, errors.New("media worker requires object storage")
	}
	if opts.Worker == "" {
		return nil, errors.New("media worker requires a worker name")
	}
	if opts.Lease <= 0 {
		opts.Lease = 90 * time.Second
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 4
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = time.Minute
	}
	if opts.ProfileKey == "" {
		return nil, errors.New("media worker requires a processing profile")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &MediaWorker{
		repo:       repo,
		resolver:   resolver,
		downloader: downloader,
		processor:  processor,
		storage:    storage,
		opts:       opts,
		logger:     logger,
	}, nil
}

// Run processes items until the context is cancelled.
func (w *MediaWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.ProcessOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.logger.Error("media worker tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ProcessOnce claims and processes one batch of items. It returns the
// number of items it handled.
func (w *MediaWorker) ProcessOnce(ctx context.Context) (int, error) {
	items, err := w.repo.ClaimItems(ctx, w.opts.Worker, w.opts.Lease, w.opts.BatchSize)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, item := range items {
		if ctx.Err() != nil {
			return handled, ctx.Err()
		}
		if err := w.processItem(ctx, item); err != nil {
			switch {
			case errors.Is(err, ErrBatchCancelled):
				w.logger.Info("media item stopped", "item", item.ID, "batch", item.BatchID, "reason", err.Error())
			case errors.Is(err, ErrItemLeaseLost):
				w.logger.Info("media item lease lost", "item", item.ID, "batch", item.BatchID, "reason", err.Error())
			case errors.Is(err, ErrRevisionNotOwned):
				// The revision left the processing state: the material is
				// not wanted any more and the task stops for good.
				w.logger.Info("media item stopped", "item", item.ID, "batch", item.BatchID, "reason", err.Error())
			default:
				w.logger.Error("media item failed", "item", item.ID, "batch", item.BatchID, "err", err)
			}
		}
		handled++
	}
	// The review gate is re-evaluated every tick: a batch whose items were
	// ready before its cover was uploaded is completed here.
	if _, err := w.repo.RefreshReadyBatches(ctx, 20); err != nil {
		w.logger.Error("media review gate refresh failed", "err", err)
	}
	return handled, nil
}

// processItem runs one episode task through the persistent stage machine. The
// task resumes at the stage it reached before: every reusable artifact is
// verified before it is trusted, and a missing or changed artifact is redone.
func (w *MediaWorker) processItem(ctx context.Context, item MediaItem) error {
	// The task was queued by a request that is long gone. The identity is
	// read back from the row, so every log line of this attempt - including
	// the ones after a worker restart - names the request that asked for it.
	if item.TraceID != "" {
		ctx = WithTraceID(ctx, item.TraceID)
	}
	jobDir, err := w.jobDir(item)
	if err != nil {
		return w.failItem(ctx, item, NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false, err))
	}
	// A task does not start when the frozen grant was revoked or expired: the
	// work would only produce material that cannot be delivered. The commit
	// path checks again, because the grant can change mid-flight.
	if err := w.repo.EnsureBatchAuthorization(ctx, item.BatchID, PermissionProcess); err != nil {
		return w.failItem(ctx, item, NewMediaError(MediaStageAcquire, MediaCodeMediaProtected, false, err))
	}

	// The lease is renewed while the task runs. A long download or
	// transcoding must not lose the item to another worker.
	taskCtx, cancelTask := context.WithCancel(ctx)
	defer cancelTask()
	renewCtx, cancelRenew := context.WithCancel(context.Background())
	renewDone := make(chan struct{})
	// The renewal goroutine reads its own snapshot: the stages keep rewriting
	// item (checkpoints, artifacts), and the lease check only needs the
	// identity and generation the task was claimed with.
	leaseItem := item
	go func() {
		defer close(renewDone)
		w.renewLease(renewCtx, leaseItem, cancelTask)
	}()
	defer func() {
		cancelRenew()
		<-renewDone
	}()

	err = w.runStages(taskCtx, &item, jobDir)
	if err == nil {
		// The material is confirmed and linked: the working directory is
		// not needed any more.
		w.cleanupJobDir(jobDir)
		return nil
	}
	if errors.Is(err, ErrBatchCancelled) || errors.Is(err, ErrItemLeaseLost) || errors.Is(err, ErrRevisionNotOwned) {
		return err
	}
	if !Retryable(err) {
		// A task that will not be retried keeps no artifact.
		w.cleanupJobDir(jobDir)
	}
	return w.failItem(ctx, item, err)
}

// runStages walks the stage machine of one task.
//
// The order follows the cheapest reusable state: material the revision
// inherited is confirmed as it is, a processed file that is still valid is
// enough to upload and to confirm, so the input is not downloaded again. Only
// when neither exists the input is acquired and the file is produced again.
func (w *MediaWorker) runStages(ctx context.Context, item *MediaItem, jobDir string) error {
	// A new revision inherits the material of the revision it was created
	// from. That material was already processed and confirmed, so the task
	// confirms it instead of processing the same bytes a second time.
	if inherited, ok := w.inheritedMaterial(ctx, *item); ok {
		if err := w.checkpoint(ctx, item, MediaItemVerifying); err != nil {
			return err
		}
		item.Object = inherited.Object
		if _, _, err := w.repo.CommitItemReady(ctx, *item, *inherited); err != nil {
			return err
		}
		return nil
	}
	processed, hasProcessed := w.reusableOutput(*item)
	info := itemInfo(*item)
	if hasProcessed && info == nil {
		// The technical shape was not stored with the file: it is read
		// from the file itself.
		probed, err := w.processor.Probe(ctx, processed.Path)
		if err != nil || probed == nil || !probed.Playable() {
			hasProcessed = false
		} else {
			info = probed
		}
	}
	if !hasProcessed {
		inputPath, _, reused := w.reusableInput(*item)
		if !reused {
			if item.InputUploadID > 0 {
				// A manual input starts at acquiring: the bytes are already
				// in the bucket, so only the download and the checks remain.
				acquired, err := w.acquireUploadedInput(ctx, item, jobDir)
				if err != nil {
					return err
				}
				inputPath = acquired
			} else {
				if err := w.checkpoint(ctx, item, MediaItemResolving); err != nil {
					return err
				}
				if w.resolver == nil || w.downloader == nil {
					return NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false,
						errors.New("external episode requires a resolver and a downloader"))
				}
				source, err := w.resolver.Resolve(ctx, ResolveRequest{
					SourceCode:        w.opts.SourceCode,
					ExternalEpisodeID: item.ExternalID,
					Quality:           w.opts.Quality,
				})
				if err != nil {
					return classifyMediaError(ctx, MediaStageAcquire, err)
				}
				if source == nil || source.URL == "" {
					return NewMediaError(MediaStageAcquire, MediaCodeSourceUnavailable, true,
						errors.New("resolver returned no media URL"))
				}
				if err := w.checkpoint(ctx, item, MediaItemAcquiring); err != nil {
					return err
				}
				downloaded, err := w.downloader.Download(ctx, source, jobDir)
				if err != nil {
					return classifyMediaError(ctx, MediaStageAcquire, err)
				}
				if downloaded == nil || downloaded.Path == "" || downloaded.SizeBytes <= 0 {
					return NewMediaError(MediaStageDownload, MediaCodeDownloadIncomplete, true,
						errors.New("downloaded input is empty"))
				}
				if w.opts.MaxInputBytes > 0 && downloaded.SizeBytes > w.opts.MaxInputBytes {
					return NewMediaError(MediaStageDownload, MediaCodeMediaTooLarge, false,
						fmt.Errorf("input exceeds the configured limit"))
				}
				inputPath = downloaded.Path
				item.InputArtifactPath = downloaded.Path
				item.InputArtifactSHA256 = downloaded.SHA256
				item.SHA256 = downloaded.SHA256
				item.SizeBytes = downloaded.SizeBytes
				item.ContentType = downloaded.ContentType
			}
		}
		probed, err := w.validateInput(ctx, item, inputPath)
		if err != nil {
			return err
		}
		info = probed
		if err := w.checkpoint(ctx, item, MediaItemProcessing); err != nil {
			return err
		}
		produced, err := w.processor.Prepare(ctx, ProcessRequest{
			InputPath:  inputPath,
			OutputDir:  jobDir,
			ProfileKey: w.opts.ProfileKey,
		})
		if err != nil {
			return classifyMediaError(ctx, MediaStageProcess, err)
		}
		if produced == nil || produced.SHA256 == "" || produced.SizeBytes <= 0 {
			return NewMediaError(MediaStageProcess, MediaCodeProcessFailed, false,
				errors.New("processed output is empty or has no sha256"))
		}
		if w.opts.MaxOutputBytes > 0 && produced.SizeBytes > w.opts.MaxOutputBytes {
			return NewMediaError(MediaStageProcess, MediaCodeMediaTooLarge, false,
				fmt.Errorf("output exceeds the configured limit"))
		}
		processed = produced
		item.OutputArtifactPath = produced.Path
		item.OutputArtifactSHA256 = produced.SHA256
		item.OutputArtifactSize = produced.SizeBytes
		item.SHA256 = produced.SHA256
		item.SizeBytes = produced.SizeBytes
		item.ContentType = "video/mp4"
	}
	item.Codec = info.VideoCodec
	item.DurationMS = info.DurationMS
	item.Width = info.Width
	item.Height = info.Height

	// The object of a resumed task is confirmed first: the upload may have
	// succeeded before the database commit failed.
	identity, err := w.uploadOutput(ctx, item, processed)
	if err != nil {
		return err
	}

	// Stage: confirm the stored object before it may become ready.
	if err := w.checkpoint(ctx, item, MediaItemVerifying); err != nil {
		return err
	}
	confirmed, err := w.confirmObject(ctx, identity, processed)
	if err != nil {
		return err
	}
	item.Object = confirmed.ObjectRef

	asset := MediaAsset{
		Object:         confirmed.ObjectRef,
		State:          MediaAssetVerified,
		SizeBytes:      confirmed.SizeBytes,
		SHA256:         processed.SHA256,
		ContentType:    "video/mp4",
		Info:           info,
		ProfileKey:     w.opts.ProfileKey,
		SourceProvider: w.opts.SourceCode,
		ExternalID:     item.ExternalID,
	}
	if _, _, err := w.repo.CommitItemReady(ctx, *item, asset); err != nil {
		return err
	}
	return nil
}

// inheritedMaterial confirms the ready material the revision inherited for
// this task, so the same bytes are not downloaded and processed again.
//
// The material is reused only when the producing task used the same input
// and the same processing profile, and when the stored object still exists
// with the size and the digest the asset records. A material that fails any
// of these checks is processed again from the input: the database row alone
// never decides that an object is usable.
func (w *MediaWorker) inheritedMaterial(ctx context.Context, item MediaItem) (*MediaAsset, bool) {
	inherited, err := w.repo.InheritedAssetForItem(ctx, item)
	if err != nil || inherited == nil {
		return nil, false
	}
	if !sameInput(inherited, item) {
		return nil, false
	}
	if inherited.ProducerProfile != w.opts.ProfileKey {
		return nil, false
	}
	asset := inherited.Asset
	if asset.Object.Key == "" || asset.SHA256 == "" || asset.SizeBytes <= 0 {
		return nil, false
	}
	// The object has to still be there and still has to be the same bytes.
	head, err := w.storage.Head(ctx, asset.Object)
	if err != nil || head == nil {
		return nil, false
	}
	if head.SizeBytes != asset.SizeBytes {
		return nil, false
	}
	if head.MetadataSHA256 == "" || !strings.EqualFold(head.MetadataSHA256, asset.SHA256) {
		return nil, false
	}
	if asset.Object.VersionID != "" && head.VersionID != asset.Object.VersionID {
		return nil, false
	}
	return &asset, true
}

// sameInput reports whether the task is fed by the input the material was
// produced from: the same confirmed upload, or the same source episode.
func sameInput(inherited *InheritedAsset, item MediaItem) bool {
	if item.InputUploadID > 0 {
		return inherited.ProducerInputUploadID == item.InputUploadID
	}
	return inherited.ProducerInputUploadID == 0 && inherited.ProducerExternalID == item.ExternalID
}

// itemInfo returns the technical shape that was stored with a task.
func itemInfo(item MediaItem) *MediaInfo {
	if item.Codec == "" || item.DurationMS <= 0 || item.Width <= 0 || item.Height <= 0 {
		return nil
	}
	return &MediaInfo{
		VideoCodec: item.Codec,
		DurationMS: item.DurationMS,
		Width:      item.Width,
		Height:     item.Height,
	}
}

// uploadOutput stores the processed file. An object with the same content
// identity is reused after it was confirmed, so a resumed task does not
// upload the same bytes twice.
func (w *MediaWorker) uploadOutput(ctx context.Context, item *MediaItem, processed *ProcessedMedia) (ObjectRef, error) {
	// The object of a resumed task is confirmed first: the upload may have
	// succeeded before the database commit failed.
	if item.Object.Key != "" {
		existing, err := w.storage.Head(ctx, item.Object)
		if err == nil && existing != nil {
			if confirmErr := checkStoredObject(existing, processed); confirmErr == nil {
				return existing.ObjectRef, nil
			}
		}
	}
	if err := w.checkpoint(ctx, item, MediaItemUploading); err != nil {
		return ObjectRef{}, err
	}
	key := ObjectKey(w.opts.KeyPrefix, processed.SHA256)
	ref := ObjectRef{Key: key}
	existing, headErr := w.storage.Head(ctx, ref)
	switch {
	case headErr == nil && existing != nil:
		if existing.SizeBytes != processed.SizeBytes {
			return ObjectRef{}, NewMediaError(MediaStageUpload, MediaCodeStorageObjectMismatch, false,
				fmt.Errorf("existing object size %d does not match processed size %d", existing.SizeBytes, processed.SizeBytes))
		}
		if existing.MetadataSHA256 != "" {
			if !strings.EqualFold(existing.MetadataSHA256, processed.SHA256) {
				return ObjectRef{}, NewMediaError(MediaStageUpload, MediaCodeStorageObjectMismatch, false,
					errors.New("existing object metadata hash does not match processed content"))
			}
			item.Object = existing.ObjectRef
			return existing.ObjectRef, nil
		}
		// Missing metadata is repaired by uploading again under the same key.
	case headErr != nil && !isCode(headErr, MediaCodeStorageObjectMissing):
		return ObjectRef{}, classifyMediaError(ctx, MediaStageUpload, headErr)
	}
	stored, err := w.storage.PutFile(ctx, PutObjectRequest{
		Key:         key,
		Path:        processed.Path,
		ContentType: "video/mp4",
		SHA256:      processed.SHA256,
		MD5Base64:   processed.MD5Base64,
		SizeBytes:   processed.SizeBytes,
	})
	if err != nil {
		return ObjectRef{}, classifyMediaError(ctx, MediaStageUpload, err)
	}
	if stored == nil {
		return ObjectRef{}, NewMediaError(MediaStageUpload, MediaCodeStorageUnavailable, true,
			errors.New("storage returned no object"))
	}
	if stored.Key == "" {
		stored.Key = key
	}
	if err := checkStoredObject(stored, processed); err != nil {
		return ObjectRef{}, err
	}
	item.Object = stored.ObjectRef
	return stored.ObjectRef, nil
}

// confirmObject checks the stored object against the local content identity.
// The size, the required metadata and the object identity have to match; a
// mismatch never becomes ready material.
func (w *MediaWorker) confirmObject(ctx context.Context, identity ObjectRef, processed *ProcessedMedia) (*StoredObject, error) {
	confirmed, err := w.storage.Head(ctx, identity)
	if err != nil {
		return nil, classifyMediaError(ctx, MediaStageVerify, err)
	}
	if confirmed == nil {
		return nil, NewMediaError(MediaStageVerify, MediaCodeStorageObjectMissing, true,
			errors.New("object is missing after upload"))
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
	return confirmed, nil
}

// acquireUploadedInput downloads the confirmed manual object and checks it
// against the frozen identity. A session that is not completed, an object of
// the wrong episode or changed bytes is a permanent failure: retrying the same
// input cannot repair it.
func (w *MediaWorker) acquireUploadedInput(ctx context.Context, item *MediaItem, jobDir string) (string, error) {
	if w.uploads == nil {
		return "", NewMediaError(MediaStageAcquire, MediaCodeConfigInvalid, false,
			errors.New("manual input requires the upload repository"))
	}
	if err := w.checkpoint(ctx, item, MediaItemAcquiring); err != nil {
		return "", err
	}
	input, err := w.uploads.GetCompletedInput(ctx, item.InputUploadID)
	if err != nil {
		return "", NewMediaError(MediaStageAcquire, MediaCodeMediaNotFound, false,
			fmt.Errorf("confirmed upload %d is not usable: %w", item.InputUploadID, err))
	}
	if input.Kind != "" && input.Kind != UploadKindEpisode {
		return "", NewMediaError(MediaStageAcquire, MediaCodeMediaNotFound, false,
			fmt.Errorf("upload %d is not an episode input", input.SessionID))
	}
	if input.EpisodeID != item.EpisodeID && !w.sameInheritedEpisode(ctx, item, input) {
		return "", NewMediaError(MediaStageAcquire, MediaCodeMediaNotFound, false,
			fmt.Errorf("upload %d belongs to episode %d, task has %d", input.SessionID, input.EpisodeID, item.EpisodeID))
	}
	// The download is bounded: an object that was replaced with a larger one
	// under the same key fails while it streams.
	limit := w.opts.MaxInputBytes
	if limit <= 0 {
		limit = input.SizeBytes
	}
	downloaded, err := w.storage.GetToFile(ctx, input.Object, jobDir, limit)
	if err != nil {
		return "", classifyMediaError(ctx, MediaStageAcquire, err)
	}
	if downloaded == nil || downloaded.Path == "" || downloaded.SizeBytes <= 0 {
		return "", NewMediaError(MediaStageDownload, MediaCodeDownloadIncomplete, true,
			errors.New("downloaded upload is empty"))
	}
	if input.SizeBytes > 0 && downloaded.SizeBytes != input.SizeBytes {
		return "", NewMediaError(MediaStageAcquire, MediaCodeStorageObjectMismatch, false,
			fmt.Errorf("uploaded object size %d does not match the confirmed %d", downloaded.SizeBytes, input.SizeBytes))
	}
	if input.SHA256 != "" && !strings.EqualFold(input.SHA256, downloaded.SHA256) {
		return "", NewMediaError(MediaStageAcquire, MediaCodeStorageObjectMismatch, false,
			errors.New("uploaded object digest does not match the confirmed input"))
	}
	if w.opts.MaxInputBytes > 0 && downloaded.SizeBytes > w.opts.MaxInputBytes {
		return "", NewMediaError(MediaStageDownload, MediaCodeMediaTooLarge, false,
			fmt.Errorf("input exceeds the configured limit"))
	}
	item.InputArtifactPath = downloaded.Path
	item.InputArtifactSHA256 = downloaded.SHA256
	item.SHA256 = downloaded.SHA256
	item.SizeBytes = downloaded.SizeBytes
	item.ContentType = downloaded.ContentType
	return downloaded.Path, nil
}

// sameInheritedEpisode reports whether a confirmed upload belongs to the same
// episode as the task even though the episode row is another one.
//
// A new revision copies the episodes of its base revision, so the confirmed
// input of a copied episode names the base episode. The session still feeds
// this task when it names the same episode of the same series; the input of
// any other episode is refused.
func (w *MediaWorker) sameInheritedEpisode(ctx context.Context, item *MediaItem, input *CompletedInput) bool {
	if input.SeriesID <= 0 || input.ExternalID == "" {
		return false
	}
	if input.ExternalID != item.ExternalID || input.EpisodeNo != item.EpisodeNo {
		return false
	}
	batch, err := w.repo.GetBatch(ctx, item.BatchID)
	if err != nil || batch == nil {
		return false
	}
	return batch.SeriesID == input.SeriesID
}

// validateInput probes the input file.
func (w *MediaWorker) validateInput(ctx context.Context, item *MediaItem, path string) (*MediaInfo, error) {
	if err := w.checkpoint(ctx, item, MediaItemValidating); err != nil {
		return nil, err
	}
	info, err := w.processor.Probe(ctx, path)
	if err != nil {
		return nil, classifyMediaError(ctx, MediaStageValidate, err)
	}
	if info == nil || !info.Playable() {
		return nil, NewMediaError(MediaStageValidate, MediaCodeMediaInvalid, false,
			errors.New("input has no playable video track"))
	}
	return info, nil
}

// reusableInput reports the downloaded input of a resumed task. The artifact
// is only reused when it exists and still has the recorded digest.
func (w *MediaWorker) reusableInput(item MediaItem) (string, string, bool) {
	if item.InputArtifactPath == "" || item.InputArtifactSHA256 == "" {
		return "", "", false
	}
	if !artifactMatches(item.InputArtifactPath, item.InputArtifactSHA256, 0) {
		return "", "", false
	}
	return item.InputArtifactPath, item.InputArtifactSHA256, true
}

// reusableOutput reports the processed file of a resumed task.
func (w *MediaWorker) reusableOutput(item MediaItem) (*ProcessedMedia, bool) {
	if item.OutputArtifactPath == "" || item.OutputArtifactSHA256 == "" {
		return nil, false
	}
	if !artifactMatches(item.OutputArtifactPath, item.OutputArtifactSHA256, item.OutputArtifactSize) {
		return nil, false
	}
	return &ProcessedMedia{
		Path:      item.OutputArtifactPath,
		SHA256:    item.OutputArtifactSHA256,
		SizeBytes: item.OutputArtifactSize,
	}, true
}

// artifactMatches reports whether a persisted artifact can be reused. The
// file has to exist and its digest has to match the recorded one.
func artifactMatches(path string, sha256 string, size int64) bool {
	if path == "" || sha256 == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if size > 0 && info.Size() != size {
		return false
	}
	digest, err := fileDigest(path)
	if err != nil {
		return false
	}
	return strings.EqualFold(digest, sha256)
}

// fileDigest returns the sha256 of a file.
func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// renewLease extends the lease of a running task. When the lease was taken
// over by another worker the task is cancelled: its writes would be
// rejected anyway.
func (w *MediaWorker) renewLease(ctx context.Context, item MediaItem, cancel context.CancelFunc) {
	interval := w.opts.Lease / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.repo.RenewItemLease(ctx, item, w.opts.Lease); err != nil {
				if errors.Is(err, ErrItemLeaseLost) {
					w.logger.Info("media item lease lost", "item", item.ID)
					cancel()
					return
				}
				w.logger.Warn("media lease renewal failed", "item", item.ID, "err", err)
			}
		}
	}
}

// checkpoint persists the stage the item reached.
func (w *MediaWorker) checkpoint(ctx context.Context, item *MediaItem, status MediaItemStatus) error {
	item.Status = status
	// The line names the task, the batch, the stage, the attempt and the
	// lease generation, so one attempt can be told from the next one and
	// from a run another worker took over.
	w.logger.InfoContext(ctx, "media item stage",
		"batch", item.BatchID,
		"item", item.ID,
		"episode", item.EpisodeNo,
		"stage", string(status),
		"attempt", item.Attempt,
		"lease_generation", item.LeaseGeneration,
	)
	saved, err := w.repo.SaveItem(ctx, *item)
	if err != nil {
		return err
	}
	if saved != nil {
		*item = *saved
	}
	return nil
}

// failItem records a classified failure on the item.
func (w *MediaWorker) failItem(ctx context.Context, item MediaItem, err error) error {
	code, ok := MediaErrorCodeOf(err)
	if !ok {
		code = MediaCodeProcessFailed
	}
	retryable := Retryable(err)
	message := sanitizeMediaError(err)
	nextEligible := (*time.Time)(nil)
	if retryable {
		at := time.Now().Add(w.opts.RetryBackoff)
		nextEligible = &at
	}
	if _, failErr := w.repo.FailItem(ctx, item, code, message, retryable, nextEligible); failErr != nil {
		return failErr
	}
	return err
}

// jobDir returns the stable working directory of one item. The directory is
// not removed between attempts, so a resumed task finds its artifacts.
func (w *MediaWorker) jobDir(item MediaItem) (string, error) {
	root := w.opts.TempDir
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create media temp root: %w", err)
	}
	dir := filepath.Join(root, fmt.Sprintf("media-item-%d", item.ID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create media job directory: %w", err)
	}
	return dir, nil
}

// cleanupJobDir removes the working directory of a finished task.
func (w *MediaWorker) cleanupJobDir(dir string) {
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		w.logger.Warn("media job directory cleanup failed", "dir", dir, "err", err)
	}
}

// sanitizeMediaError keeps the operator-facing message short and free of
// signed addresses or credentials.
func sanitizeMediaError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
