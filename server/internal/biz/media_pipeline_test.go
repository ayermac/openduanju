package biz_test

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openduanju-server/internal/biz"
)

const processedContent = "normalized-media-bytes"

type fakeResolver struct {
	src   *biz.MediaSource
	err   error
	calls int
}

func (f *fakeResolver) Resolve(context.Context, biz.ResolveRequest) (*biz.MediaSource, error) {
	f.calls++
	return f.src, f.err
}

type fakeDownloader struct {
	file  *biz.DownloadedFile
	err   error
	calls int
	// delay simulates a slow transfer.
	delay time.Duration
}

func (f *fakeDownloader) Download(ctx context.Context, _ *biz.MediaSource, _ string) (*biz.DownloadedFile, error) {
	f.calls++
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return f.file, f.err
}

type fakeProcessor struct {
	info       *biz.MediaInfo
	prepareErr error
	probeErr   error
	probes     int
	prepares   int
	outputDir  string
}

func (f *fakeProcessor) Probe(context.Context, string) (*biz.MediaInfo, error) {
	f.probes++
	return f.info, f.probeErr
}

func (f *fakeProcessor) Prepare(_ context.Context, req biz.ProcessRequest) (*biz.ProcessedMedia, error) {
	f.prepares++
	f.outputDir = req.OutputDir
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	path := filepath.Join(req.OutputDir, "processed.mp4")
	if err := os.WriteFile(path, []byte(processedContent), 0o600); err != nil {
		return nil, err
	}
	return &biz.ProcessedMedia{
		Path:       path,
		SHA256:     digestOf(processedContent),
		MD5Base64:  md5Base64Of(processedContent),
		SizeBytes:  int64(len(processedContent)),
		Info:       f.info,
		Remuxed:    true,
		ProfileKey: req.ProfileKey,
	}, nil
}

// fakeStorage models a versioned bucket: every PutFile appends a new version
// and Head returns the requested version instead of the latest one.
type fakeStorage struct {
	versions     map[string][]*biz.StoredObject
	putErr       error
	headErr      error
	skipStore    bool
	dropMetadata bool
	headDrift    string
	sizeOverride int64
	puts         int
	heads        int
	nextVersion  int
	signTTLSeen  time.Duration
	signedRef    biz.ObjectRef
	// files maps an object key to a local file that GetToFile copies. It
	// lets a test prove server-side byte verification.
	files map[string]string
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{versions: map[string][]*biz.StoredObject{}}
}

func (f *fakeStorage) addVersion(obj *biz.StoredObject) {
	f.nextVersion++
	obj.VersionID = fmt.Sprintf("v%d", f.nextVersion)
	f.versions[obj.Key] = append(f.versions[obj.Key], obj)
}

func (f *fakeStorage) latest(key string) *biz.StoredObject {
	list := f.versions[key]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func (f *fakeStorage) PutFile(_ context.Context, req biz.PutObjectRequest) (*biz.StoredObject, error) {
	f.puts++
	if f.putErr != nil {
		return nil, f.putErr
	}
	obj := &biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: req.Key},
		SizeBytes:      req.SizeBytes,
		MetadataSHA256: req.SHA256,
		ContentType:    req.ContentType,
	}
	if f.dropMetadata {
		obj.MetadataSHA256 = ""
	}
	if f.sizeOverride > 0 {
		obj.SizeBytes = f.sizeOverride
	}
	if !f.skipStore {
		f.addVersion(obj)
	}
	copied := *obj
	return &copied, nil
}

func (f *fakeStorage) Head(_ context.Context, ref biz.ObjectRef) (*biz.StoredObject, error) {
	f.heads++
	if f.headErr != nil {
		return nil, f.headErr
	}
	var obj *biz.StoredObject
	if ref.VersionID == "" {
		obj = f.latest(ref.Key)
	} else {
		for _, candidate := range f.versions[ref.Key] {
			if candidate.VersionID == ref.VersionID {
				obj = candidate
				break
			}
		}
	}
	if obj == nil {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMissing, false, errors.New("missing"))
	}
	copied := *obj
	if f.headDrift != "" {
		copied.VersionID = f.headDrift
	}
	return &copied, nil
}

func (f *fakeStorage) PresignGet(_ context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.SignedAccess, error) {
	f.signTTLSeen = ttl
	f.signedRef = ref
	return &biz.SignedAccess{URL: "https://cos.example/signed?versionId=" + ref.VersionID, ExpiresAt: time.Now().Add(ttl), Object: ref}, nil
}

func (f *fakeStorage) PresignPut(_ context.Context, req biz.PresignPutRequest) (*biz.PresignedUpload, error) {
	return &biz.PresignedUpload{
		URL:       "https://upload.example/" + req.Object.Key,
		Method:    "PUT",
		ExpiresAt: time.Now().Add(req.TTL),
	}, nil
}

func (f *fakeStorage) PlaybackURL(_ context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.PresignedDownload, error) {
	return &biz.PresignedDownload{URL: "/play/" + ref.Key, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (f *fakeStorage) PreviewURL(_ context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.PresignedDownload, error) {
	return &biz.PresignedDownload{URL: "/preview/" + ref.Key, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (f *fakeStorage) GetToFile(_ context.Context, ref biz.ObjectRef, dir string, maxBytes int64) (*biz.DownloadedFile, error) {
	obj := f.latest(ref.Key)
	if obj == nil {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMissing, false, errors.New("missing"))
	}
	if f.files != nil {
		if source, ok := f.files[ref.Key]; ok {
			body, err := os.ReadFile(source)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, err
			}
			path := filepath.Join(dir, "staged-object")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				return nil, err
			}
			if maxBytes > 0 && int64(len(body)) > maxBytes {
				return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeMediaTooLarge, false, errors.New("object exceeds the limit"))
			}
			return &biz.DownloadedFile{
				Path:        path,
				SizeBytes:   int64(len(body)),
				SHA256:      digestOf(string(body)),
				ContentType: obj.ContentType,
			}, nil
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "fake-object")
	if err := os.WriteFile(path, []byte(obj.Key), 0o600); err != nil {
		return nil, err
	}
	return &biz.DownloadedFile{Path: path, SizeBytes: obj.SizeBytes, SHA256: obj.MetadataSHA256}, nil
}

func playableInfo() *biz.MediaInfo {
	return &biz.MediaInfo{
		Container:  "mov,mp4,m4a,3gp,3g2,mj2",
		DurationMS: 65_000,
		VideoCodec: "h264",
		AudioCodec: "aac",
		Width:      1080,
		Height:     1920,
		SizeBytes:  12_345,
	}
}

func downloadFile(t *testing.T, dir string) *biz.DownloadedFile {
	t.Helper()
	path := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(path, []byte("source-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &biz.DownloadedFile{Path: path, SizeBytes: 12, SHA256: digestOf("source-bytes"), ContentType: "video/mp4"}
}

func digestOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func md5Base64Of(value string) string {
	sum := md5.Sum([]byte(value))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func testOptions(t *testing.T) biz.PipelineOptions {
	t.Helper()
	return biz.PipelineOptions{
		TempDir:        t.TempDir(),
		ProfileKey:     "web-mp4-h264-aac-v1",
		KeyPrefix:      "media-test",
		MaxInputBytes:  1 << 20,
		MaxOutputBytes: 1 << 20,
	}
}

func newTestPipeline(t *testing.T, resolver biz.Resolver, downloader biz.Downloader, processor biz.MediaProcessor, storage biz.ObjectStorage, opts biz.PipelineOptions) *biz.Pipeline {
	t.Helper()
	pipeline, err := biz.NewPipeline(resolver, downloader, processor, storage, opts, nil)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return pipeline
}

func TestPipelineRunVerifiesObject(t *testing.T) {
	dir := t.TempDir()
	storage := newFakeStorage()
	resolver := &fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}}
	downloader := &fakeDownloader{file: downloadFile(t, dir)}
	processor := &fakeProcessor{info: playableInfo()}
	pipeline := newTestPipeline(t, resolver, downloader, processor, storage, testOptions(t))

	result, err := pipeline.Run(context.Background(), biz.RunInput{
		SourceCode:        "hongguo",
		ExternalEpisodeID: "episode-1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Asset == nil || result.Asset.State != biz.MediaAssetVerified {
		t.Fatalf("asset = %+v, want verified", result.Asset)
	}
	wantKey := "media-test/" + result.Asset.SHA256[:2] + "/" + result.Asset.SHA256 + ".mp4"
	if result.Asset.Object.Key != wantKey {
		t.Fatalf("key = %q, want %q", result.Asset.Object.Key, wantKey)
	}
	if storage.puts != 1 {
		t.Fatalf("puts = %d, want 1", storage.puts)
	}
	if storage.heads != 2 {
		t.Fatalf("heads = %d, want 2 (existence pre-check + confirmation)", storage.heads)
	}
	if len(result.Stages) != 6 {
		t.Fatalf("stages = %d, want 6", len(result.Stages))
	}
	wantStages := []biz.MediaStage{
		biz.MediaStageAcquire, biz.MediaStageDownload, biz.MediaStageValidate,
		biz.MediaStageProcess, biz.MediaStageUpload, biz.MediaStageVerify,
	}
	for i, stage := range wantStages {
		if result.Stages[i].Stage != stage {
			t.Fatalf("stage %d = %s, want %s", i, result.Stages[i].Stage, stage)
		}
	}
	if result.Asset.DownloadedHash != digestOf("source-bytes") {
		t.Fatalf("downloaded hash not recorded")
	}
}

func TestPipelineRejectsUnplayableInput(t *testing.T) {
	storage := newFakeStorage()
	processor := &fakeProcessor{info: &biz.MediaInfo{Container: "mp3", DurationMS: 30_000}}
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())}, processor, storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaInvalid {
		t.Fatalf("code = %s, want %s (%v)", code, biz.MediaCodeMediaInvalid, err)
	}
	if processor.prepares != 0 || storage.puts != 0 {
		t.Fatalf("unplayable input reached processing/upload")
	}
}

func TestPipelineRejectsEmptyProbeResult(t *testing.T) {
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: nil}, newFakeStorage(), testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaInvalid {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeMediaInvalid)
	}
}

func TestPipelineStopsWhenResolverRejectsProtectedMedia(t *testing.T) {
	storage := newFakeStorage()
	downloader := &fakeDownloader{file: downloadFile(t, t.TempDir())}
	processor := &fakeProcessor{info: playableInfo()}
	resolver := &fakeResolver{err: biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeMediaProtected, false, errors.New("drm"))}
	pipeline := newTestPipeline(t, resolver, downloader, processor, storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaProtected {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeMediaProtected)
	}
	if downloader.calls != 0 || processor.prepares != 0 || storage.puts != 0 {
		t.Fatalf("protected media continued through the pipeline")
	}
}

func TestPipelineFailsWhenObjectMissingAfterUpload(t *testing.T) {
	storage := newFakeStorage()
	storage.skipStore = true
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()},
		storage, testOptions(t))

	result, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMissing {
		t.Fatalf("code = %s, want %s (%v)", code, biz.MediaCodeStorageObjectMissing, err)
	}
	if result != nil {
		t.Fatalf("result must be nil when confirmation fails")
	}
}

func TestPipelineReusesConfirmedObject(t *testing.T) {
	storage := newFakeStorage()
	sha := digestOf(processedContent)
	key := biz.ObjectKey("media-test", sha)
	storage.addVersion(&biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Key: key},
		SizeBytes:      int64(len(processedContent)),
		MetadataSHA256: sha,
	})
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()},
		storage, testOptions(t))

	result, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if storage.puts != 0 {
		t.Fatalf("puts = %d, want 0 for an already confirmed object", storage.puts)
	}
	if result.Asset == nil || result.Asset.Object.Key != key {
		t.Fatalf("asset = %+v", result.Asset)
	}
	if result.Asset.Object.VersionID != "v1" {
		t.Fatalf("reused version = %q, want v1", result.Asset.Object.VersionID)
	}
}

func TestPipelineRejectsObjectSizeMismatch(t *testing.T) {
	storage := newFakeStorage()
	sha := digestOf(processedContent)
	key := biz.ObjectKey("media-test", sha)
	storage.addVersion(&biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Key: key},
		SizeBytes:      1,
		MetadataSHA256: sha,
	})
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()},
		storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMismatch {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeStorageObjectMismatch)
	}
}

func TestPipelineRequiresExactlyOneInput(t *testing.T) {
	pipeline := newTestPipeline(t, &fakeResolver{}, &fakeDownloader{}, &fakeProcessor{info: playableInfo()}, newFakeStorage(), testOptions(t))
	if _, err := pipeline.Run(context.Background(), biz.RunInput{}); err == nil {
		t.Fatal("empty input must fail")
	}
	if _, err := pipeline.Run(context.Background(), biz.RunInput{
		SourceCode:        "hongguo",
		ExternalEpisodeID: "episode-1",
		LocalInputPath:    "/tmp/file.mp4",
	}); err == nil {
		t.Fatal("both inputs must fail")
	}
	if _, err := pipeline.Run(context.Background(), biz.RunInput{ExternalEpisodeID: "episode-1"}); err == nil {
		t.Fatal("missing source code must fail")
	}
}

func TestPipelineRejectsExternalRunWithoutResolver(t *testing.T) {
	pipeline := newTestPipeline(t, nil, nil, &fakeProcessor{info: playableInfo()}, newFakeStorage(), testOptions(t))
	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeConfigInvalid {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeConfigInvalid)
	}
}

func TestPipelineProcessOnlySkipsStorageAndKeepsPreview(t *testing.T) {
	dir := t.TempDir()
	keepDir := filepath.Join(dir, "preview")
	inputPath := filepath.Join(dir, "input.mp4")
	if err := os.WriteFile(inputPath, []byte("local-input"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := newTestPipeline(t, nil, nil, &fakeProcessor{info: playableInfo()}, nil, testOptions(t))

	result, err := pipeline.Run(context.Background(), biz.RunInput{
		LocalInputPath:   inputPath,
		ProcessOnly:      true,
		KeepProcessed:    true,
		KeepProcessedDir: keepDir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Asset != nil {
		t.Fatalf("process-only run must not produce an asset")
	}
	if !strings.HasPrefix(result.ProcessedPath, keepDir) {
		t.Fatalf("processed path = %q, want under %q", result.ProcessedPath, keepDir)
	}
	if _, err := os.Stat(result.ProcessedPath); err != nil {
		t.Fatalf("preview file missing: %v", err)
	}
}

func TestPipelineRejectsTooLargeDownload(t *testing.T) {
	opts := testOptions(t)
	opts.MaxInputBytes = 4
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: &biz.DownloadedFile{Path: "/tmp/x", SizeBytes: 10, SHA256: digestOf("x")}},
		&fakeProcessor{info: playableInfo()}, newFakeStorage(), opts)

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaTooLarge {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeMediaTooLarge)
	}
}

func TestPipelineHeadFailureStopsBeforeUpload(t *testing.T) {
	storage := newFakeStorage()
	storage.headErr = biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageAuthFailed, false, errors.New("403"))
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageAuthFailed {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeStorageAuthFailed)
	}
	if storage.puts != 0 {
		t.Fatalf("upload must not start after a storage authorization failure")
	}
}

func TestPipelineSignsPreviewWhenRequested(t *testing.T) {
	storage := newFakeStorage()
	opts := testOptions(t)
	opts.SignTTL = 10 * time.Minute
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, opts)

	result, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Signed == nil || result.Signed.URL == "" {
		t.Fatal("expected a signed preview access")
	}
	if storage.signTTLSeen != 10*time.Minute {
		t.Fatalf("sign ttl = %s", storage.signTTLSeen)
	}
	if result.Signed.Object.VersionID == "" || result.Signed.Object.VersionID != result.Asset.Object.VersionID {
		t.Fatalf("signature is not bound to the confirmed version: signed=%q asset=%q",
			result.Signed.Object.VersionID, result.Asset.Object.VersionID)
	}
	if storage.signedRef.Key != result.Asset.Object.Key {
		t.Fatalf("signed key %q != asset key %q", storage.signedRef.Key, result.Asset.Object.Key)
	}
}

func TestPipelinePublishesUploadedVersionIdentity(t *testing.T) {
	storage := newFakeStorage()
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, testOptions(t))

	result, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Asset.Object.VersionID != "v1" {
		t.Fatalf("asset version = %q, want v1", result.Asset.Object.VersionID)
	}
	if result.Asset.Object.Bucket != "bucket" || result.Asset.Object.Provider != "fake" {
		t.Fatalf("asset identity = %+v", result.Asset.Object)
	}
}

func TestPipelineRejectsVersionDrift(t *testing.T) {
	storage := newFakeStorage()
	storage.headDrift = "v-other"
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMismatch {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeStorageObjectMismatch)
	}
}

func TestPipelineRepairsMissingMetadataOnExistingObject(t *testing.T) {
	storage := newFakeStorage()
	sha := digestOf(processedContent)
	key := biz.ObjectKey("media-test", sha)
	storage.addVersion(&biz.StoredObject{
		ObjectRef: biz.ObjectRef{Provider: "fake", Bucket: "bucket", Key: key},
		SizeBytes: int64(len(processedContent)),
		// An object written before metadata existed is not considered confirmed.
	})
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, testOptions(t))

	result, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if storage.puts != 1 {
		t.Fatalf("puts = %d, want 1 metadata repair upload", storage.puts)
	}
	if result.Asset.Object.VersionID != "v2" {
		t.Fatalf("asset version = %q, want the repaired v2", result.Asset.Object.VersionID)
	}
}

func TestPipelineRejectsExistingMetadataMismatch(t *testing.T) {
	storage := newFakeStorage()
	sha := digestOf(processedContent)
	key := biz.ObjectKey("media-test", sha)
	storage.addVersion(&biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Key: key},
		SizeBytes:      int64(len(processedContent)),
		MetadataSHA256: digestOf("another-object"),
	})
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMismatch {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeStorageObjectMismatch)
	}
	if storage.puts != 0 {
		t.Fatalf("a conflicting object must not be silently overwritten")
	}
}

func TestPipelineRejectsMissingMetadataAfterUpload(t *testing.T) {
	storage := newFakeStorage()
	storage.dropMetadata = true
	pipeline := newTestPipeline(t,
		&fakeResolver{src: &biz.MediaSource{URL: "https://cdn.example/ep.mp4"}},
		&fakeDownloader{file: downloadFile(t, t.TempDir())},
		&fakeProcessor{info: playableInfo()}, storage, testOptions(t))

	_, err := pipeline.Run(context.Background(), biz.RunInput{SourceCode: "hongguo", ExternalEpisodeID: "episode-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMismatch {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeStorageObjectMismatch)
	}
}

func TestObjectKeyIsContentAddressed(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	if got := biz.ObjectKey("/media/", sha); got != "media/ab/"+sha+".mp4" {
		t.Fatalf("key = %q", got)
	}
	if got := biz.ObjectKey("", sha); got != "ab/"+sha+".mp4" {
		t.Fatalf("key = %q", got)
	}
}
