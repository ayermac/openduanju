package biz_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"openduanju-server/internal/biz"
)

// recordedFailure is one failure the worker wrote to a task.
type recordedFailure struct {
	code           biz.MediaErrorCode
	retryable      bool
	nextEligibleAt *time.Time
}

// recordingBatchRepo records the checkpoints of one worker run.
type recordingBatchRepo struct {
	claimed   []biz.MediaItem
	saved     []biz.MediaItemStatus
	committed []biz.MediaAsset
	failures  []recordedFailure
	renewals  int
	leaseLost bool
	commitErr error
}

func (f *recordingBatchRepo) FreezeAndCreateBatch(context.Context, biz.FreezeBatchRequest) (*biz.MediaBatch, error) {
	return nil, nil
}

func (f *recordingBatchRepo) GetBatch(context.Context, int64) (*biz.MediaBatch, error) {
	return nil, nil
}

func (f *recordingBatchRepo) GetBatchByRevision(context.Context, int64) (*biz.MediaBatch, error) {
	return nil, nil
}

func (f *recordingBatchRepo) ListBatches(context.Context, biz.BatchQuery) ([]biz.MediaBatch, int, error) {
	return nil, 0, nil
}

func (f *recordingBatchRepo) ListItems(context.Context, biz.ItemQuery) ([]biz.MediaItem, int, error) {
	return nil, 0, nil
}

func (f *recordingBatchRepo) GetItem(context.Context, int64) (*biz.MediaItem, error) {
	return nil, nil
}

func (f *recordingBatchRepo) ClaimItems(context.Context, string, time.Duration, int) ([]biz.MediaItem, error) {
	claimed := f.claimed
	f.claimed = nil
	return claimed, nil
}

func (f *recordingBatchRepo) SaveItem(_ context.Context, item biz.MediaItem) (*biz.MediaItem, error) {
	f.saved = append(f.saved, item.Status)
	saved := item
	return &saved, nil
}

func (f *recordingBatchRepo) RenewItemLease(_ context.Context, item biz.MediaItem, _ time.Duration) (*biz.MediaItem, error) {
	if f.leaseLost {
		return nil, biz.ErrItemLeaseLost
	}
	f.renewals++
	renewed := item
	return &renewed, nil
}

func (f *recordingBatchRepo) CommitItemReady(_ context.Context, item biz.MediaItem, asset biz.MediaAsset) (*biz.MediaItem, *biz.MediaBatch, error) {
	if f.commitErr != nil {
		return nil, nil, f.commitErr
	}
	f.committed = append(f.committed, asset)
	ready := item
	ready.Status = biz.MediaItemReady
	return &ready, nil, nil
}

func (f *recordingBatchRepo) FailItem(_ context.Context, item biz.MediaItem, code biz.MediaErrorCode, message string, retryable bool, nextEligibleAt *time.Time) (*biz.MediaItem, error) {
	f.failures = append(f.failures, recordedFailure{code: code, retryable: retryable, nextEligibleAt: nextEligibleAt})
	failed := item
	failed.Status = biz.MediaItemFailed
	return &failed, nil
}

func (f *recordingBatchRepo) RequestCancel(context.Context, int64, int, string) (*biz.MediaBatch, error) {
	return nil, nil
}

func (f *recordingBatchRepo) RetryItems(context.Context, int64, int, []int64) (*biz.MediaBatch, error) {
	return nil, nil
}

func (f *recordingBatchRepo) RefreshBatchStatus(context.Context, int64) (*biz.MediaBatch, error) {
	return nil, nil
}

func (f *recordingBatchRepo) RefreshReadyBatches(context.Context, int) (int, error) {
	return 0, nil
}

func (f *recordingBatchRepo) InheritedAssetForItem(context.Context, biz.MediaItem) (*biz.InheritedAsset, error) {
	return nil, nil
}

func (f *recordingBatchRepo) EnsureBatchAuthorization(context.Context, int64, biz.Permission) error {
	return nil
}

func workerOptions(t *testing.T) biz.MediaWorkerOptions {
	t.Helper()
	return biz.MediaWorkerOptions{
		Worker:       "worker-a",
		Lease:        time.Minute,
		BatchSize:    1,
		PollInterval: time.Second,
		SourceCode:   "hongguo",
		ProfileKey:   "web-mp4-h264-aac-v1",
		KeyPrefix:    "media-test",
		TempDir:      t.TempDir(),
		RetryBackoff: time.Minute,
	}
}

func queuedItem() biz.MediaItem {
	return biz.MediaItem{ID: 1, BatchID: 9, EpisodeID: 100, ExternalID: "ep-1", EpisodeNo: 1, Status: biz.MediaItemQueued}
}

func TestMediaWorkerReachesReadyThroughEveryCheckpoint(t *testing.T) {
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{queuedItem()}}
	storage := newFakeStorage()
	worker, err := biz.NewMediaWorker(repo,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()},
		storage, workerOptions(t), nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}

	handled, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want 1", handled)
	}
	want := []biz.MediaItemStatus{
		biz.MediaItemResolving, biz.MediaItemAcquiring, biz.MediaItemValidating,
		biz.MediaItemProcessing, biz.MediaItemUploading, biz.MediaItemVerifying,
	}
	if len(repo.saved) != len(want) {
		t.Fatalf("checkpoints = %v, want %v", repo.saved, want)
	}
	for i, status := range want {
		if repo.saved[i] != status {
			t.Fatalf("checkpoint %d = %s, want %s", i, repo.saved[i], status)
		}
	}
	if len(repo.committed) != 1 {
		t.Fatalf("committed = %d, want 1", len(repo.committed))
	}
	asset := repo.committed[0]
	if asset.SHA256 != digestOf(processedContent) {
		t.Fatalf("asset sha256 = %q, want the processed digest", asset.SHA256)
	}
	if asset.ExternalID != "ep-1" {
		t.Fatalf("asset external id = %q, want the episode of the task", asset.ExternalID)
	}
	if len(repo.failures) != 0 {
		t.Fatalf("failures = %v, want none", repo.failures)
	}
}

func TestMediaWorkerRecordsARetryableFailure(t *testing.T) {
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{queuedItem()}}
	worker, err := biz.NewMediaWorker(repo,
		&fakeResolver{err: errors.New("source is down")},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()},
		newFakeStorage(), workerOptions(t), nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}

	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if len(repo.failures) != 1 {
		t.Fatalf("failures = %v, want one recorded failure", repo.failures)
	}
	failure := repo.failures[0]
	if failure.code != biz.MediaCodeSourceUnavailable {
		t.Fatalf("code = %s, want %s", failure.code, biz.MediaCodeSourceUnavailable)
	}
	if !failure.retryable {
		t.Fatal("an unreachable source must stay retryable")
	}
	if failure.nextEligibleAt == nil {
		t.Fatal("a retryable failure must schedule the next attempt")
	}
	if len(repo.committed) != 0 {
		t.Fatal("a failed task must not be committed as ready")
	}
}

func TestMediaWorkerStopsOnAPermanentFailure(t *testing.T) {
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{queuedItem()}}
	worker, err := biz.NewMediaWorker(repo,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: &biz.MediaInfo{Container: "mp3", DurationMS: 30_000}},
		newFakeStorage(), workerOptions(t), nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}

	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if len(repo.failures) != 1 {
		t.Fatalf("failures = %v, want one recorded failure", repo.failures)
	}
	failure := repo.failures[0]
	if failure.code != biz.MediaCodeMediaInvalid {
		t.Fatalf("code = %s, want %s", failure.code, biz.MediaCodeMediaInvalid)
	}
	if failure.retryable {
		t.Fatal("an unplayable input must not be retried forever")
	}
	if failure.nextEligibleAt != nil {
		t.Fatal("a permanent failure must not schedule a retry")
	}
}

func TestMediaWorkerRequiresItsCollaborators(t *testing.T) {
	if _, err := biz.NewMediaWorker(nil, nil, nil, &fakeProcessor{}, newFakeStorage(), workerOptions(t), nil); err == nil {
		t.Fatal("a worker without a repository must fail")
	}
	if _, err := biz.NewMediaWorker(&recordingBatchRepo{}, nil, nil, nil, newFakeStorage(), workerOptions(t), nil); err == nil {
		t.Fatal("a worker without a processor must fail")
	}
	if _, err := biz.NewMediaWorker(&recordingBatchRepo{}, nil, nil, &fakeProcessor{}, nil, workerOptions(t), nil); err == nil {
		t.Fatal("a worker without object storage must fail")
	}
}

// inputArtifact writes the downloaded input of a task and returns its
// checkpoint fields.
func inputArtifact(t *testing.T, dir string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(path, []byte("source-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, digestOf("source-bytes")
}

// processedArtifact writes the processed file of a task and returns its
// checkpoint fields.
func processedArtifact(t *testing.T, dir string, content string) (string, string, int64) {
	t.Helper()
	path := filepath.Join(dir, "processed.mp4")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, digestOf(content), int64(len(content))
}

func TestMediaWorkerResumesFromThePersistedOutput(t *testing.T) {
	options := workerOptions(t)
	jobDir := filepath.Join(options.TempDir, "media-item-1")
	if err := os.MkdirAll(jobDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path, digest, size := processedArtifact(t, jobDir, processedContent)
	item := queuedItem()
	item.Status = biz.MediaItemProcessing
	item.OutputArtifactPath = path
	item.OutputArtifactSHA256 = digest
	item.OutputArtifactSize = size
	item.Codec = "h264"
	item.DurationMS = 65_000
	item.Width = 1080
	item.Height = 1920

	repo := &recordingBatchRepo{claimed: []biz.MediaItem{item}}
	resolver := &fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}}
	downloader := &fakeDownloader{file: downloadFile(t, t.TempDir())}
	processor := &fakeProcessor{info: playableInfo()}
	storage := newFakeStorage()
	worker, err := biz.NewMediaWorker(repo, resolver, downloader, processor, storage, options, nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}

	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want none: the task resumes after the resolve", resolver.calls)
	}
	if downloader.calls != 0 {
		t.Fatalf("downloader calls = %d, want none: the processed file needs no input", downloader.calls)
	}
	if processor.prepares != 0 {
		t.Fatalf("prepares = %d, want none: the processed file is reused", processor.prepares)
	}
	if len(repo.committed) != 1 {
		t.Fatalf("committed = %d, want 1", len(repo.committed))
	}
}

func TestMediaWorkerResumesFromThePersistedInput(t *testing.T) {
	options := workerOptions(t)
	jobDir := filepath.Join(options.TempDir, "media-item-1")
	if err := os.MkdirAll(jobDir, 0o750); err != nil {
		t.Fatal(err)
	}
	inputPath, inputDigest := inputArtifact(t, jobDir)
	item := queuedItem()
	item.Status = biz.MediaItemValidating
	item.InputArtifactPath = inputPath
	item.InputArtifactSHA256 = inputDigest

	repo := &recordingBatchRepo{claimed: []biz.MediaItem{item}}
	resolver := &fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}}
	downloader := &fakeDownloader{file: downloadFile(t, t.TempDir())}
	processor := &fakeProcessor{info: playableInfo()}
	worker, err := biz.NewMediaWorker(repo, resolver, downloader, processor, newFakeStorage(), options, nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if resolver.calls != 0 || downloader.calls != 0 {
		t.Fatalf("the input is reused: resolves=%d downloads=%d", resolver.calls, downloader.calls)
	}
	if processor.prepares != 1 {
		t.Fatalf("prepares = %d, want 1: the processed file is missing", processor.prepares)
	}
	if len(repo.committed) != 1 {
		t.Fatalf("committed = %d, want 1", len(repo.committed))
	}
}

func TestMediaWorkerRedoesAnArtifactThatChanged(t *testing.T) {
	options := workerOptions(t)
	jobDir := filepath.Join(options.TempDir, "media-item-1")
	if err := os.MkdirAll(jobDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path, _, size := processedArtifact(t, jobDir, "a different file")
	item := queuedItem()
	item.Status = biz.MediaItemProcessing
	item.OutputArtifactPath = path
	item.OutputArtifactSHA256 = digestOf(processedContent)
	item.OutputArtifactSize = size

	repo := &recordingBatchRepo{claimed: []biz.MediaItem{item}}
	downloader := &fakeDownloader{file: downloadFile(t, t.TempDir())}
	processor := &fakeProcessor{info: playableInfo()}
	worker, err := biz.NewMediaWorker(repo,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		downloader, processor, newFakeStorage(), options, nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if processor.prepares != 1 {
		t.Fatalf("prepares = %d, want 1: a changed artifact is redone", processor.prepares)
	}
	if len(repo.committed) != 1 {
		t.Fatalf("committed = %d, want 1", len(repo.committed))
	}
}

func TestMediaWorkerConfirmsAnObjectThatWasUploadedBefore(t *testing.T) {
	options := workerOptions(t)
	jobDir := filepath.Join(options.TempDir, "media-item-1")
	if err := os.MkdirAll(jobDir, 0o750); err != nil {
		t.Fatal(err)
	}
	path, digest, size := processedArtifact(t, jobDir, processedContent)
	inputPath, inputDigest := inputArtifact(t, jobDir)
	item := queuedItem()
	item.Status = biz.MediaItemVerifying
	item.InputArtifactPath = inputPath
	item.InputArtifactSHA256 = inputDigest
	item.OutputArtifactPath = path
	item.OutputArtifactSHA256 = digest
	item.OutputArtifactSize = size
	item.Codec = "h264"
	item.DurationMS = 65_000
	item.Width = 1080
	item.Height = 1920

	storage := newFakeStorage()
	key := biz.ObjectKey(options.KeyPrefix, digest)
	storage.addVersion(&biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: key},
		SizeBytes:      size,
		MetadataSHA256: digest,
		ContentType:    "video/mp4",
	})
	item.Object = biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: key}

	repo := &recordingBatchRepo{claimed: []biz.MediaItem{item}}
	downloader := &fakeDownloader{file: downloadFile(t, t.TempDir())}
	processor := &fakeProcessor{info: playableInfo()}
	worker, err := biz.NewMediaWorker(repo,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		downloader, processor, storage, options, nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if storage.puts != 0 {
		t.Fatalf("puts = %d, want none: the stored object is confirmed again", storage.puts)
	}
	if downloader.calls != 0 || processor.prepares != 0 {
		t.Fatalf("the task must not download or process again: downloads=%d prepares=%d", downloader.calls, processor.prepares)
	}
	if len(repo.committed) != 1 {
		t.Fatalf("committed = %d, want 1", len(repo.committed))
	}
}

func TestMediaWorkerRejectsAnUnconfirmedObject(t *testing.T) {
	cases := []struct {
		name    string
		storage func() *fakeStorage
	}{
		{
			name: "object without the sha256 metadata",
			storage: func() *fakeStorage {
				storage := newFakeStorage()
				storage.dropMetadata = true
				return storage
			},
		},
		{
			name: "object with a different size",
			storage: func() *fakeStorage {
				storage := newFakeStorage()
				storage.sizeOverride = 7
				return storage
			},
		},
		{
			name: "object with a drifting version",
			storage: func() *fakeStorage {
				storage := newFakeStorage()
				storage.headDrift = "v-other"
				return storage
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &recordingBatchRepo{claimed: []biz.MediaItem{queuedItem()}}
			storage := tc.storage()
			worker, err := biz.NewMediaWorker(repo,
				&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
				&fakeDownloader{file: downloadFile(t, t.TempDir())},
				&fakeProcessor{info: playableInfo()},
				storage, workerOptions(t), nil)
			if err != nil {
				t.Fatalf("NewMediaWorker: %v", err)
			}
			if _, err := worker.ProcessOnce(context.Background()); err != nil {
				t.Fatalf("ProcessOnce: %v", err)
			}
			if len(repo.committed) != 0 {
				t.Fatal("an unconfirmed object must not become ready material")
			}
			if len(repo.failures) != 1 {
				t.Fatalf("failures = %v, want one recorded failure", repo.failures)
			}
			if repo.failures[0].code != biz.MediaCodeStorageObjectMismatch {
				t.Fatalf("code = %s, want %s", repo.failures[0].code, biz.MediaCodeStorageObjectMismatch)
			}
		})
	}
}

func TestMediaWorkerRenewsTheLeaseWhileItRuns(t *testing.T) {
	options := workerOptions(t)
	options.Lease = 120 * time.Millisecond
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{queuedItem()}}
	worker, err := biz.NewMediaWorker(repo,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir()), delay: 150 * time.Millisecond},
		&fakeProcessor{info: playableInfo()},
		newFakeStorage(), options, nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if repo.renewals == 0 {
		t.Fatal("a running task must renew its lease")
	}
}

// fakeInputReader returns one confirmed manual input.
type fakeInputReader struct {
	input *biz.CompletedInput
	err   error
	calls int
}

func (f *fakeInputReader) GetCompletedInput(context.Context, int64) (*biz.CompletedInput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.input, nil
}

// manualFixture stages one uploaded object and the matching input record.
func manualFixture(t *testing.T, body []byte) (*fakeStorage, *fakeInputReader, *biz.MediaItem) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "uploaded.mp4")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	storage := newFakeStorage()
	storage.addVersion(&biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: "uploads/manual", VersionID: "v1"},
		SizeBytes:      int64(len(body)),
		MetadataSHA256: digestOf(string(body)),
		ContentType:    "video/mp4",
	})
	storage.files = map[string]string{"uploads/manual": path}
	reader := &fakeInputReader{input: &biz.CompletedInput{
		SessionID: 42,
		EpisodeID: 7,
		Kind:      biz.UploadKindEpisode,
		Object:    biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: "uploads/manual"},
		SizeBytes: int64(len(body)),
		SHA256:    digestOf(string(body)),
		Status:    biz.UploadCompleted,
	}}
	item := &biz.MediaItem{
		ID:              1,
		BatchID:         2,
		EpisodeID:       7,
		ExternalID:      "ep-7",
		Status:          biz.MediaItemQueued,
		InputUploadID:   42,
		LeaseGeneration: 1,
		LeaseOwner:      "worker-a",
	}
	return storage, reader, item
}

func TestMediaWorkerManualInputRunsWithoutResolver(t *testing.T) {
	body := []byte("manually uploaded episode bytes")
	storage, reader, item := manualFixture(t, body)
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{*item}}
	processor := &fakeProcessor{info: playableInfo()}
	// No resolver and no downloader: a manual task must not need them.
	worker, err := biz.NewMediaWorker(repo, nil, nil, processor, storage, workerOptions(t), nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	worker = worker.WithUploads(reader)
	handled, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if handled != 1 || len(repo.committed) != 1 {
		t.Fatalf("handled=%d committed=%d", handled, len(repo.committed))
	}
	if reader.calls != 1 {
		t.Fatalf("input read %d times", reader.calls)
	}
	// The task starts at acquiring: the bytes are already uploaded.
	if len(repo.saved) == 0 || repo.saved[0] != biz.MediaItemAcquiring {
		t.Fatalf("first checkpoint: %v", repo.saved)
	}
	if processor.prepares != 1 {
		t.Fatalf("prepares: %d", processor.prepares)
	}
}

func TestMediaWorkerManualInputRejectsChangedBytes(t *testing.T) {
	body := []byte("manually uploaded episode bytes")
	storage, reader, item := manualFixture(t, body)
	reader.input.SHA256 = digestOf("different bytes")
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{*item}}
	worker, err := biz.NewMediaWorker(repo, nil, nil, &fakeProcessor{info: playableInfo()}, storage, workerOptions(t), nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	worker = worker.WithUploads(reader)
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if len(repo.committed) != 0 {
		t.Fatal("changed bytes reached ready")
	}
	if len(repo.failures) != 1 {
		t.Fatalf("failures: %d", len(repo.failures))
	}
	if repo.failures[0].retryable {
		t.Fatal("a digest mismatch was marked retryable")
	}
	if repo.failures[0].code != biz.MediaCodeStorageObjectMismatch {
		t.Fatalf("failure code: %s", repo.failures[0].code)
	}
}

func TestMediaWorkerManualInputNeedsAReader(t *testing.T) {
	body := []byte("manually uploaded episode bytes")
	storage, _, item := manualFixture(t, body)
	repo := &recordingBatchRepo{claimed: []biz.MediaItem{*item}}
	worker, err := biz.NewMediaWorker(repo, nil, nil, &fakeProcessor{info: playableInfo()}, storage, workerOptions(t), nil)
	if err != nil {
		t.Fatalf("NewMediaWorker: %v", err)
	}
	// Without the reader the task fails as a configuration error instead of
	// silently resolving the source.
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if len(repo.committed) != 0 {
		t.Fatal("a task without an input reader reached ready")
	}
	if len(repo.failures) != 1 || repo.failures[0].code != biz.MediaCodeConfigInvalid {
		t.Fatalf("failures: %+v", repo.failures)
	}
}
