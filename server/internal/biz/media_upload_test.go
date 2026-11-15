package biz_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"openduanju-server/internal/biz"
)

// fakeUploadRepo is an in-memory upload repository for the use-case tests.
type fakeUploadRepo struct {
	session       *biz.UploadSession
	coverRecord   *biz.CompleteCoverRecord
	inputDigest   string
	coverCalls    int
	inputCalls    int
	createCalls   int
	renewCalls    int
	cancelCalls   int
	createRecord  biz.CreateUploadRecord
	renewExpiry   time.Time
	completedView *biz.UploadSession
}

func (f *fakeUploadRepo) CreateUpload(_ context.Context, record biz.CreateUploadRecord) (*biz.UploadSession, error) {
	f.createCalls++
	f.createRecord = record
	if f.session == nil {
		return nil, biz.ErrUploadNotFound
	}
	return f.session, nil
}

func (f *fakeUploadRepo) GetUpload(_ context.Context, _ int64) (*biz.UploadSession, error) {
	if f.session == nil {
		return nil, biz.ErrUploadNotFound
	}
	return f.session, nil
}

func (f *fakeUploadRepo) ListUploads(_ context.Context, _ biz.UploadQuery) ([]biz.UploadSession, int, error) {
	if f.session == nil {
		return nil, 0, nil
	}
	return []biz.UploadSession{*f.session}, 1, nil
}

func (f *fakeUploadRepo) RenewUpload(_ context.Context, _ int64, _ int, _ string, expiresAt time.Time) (*biz.UploadSession, error) {
	f.renewCalls++
	f.renewExpiry = expiresAt
	renewed := *f.session
	renewed.ExpiresAt = expiresAt
	renewed.Version++
	return &renewed, nil
}

func (f *fakeUploadRepo) CompleteUploadInput(_ context.Context, _ int64, _ int, _ string, _ biz.ObjectRef, sha256 string, _ int64, _ string) (*biz.UploadSession, error) {
	f.inputCalls++
	f.inputDigest = sha256
	completed := *f.session
	completed.Status = biz.UploadCompleted
	return &completed, nil
}

func (f *fakeUploadRepo) CompleteUploadCover(_ context.Context, record biz.CompleteCoverRecord, _ string) (*biz.UploadSession, error) {
	f.coverCalls++
	f.coverRecord = &record
	completed := *f.session
	completed.Status = biz.UploadCompleted
	completed.AssetID = 77
	return &completed, nil
}

func (f *fakeUploadRepo) CancelUpload(_ context.Context, _ int64, _ int, _ string) (*biz.UploadSession, error) {
	f.cancelCalls++
	cancelled := *f.session
	cancelled.Status = biz.UploadCancelled
	return &cancelled, nil
}

func (f *fakeUploadRepo) GetCompletedInput(_ context.Context, _ int64) (*biz.CompletedInput, error) {
	if f.session == nil || f.session.Status != biz.UploadCompleted {
		return nil, biz.ErrUploadNotPending
	}
	return &biz.CompletedInput{
		SessionID:  f.session.ID,
		RevisionID: f.session.RevisionID,
		EpisodeID:  f.session.EpisodeID,
		Kind:       f.session.Kind,
		Object: biz.ObjectRef{
			Provider: f.session.Provider,
			Bucket:   f.session.Bucket,
			Region:   f.session.Region,
			Key:      f.session.StagingKey,
		},
		SizeBytes: f.session.SizeBytes,
		SHA256:    f.session.SHA256,
		Status:    f.session.Status,
	}, nil
}

// fakeUploadContent returns a fixed revision.
type fakeUploadContent struct {
	revision *biz.ContentRevision
	err      error
}

func (f *fakeUploadContent) GetRevisionForUpload(_ context.Context, _ int64) (*biz.ContentRevision, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.revision, nil
}

func (f *fakeUploadContent) HasRevisionEpisode(_ context.Context, _ int64, _ int64) (bool, error) {
	return true, nil
}

// uploadFixture builds one pending episode session with a staged object.
func uploadFixture(t *testing.T, storage *fakeStorage, body []byte, kind string) (*biz.Uploads, *fakeUploadRepo, *biz.UploadSession) {
	t.Helper()
	dir := t.TempDir()
	local := filepath.Join(dir, "staged.bin")
	if err := os.WriteFile(local, body, 0o600); err != nil {
		t.Fatal(err)
	}
	key := "uploads/1/episodes/2/staged"
	session := &biz.UploadSession{
		ID:              11,
		RevisionID:      1,
		EpisodeID:       2,
		Kind:            kind,
		Status:          biz.UploadPending,
		Version:         1,
		Provider:        "fake",
		Bucket:          "bucket",
		Region:          "region",
		StagingKey:      key,
		SizeBytes:       int64(len(body)),
		CreatedBy:       "shared-token",
		RevisionVersion: 3,
		ExpiresAt:       time.Now().Add(10 * time.Minute),
	}
	storage.addVersion(&biz.StoredObject{
		ObjectRef:      biz.ObjectRef{Provider: "fake", Bucket: "bucket", Region: "region", Key: key, VersionID: "v1"},
		SizeBytes:      int64(len(body)),
		MetadataSHA256: digestOf(string(body)),
		ContentType:    "application/octet-stream",
	})
	if storage.files == nil {
		storage.files = map[string]string{}
	}
	storage.files[key] = local
	repo := &fakeUploadRepo{session: session}
	content := &fakeUploadContent{revision: &biz.ContentRevision{
		ID:      1,
		State:   biz.RevisionStateDraft,
		Version: 3,
	}}
	uc := biz.NewUploads(repo, content, storage, biz.UploadOptions{
		TTL:           time.Minute,
		MaxCoverBytes: 5 << 20,
		TempDir:       dir,
		KeyPrefix:     "openduanju-media",
		Provider:      "fake",
		Bucket:        "bucket",
		Region:        "region",
		MinCoverSide:  100,
		MaxCoverSide:  4000,
	})
	return uc, repo, session
}

func TestUploadCreateRejectsInvalidTarget(t *testing.T) {
	storage := newFakeStorage()
	uc, _, _ := uploadFixture(t, storage, []byte("video"), biz.UploadKindEpisode)
	ctx := context.Background()

	if _, err := uc.Create(ctx, biz.CreateUploadRequest{
		RevisionID: 1, ExpectedRevisionVersion: 3, EpisodeID: 0,
		Kind: biz.UploadKindEpisode, Filename: "a.mp4", SizeBytes: 10,
	}); !errors.Is(err, biz.ErrUploadEpisodeInvalid) {
		t.Fatalf("episode input without episode: got %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateUploadRequest{
		RevisionID: 1, ExpectedRevisionVersion: 3, EpisodeID: 2,
		Kind: biz.UploadKindCover, Filename: "a.jpg", SizeBytes: 10,
	}); !errors.Is(err, biz.ErrUploadEpisodeInvalid) {
		t.Fatalf("cover bound to an episode: got %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateUploadRequest{
		RevisionID: 1, ExpectedRevisionVersion: 3, EpisodeID: 2,
		Kind: biz.UploadKindEpisode, Filename: "a.mp4", SizeBytes: 0,
	}); !errors.Is(err, biz.ErrUploadSizeInvalid) {
		t.Fatalf("zero size: got %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateUploadRequest{
		RevisionID: 1, ExpectedRevisionVersion: 0, EpisodeID: 2,
		Kind: biz.UploadKindEpisode, Filename: "a.mp4", SizeBytes: 10,
	}); !errors.Is(err, biz.ErrVersionRequired) {
		t.Fatalf("missing revision version: got %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateUploadRequest{
		RevisionID: 1, ExpectedRevisionVersion: 3, EpisodeID: 2,
		Kind: "unknown", Filename: "a.mp4", SizeBytes: 10,
	}); !errors.Is(err, biz.ErrUploadKindInvalid) {
		t.Fatalf("unknown kind: got %v", err)
	}
}

func TestUploadCreateRejectsOversizeCover(t *testing.T) {
	storage := newFakeStorage()
	uc, _, _ := uploadFixture(t, storage, []byte("video"), biz.UploadKindCover)
	_, err := uc.Create(context.Background(), biz.CreateUploadRequest{
		RevisionID: 1, ExpectedRevisionVersion: 3,
		Kind: biz.UploadKindCover, Filename: "cover.jpg", SizeBytes: 6 << 20,
	})
	if !errors.Is(err, biz.ErrUploadTooLarge) {
		t.Fatalf("oversize cover: got %v", err)
	}
}

func TestUploadCompleteRejectsExpiredAndForeignSession(t *testing.T) {
	storage := newFakeStorage()
	uc, repo, session := uploadFixture(t, storage, []byte("video"), biz.UploadKindEpisode)
	session.ExpiresAt = time.Now().Add(-time.Minute)
	if _, err := uc.Complete(context.Background(), session.ID, 1, "shared-token"); !errors.Is(err, biz.ErrUploadExpired) {
		t.Fatalf("expired session: got %v", err)
	}
	session.ExpiresAt = time.Now().Add(time.Minute)
	if _, err := uc.Complete(context.Background(), session.ID, 1, "another-operator"); !errors.Is(err, biz.ErrUploadActorNotOwner) {
		t.Fatalf("foreign actor: got %v", err)
	}
	if repo.inputCalls != 0 {
		t.Fatalf("a rejected completion bound %d inputs", repo.inputCalls)
	}
}

func TestUploadCompleteRejectsMissingObjectAndSizeMismatch(t *testing.T) {
	storage := newFakeStorage()
	uc, _, session := uploadFixture(t, storage, []byte("video"), biz.UploadKindEpisode)
	// A different key has no stored object.
	session.StagingKey = "uploads/1/episodes/2/absent"
	if _, err := uc.Complete(context.Background(), session.ID, 1, "shared-token"); !errors.Is(err, biz.ErrUploadObjectMissing) {
		t.Fatalf("missing object: got %v", err)
	}
	session.StagingKey = "uploads/1/episodes/2/staged"
	session.SizeBytes = 99
	if _, err := uc.Complete(context.Background(), session.ID, 1, "shared-token"); !errors.Is(err, biz.ErrUploadObjectSize) {
		t.Fatalf("size mismatch: got %v", err)
	}
}

func TestUploadCompleteRejectsChangedRevision(t *testing.T) {
	storage := newFakeStorage()
	uc, repo, session := uploadFixture(t, storage, []byte("video"), biz.UploadKindEpisode)
	uc = biz.NewUploads(repo, &fakeUploadContent{revision: &biz.ContentRevision{
		ID: 1, State: biz.RevisionStateDraft, Version: 4,
	}}, storage, biz.UploadOptions{TTL: time.Minute, TempDir: t.TempDir(), Provider: "fake", Bucket: "bucket", Region: "region", MinCoverSide: 100})
	if _, err := uc.Complete(context.Background(), session.ID, 1, "shared-token"); !errors.Is(err, biz.ErrUploadRevisionChanged) {
		t.Fatalf("changed revision: got %v", err)
	}
}

func TestUploadCompleteEpisodeInputIsIdempotent(t *testing.T) {
	storage := newFakeStorage()
	uc, repo, session := uploadFixture(t, storage, []byte("video"), biz.UploadKindEpisode)
	first, err := uc.Complete(context.Background(), session.ID, 1, "shared-token")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if first.Status != biz.UploadCompleted {
		t.Fatalf("status: %s", first.Status)
	}
	if repo.inputCalls != 1 {
		t.Fatalf("input registered %d times", repo.inputCalls)
	}
	// A repeated confirmation returns the stored session and binds nothing.
	session.Status = biz.UploadCompleted
	if _, err := uc.Complete(context.Background(), session.ID, 1, "shared-token"); err != nil {
		t.Fatalf("repeat complete: %v", err)
	}
	if repo.inputCalls != 1 {
		t.Fatalf("repeat complete bound a second input: %d", repo.inputCalls)
	}
}

func TestUploadCompleteCoverRejectsNonImage(t *testing.T) {
	storage := newFakeStorage()
	body := []byte("# not an image, just a script pretending to be a cover\n")
	uc, repo, session := uploadFixture(t, storage, body, biz.UploadKindCover)
	_, err := uc.Complete(context.Background(), session.ID, 1, "shared-token")
	if !errors.Is(err, biz.ErrCoverImageUnsupported) && !errors.Is(err, biz.ErrCoverImageInvalid) {
		t.Fatalf("non-image cover: got %v", err)
	}
	if repo.coverCalls != 0 {
		t.Fatalf("an invalid cover was bound %d times", repo.coverCalls)
	}
}

func TestUploadCompleteCoverBindsValidatedImage(t *testing.T) {
	storage := newFakeStorage()
	body := encodePNG(t, 320, 480)
	uc, repo, session := uploadFixture(t, storage, body, biz.UploadKindCover)
	completed, err := uc.Complete(context.Background(), session.ID, 1, "shared-token")
	if err != nil {
		t.Fatalf("complete cover: %v", err)
	}
	if repo.coverCalls != 1 || repo.coverRecord == nil {
		t.Fatalf("cover was not bound once: %d", repo.coverCalls)
	}
	if repo.coverRecord.Width != 320 || repo.coverRecord.Height != 480 {
		t.Fatalf("cover dimensions: %dx%d", repo.coverRecord.Width, repo.coverRecord.Height)
	}
	if repo.coverRecord.ContentType != "image/png" {
		t.Fatalf("cover content type: %s", repo.coverRecord.ContentType)
	}
	if completed.AssetID != 77 {
		t.Fatalf("cover asset id: %d", completed.AssetID)
	}
	// The stored cover is content addressed, so the same bytes never create a
	// second object.
	if repo.coverRecord.Object.Key != biz.CoverObjectKey("openduanju-media", digestOf(string(body)), ".png") {
		t.Fatalf("cover key: %s", repo.coverRecord.Object.Key)
	}
	// The validated bytes were stored as an immutable object.
	if storage.latest(repo.coverRecord.Object.Key) == nil {
		t.Fatal("the cover object was not stored")
	}
}

func TestUploadCompleteRejectsVideoThroughCoverEntry(t *testing.T) {
	storage := newFakeStorage()
	// A real MP4 header is not a decodable image: the cover entry must not
	// promote it.
	body := []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}
	uc, repo, session := uploadFixture(t, storage, body, biz.UploadKindCover)
	if _, err := uc.Complete(context.Background(), session.ID, 1, "shared-token"); err == nil {
		t.Fatal("a video container was accepted as a cover")
	}
	if repo.coverCalls != 0 {
		t.Fatal("a rejected cover reached the repository")
	}
}

// encodePNG builds a real PNG of the requested size.
func encodePNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
