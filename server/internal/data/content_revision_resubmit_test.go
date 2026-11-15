package data_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/mediaasset"
	"openduanju-server/internal/data/ent/reviewdecision"
	"openduanju-server/internal/data/ent/reviewtask"
	"openduanju-server/internal/data/ent/revisionepisode"
)

// countingProcessor records how often a file is really transcoded. A task
// that reuses inherited material must not reach the processor again.
type countingProcessor struct {
	inner    biz.MediaProcessor
	prepares atomic.Int32
}

func (c *countingProcessor) Probe(ctx context.Context, path string) (*biz.MediaInfo, error) {
	return c.inner.Probe(ctx, path)
}

func (c *countingProcessor) Prepare(ctx context.Context, req biz.ProcessRequest) (*biz.ProcessedMedia, error) {
	c.prepares.Add(1)
	return c.inner.Prepare(ctx, req)
}

// resubmitChain is the real chain a resubmission runs through: a disposable
// MySQL database, the local object storage, the manual upload use case, the
// media batch repository, the review repository and the persistent worker.
type resubmitChain struct {
	client     *ent.Client
	storage    *data.LocalObjectStorage
	uploads    *biz.Uploads
	uploadRepo *data.UploadRepo
	batches    *data.MediaBatchRepo
	catalog    biz.SourceCatalogRepo
	reviews    *data.ReviewRepo
	processor  *countingProcessor
	worker     *biz.MediaWorker
	mediaDir   string
	// storageRoot keeps the local object tree, so a test can remove an
	// object and prove the worker verifies what it reuses.
	storageRoot string
}

func newResubmitChain(t *testing.T) *resubmitChain {
	t.Helper()
	requireFFmpeg(t)
	client := openMediaClient(t)
	dir := t.TempDir()
	storage, err := data.NewLocalObjectStorage(filepath.Join(dir, "storage"))
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	uploadRepo := data.NewUploadRepoWithDB(client)
	uploads := biz.NewUploads(uploadRepo, uploadRepo, storage, biz.UploadOptions{
		TTL:             30 * time.Minute,
		MaxEpisodeBytes: 64 << 20,
		MaxCoverBytes:   10 << 20,
		KeyPrefix:       "openduanju-media",
		TempDir:         filepath.Join(dir, "upload-tmp"),
		MinCoverSide:    240,
		MaxCoverSide:    10000,
		Provider:        "local",
		Bucket:          "objects",
		Region:          "local",
	})
	processor := &countingProcessor{inner: newFFmpegProcessor(t, 2*time.Minute)}
	batches := data.NewMediaBatchRepoWithDB(client)
	worker, err := biz.NewMediaWorker(batches, nil, nil, processor, storage, biz.MediaWorkerOptions{
		Worker:         "resubmit-test-worker",
		Lease:          30 * time.Second,
		BatchSize:      8,
		PollInterval:   time.Second,
		ProfileKey:     "web-mp4-h264-aac-v1",
		KeyPrefix:      "openduanju-media",
		TempDir:        filepath.Join(dir, "media-tmp"),
		MaxInputBytes:  64 << 20,
		MaxOutputBytes: 64 << 20,
		RetryBackoff:   time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("worker: %v", err)
	}
	return &resubmitChain{
		client:      client,
		storage:     storage,
		uploads:     uploads,
		uploadRepo:  uploadRepo,
		batches:     batches,
		catalog:     data.NewSourceCatalogRepoWithDB(client),
		reviews:     data.NewReviewRepoWithDB(client),
		processor:   processor,
		worker:      worker.WithUploads(uploadRepo),
		mediaDir:    dir,
		storageRoot: filepath.Join(dir, "storage"),
	}
}

// stageEpisode uploads one real video through the manual upload use case. The
// bytes reach the staging object, the completion reads them back and binds
// them to the episode.
func (c *resubmitChain) stageEpisode(t *testing.T, revisionID int64, revisionVersion int, episodeID int64, path string, actor string) *biz.UploadSession {
	t.Helper()
	ctx := context.Background()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat video: %v", err)
	}
	view, err := c.uploads.Create(ctx, biz.CreateUploadRequest{
		RevisionID:              revisionID,
		ExpectedRevisionVersion: revisionVersion,
		EpisodeID:               episodeID,
		Kind:                    biz.UploadKindEpisode,
		Filename:                filepath.Base(path),
		SizeBytes:               info.Size(),
		ContentType:             "video/mp4",
		Actor:                   actor,
	})
	if err != nil {
		t.Fatalf("create episode upload: %v", err)
	}
	if _, err := c.storage.PutFile(ctx, biz.PutObjectRequest{
		Key:         view.Session.StagingKey,
		Path:        path,
		ContentType: "video/mp4",
		SizeBytes:   info.Size(),
	}); err != nil {
		t.Fatalf("stage episode bytes: %v", err)
	}
	completed, err := c.uploads.Complete(ctx, view.Session.ID, view.Session.Version, actor)
	if err != nil {
		t.Fatalf("complete episode upload: %v", err)
	}
	return completed
}

// stageCover uploads one real image and binds it to the revision.
func (c *resubmitChain) stageCover(t *testing.T, revisionID int64, revisionVersion int, path string, actor string) *biz.UploadSession {
	t.Helper()
	ctx := context.Background()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cover: %v", err)
	}
	view, err := c.uploads.Create(ctx, biz.CreateUploadRequest{
		RevisionID:              revisionID,
		ExpectedRevisionVersion: revisionVersion,
		Kind:                    biz.UploadKindCover,
		Filename:                filepath.Base(path),
		SizeBytes:               info.Size(),
		ContentType:             "image/png",
		Actor:                   actor,
	})
	if err != nil {
		t.Fatalf("create cover upload: %v", err)
	}
	if _, err := c.storage.PutFile(ctx, biz.PutObjectRequest{
		Key:         view.Session.StagingKey,
		Path:        path,
		ContentType: "image/png",
		SizeBytes:   info.Size(),
	}); err != nil {
		t.Fatalf("stage cover bytes: %v", err)
	}
	completed, err := c.uploads.Complete(ctx, view.Session.ID, view.Session.Version, actor)
	if err != nil {
		t.Fatalf("complete cover upload: %v", err)
	}
	return completed
}

// generateCoverPNG writes one decodable cover image.
func generateCoverPNG(t *testing.T, dir string) string {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 800; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 180, A: 255})
		}
	}
	path := filepath.Join(dir, "cover.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create cover file: %v", err)
	}
	if err := png.Encode(file, source); err != nil {
		_ = file.Close()
		t.Fatalf("encode cover: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close cover: %v", err)
	}
	return path
}

// seedResubmitGrant stores one active grant with all three permissions.
func seedResubmitGrant(t *testing.T, client *ent.Client, seriesID int64, reference string) *ent.AuthorizationGrant {
	t.Helper()
	now := time.Now().UTC()
	grant, err := client.AuthorizationGrant.Create().
		SetSeriesID(seriesID).
		SetReference(reference).
		SetAllowProcess(true).
		SetAllowDisplay(true).
		SetAllowPlayback(true).
		SetValidFrom(now.Add(-time.Hour)).
		SetStatus("active").
		SetVersion(1).
		SetConfirmedBy("admin").
		SetConfirmedAt(now).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return grant
}

// freezeRevision submits a revision with one explicit grant.
func (c *resubmitChain) freezeRevision(t *testing.T, revisionID int64, revisionVersion int, grantID int64, actor string) *biz.MediaBatch {
	t.Helper()
	batch, err := c.batches.FreezeAndCreateBatch(context.Background(), biz.FreezeBatchRequest{
		RevisionID:                   revisionID,
		ExpectedVersion:              revisionVersion,
		ProfileVersion:               "web-mp4-h264-aac-v1",
		InputKind:                    "manual",
		Actor:                        actor,
		AuthorizationGrantID:         grantID,
		ExpectedAuthorizationVersion: 1,
	})
	if err != nil {
		t.Fatalf("freeze revision %d: %v", revisionID, err)
	}
	return batch
}

// runWorker processes every due task of the batch.
func (c *resubmitChain) runWorker(t *testing.T) {
	t.Helper()
	for attempt := 0; attempt < 4; attempt++ {
		handled, err := c.worker.ProcessOnce(context.Background())
		if err != nil {
			t.Fatalf("worker tick: %v", err)
		}
		if handled == 0 {
			return
		}
	}
}

func (c *resubmitChain) episodes(t *testing.T, revisionID int64) []*ent.RevisionEpisode {
	t.Helper()
	rows, err := c.client.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID)).
		Order(revisionepisode.ByEpisodeNo()).
		All(context.Background())
	if err != nil {
		t.Fatalf("list revision episodes: %v", err)
	}
	return rows
}

func (c *resubmitChain) reviewTasks(t *testing.T, revisionID int64) []*ent.ReviewTask {
	t.Helper()
	rows, err := c.client.ReviewTask.Query().
		Where(reviewtask.RevisionIDEQ(revisionID)).
		All(context.Background())
	if err != nil {
		t.Fatalf("list review tasks: %v", err)
	}
	return rows
}

// revisionVersion reads the version a submission has to name.
func (c *resubmitChain) revisionVersion(t *testing.T, revisionID int64) int {
	t.Helper()
	row, err := c.client.ContentRevision.Get(context.Background(), revisionID)
	if err != nil {
		t.Fatalf("read revision %d: %v", revisionID, err)
	}
	return int(row.Version)
}

// TestInheritedMaterialReachesReviewAfterResubmission proves the whole
// resubmission chain: the rejected revision keeps its ready video and cover,
// a new draft inherits them, the resubmission runs the existing worker, the
// worker reuses the inherited material (no second transcode, no second
// object) and the review gate opens exactly one task for the new revision.
func TestInheritedMaterialReachesReviewAfterResubmission(t *testing.T) {
	chain := newResubmitChain(t)
	ctx := context.Background()
	video := generateCompatibleMP4(t, t.TempDir())
	cover := generateCoverPNG(t, t.TempDir())

	// A draft with one selected episode, a real video and a real cover.
	base := seedRevision(t, chain.client, 1)
	baseEpisodes := chain.episodes(t, base.ID)
	chain.stageEpisode(t, base.ID, int(base.Version), baseEpisodes[0].ID, video, "operator")
	chain.stageCover(t, base.ID, int(base.Version), cover, "operator")

	firstGrant := seedResubmitGrant(t, chain.client, base.SeriesID, "AUTH-RESUBMIT-001")
	// Binding the cover advanced the revision version.
	firstBatch := chain.freezeRevision(t, base.ID, chain.revisionVersion(t, base.ID), firstGrant.ID, "operator")
	chain.runWorker(t)

	first, err := chain.client.ContentRevision.Get(ctx, base.ID)
	if err != nil {
		t.Fatalf("read the submitted revision: %v", err)
	}
	if first.State != biz.RevisionStatePendingReview {
		t.Fatalf("revision after submission: %s", first.State)
	}
	firstTasks := chain.reviewTasks(t, base.ID)
	if len(firstTasks) != 1 {
		t.Fatalf("review tasks after the first submission: %d", len(firstTasks))
	}
	baseEpisodes = chain.episodes(t, base.ID)
	if baseEpisodes[0].AssetID == 0 || baseEpisodes[0].AssetState != string(biz.MediaAssetReady) {
		t.Fatalf("the first submission did not freeze a ready asset: %+v", baseEpisodes[0])
	}
	frozenAssetID := baseEpisodes[0].AssetID
	if prepared := chain.processor.prepares.Load(); prepared != 1 {
		t.Fatalf("the first submission transcoded %d times, want one", prepared)
	}

	// The reviewer rejects the submission with a reason.
	claimed, err := chain.reviews.ClaimReview(ctx, firstTasks[0].ID, int(firstTasks[0].Version), "reviewer")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := chain.reviews.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID:          firstTasks[0].ID,
		ExpectedVersion: claimed.Version,
		Decision:        biz.DecisionReject,
		Reason:          "第1集音画不同步",
		Actor:           "reviewer",
	}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected, err := chain.client.ContentRevision.Get(ctx, base.ID)
	if err != nil {
		t.Fatalf("read the rejected revision: %v", err)
	}
	if rejected.State != biz.RevisionStateRejected {
		t.Fatalf("revision after the rejection: %s", rejected.State)
	}

	// A rejected revision is history, not an editable draft: the operator
	// creates a new draft without replacing anything.
	if _, err := chain.catalog.UpdateRevision(ctx, base.SeriesID, base.ID, int(rejected.Version), true, "不该生效", false, ""); err == nil {
		t.Fatal("a rejected revision was edited in place")
	}
	view, err := chain.catalog.CreateRevision(ctx, base.SeriesID, base.ID, 0, 0)
	if err != nil {
		t.Fatalf("create the new draft: %v", err)
	}
	next := view.Revision
	if next == nil || next.ID == base.ID || next.State != biz.RevisionStateDraft {
		t.Fatalf("new revision: %+v", next)
	}
	// The inherited selection keeps the frozen material and the input.
	nextEpisodes := chain.episodes(t, next.ID)
	if len(nextEpisodes) != 1 {
		t.Fatalf("the new revision has %d episodes", len(nextEpisodes))
	}
	if nextEpisodes[0].AssetID != frozenAssetID || nextEpisodes[0].AssetState != string(biz.MediaAssetReady) {
		t.Fatalf("the frozen material was not inherited: %+v", nextEpisodes[0])
	}
	if nextEpisodes[0].InputKind != "manual" || nextEpisodes[0].InputRef != baseEpisodes[0].InputRef {
		t.Fatalf("the confirmed input was not inherited: %+v", nextEpisodes[0])
	}
	nextRevision, err := chain.client.ContentRevision.Get(ctx, next.ID)
	if err != nil {
		t.Fatalf("read the new draft: %v", err)
	}
	if nextRevision.CoverAssetID == 0 {
		t.Fatal("the inherited cover is missing")
	}
	if nextRevision.AuthorizationGrantID != 0 || nextRevision.AuthorizationHash != "" {
		t.Fatalf("the new draft inherited an authorization: %+v", nextRevision)
	}

	// The resubmission chooses a valid grant explicitly and runs the
	// existing worker and gate.
	secondGrant := seedResubmitGrant(t, chain.client, base.SeriesID, "AUTH-RESUBMIT-002")
	secondBatch := chain.freezeRevision(t, next.ID, int(nextRevision.Version), secondGrant.ID, "operator")
	if secondBatch.CoverStatus != biz.MediaCoverReady {
		t.Fatalf("the inherited cover did not reach the batch: %s", secondBatch.CoverStatus)
	}
	chain.runWorker(t)

	// The inherited material was reused: no second transcode and no second
	// asset row.
	if prepared := chain.processor.prepares.Load(); prepared != 1 {
		t.Fatalf("the resubmission transcoded %d times, want the inherited material to be reused", prepared)
	}
	assets, err := chain.client.MediaAsset.Query().
		Where(mediaasset.IDEQ(frozenAssetID), mediaasset.KindEQ("episode")).
		All(ctx)
	if err != nil {
		t.Fatalf("read the frozen asset: %v", err)
	}
	if len(assets) != 1 || assets[0].State != string(biz.MediaAssetReady) {
		t.Fatalf("the frozen asset changed: %+v", assets)
	}

	// Exactly one review task describes the new revision.
	nextTasks := chain.reviewTasks(t, next.ID)
	if len(nextTasks) != 1 {
		items, _, err := chain.batches.ListItems(ctx, biz.ItemQuery{BatchID: secondBatch.ID, Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("review tasks of the resubmitted revision: %d (items: %v)", len(nextTasks), err)
		}
		t.Fatalf("review tasks of the resubmitted revision: %d, items: %+v", len(nextTasks), items)
	}
	stored, err := chain.client.ContentRevision.Get(ctx, next.ID)
	if err != nil {
		t.Fatalf("read the resubmitted revision: %v", err)
	}
	if stored.State != biz.RevisionStatePendingReview {
		t.Fatalf("the resubmitted revision is %s, want pending_review", stored.State)
	}
	if stored.AuthorizationGrantID != secondGrant.ID || stored.AuthorizationVersion != 1 {
		t.Fatalf("the resubmission did not freeze the chosen grant: %+v", stored)
	}
	if stored.AuthorizationHash == "" {
		t.Fatal("the resubmission did not freeze the grant fingerprint")
	}
	batch, err := chain.batches.GetBatch(ctx, secondBatch.ID)
	if err != nil {
		t.Fatalf("read the resubmitted batch: %v", err)
	}
	if batch.Status != biz.MediaBatchPendingReview || batch.ItemReady != batch.ItemTotal || batch.ItemTotal != 1 {
		t.Fatalf("resubmitted batch: status=%s ready=%d total=%d", batch.Status, batch.ItemReady, batch.ItemTotal)
	}
	resubmittedEpisodes := chain.episodes(t, next.ID)
	if resubmittedEpisodes[0].AssetID != frozenAssetID {
		t.Fatalf("the resubmitted revision lost the frozen asset: %d", resubmittedEpisodes[0].AssetID)
	}

	// The old revision, its batch, its asset and its review history are
	// untouched.
	oldRevision, err := chain.client.ContentRevision.Get(ctx, base.ID)
	if err != nil {
		t.Fatalf("read the rejected revision: %v", err)
	}
	if oldRevision.State != biz.RevisionStateRejected || oldRevision.AuthorizationGrantID != firstGrant.ID {
		t.Fatalf("the rejected revision changed: %+v", oldRevision)
	}
	oldBatch, err := chain.batches.GetBatch(ctx, firstBatch.ID)
	if err != nil {
		t.Fatalf("read the old batch: %v", err)
	}
	if oldBatch.Status != biz.MediaBatchPendingReview || oldBatch.ItemReady != oldBatch.ItemTotal {
		t.Fatalf("the old batch changed: %+v", oldBatch)
	}
	oldTasks := chain.reviewTasks(t, base.ID)
	if len(oldTasks) != 1 || oldTasks[0].Status != biz.ReviewRejected {
		t.Fatalf("the old review history changed: %+v", oldTasks)
	}
}

// generateAlternateMP4 writes a clip whose bytes differ from the first one, so
// a reprocessed episode produces a different content identity.
func generateAlternateMP4(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(dir, "alternate.mp4")
	cmd := exec.Command("ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=44100",
		"-t", "3",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-movflags", "+faststart",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate alternate mp4: %v: %s", err, out)
	}
	return path
}

// submitAndReject runs one revision through the worker and the review, and
// returns the rejected revision. It is the starting point of both negative
// cases: a rejected revision whose ready material a new draft inherits.
func (c *resubmitChain) submitAndReject(t *testing.T, video string, cover string) *ent.ContentRevision {
	t.Helper()
	ctx := context.Background()
	revision := seedRevision(t, c.client, 1)
	episodes := c.episodes(t, revision.ID)
	c.stageEpisode(t, revision.ID, int(revision.Version), episodes[0].ID, video, "operator")
	c.stageCover(t, revision.ID, int(revision.Version), cover, "operator")
	grant := seedResubmitGrant(t, c.client, revision.SeriesID, "AUTH-NEGATIVE-001")
	c.freezeRevision(t, revision.ID, c.revisionVersion(t, revision.ID), grant.ID, "operator")
	c.runWorker(t)
	tasks := c.reviewTasks(t, revision.ID)
	if len(tasks) != 1 {
		t.Fatalf("review tasks after the submission: %d", len(tasks))
	}
	claimed, err := c.reviews.ClaimReview(ctx, tasks[0].ID, int(tasks[0].Version), "reviewer")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := c.reviews.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID:          tasks[0].ID,
		ExpectedVersion: claimed.Version,
		Decision:        biz.DecisionReject,
		Reason:          "第1集音画不同步",
		Actor:           "reviewer",
	}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected, err := c.client.ContentRevision.Get(ctx, revision.ID)
	if err != nil {
		t.Fatalf("read the rejected revision: %v", err)
	}
	return rejected
}

// TestResubmissionProcessesAnEpisodeWhoseInputChanged proves the inherited
// material is only reused while the input is the one it was produced from: a
// new upload on the new draft is processed again.
func TestResubmissionProcessesAnEpisodeWhoseInputChanged(t *testing.T) {
	chain := newResubmitChain(t)
	ctx := context.Background()
	first := generateCompatibleMP4(t, t.TempDir())
	cover := generateCoverPNG(t, t.TempDir())
	rejected := chain.submitAndReject(t, first, cover)

	view, err := chain.catalog.CreateRevision(ctx, rejected.SeriesID, rejected.ID, 0, 0)
	if err != nil {
		t.Fatalf("create the new draft: %v", err)
	}
	next := view.Revision
	// The operator uploads a different video to the copied episode.
	episodes := chain.episodes(t, next.ID)
	second := generateAlternateMP4(t, t.TempDir())
	chain.stageEpisode(t, next.ID, chain.revisionVersion(t, next.ID), episodes[0].ID, second, "operator")

	grant := seedResubmitGrant(t, chain.client, rejected.SeriesID, "AUTH-NEGATIVE-002")
	chain.freezeRevision(t, next.ID, chain.revisionVersion(t, next.ID), grant.ID, "operator")
	chain.runWorker(t)

	if prepared := chain.processor.prepares.Load(); prepared != 2 {
		t.Fatalf("the changed input was not processed again: %d transcodes", prepared)
	}
	if tasks := chain.reviewTasks(t, next.ID); len(tasks) != 1 {
		t.Fatalf("review tasks after the resubmission: %d", len(tasks))
	}
	stored, err := chain.client.ContentRevision.Get(ctx, next.ID)
	if err != nil {
		t.Fatalf("read the resubmitted revision: %v", err)
	}
	if stored.State != biz.RevisionStatePendingReview {
		t.Fatalf("the resubmitted revision is %s", stored.State)
	}
	// The new material is a different object than the one the base revision
	// froze.
	baseEpisodes := chain.episodes(t, rejected.ID)
	nextEpisodes := chain.episodes(t, next.ID)
	if nextEpisodes[0].AssetID == 0 || nextEpisodes[0].AssetID == baseEpisodes[0].AssetID {
		t.Fatalf("the changed input kept the old asset: %d", nextEpisodes[0].AssetID)
	}
}

// TestResubmissionReprocessesWhenTheInheritedObjectIsGone proves the worker
// verifies the stored object instead of trusting the database row: a missing
// object is produced again from the inherited input.
func TestResubmissionReprocessesWhenTheInheritedObjectIsGone(t *testing.T) {
	chain := newResubmitChain(t)
	ctx := context.Background()
	first := generateCompatibleMP4(t, t.TempDir())
	cover := generateCoverPNG(t, t.TempDir())
	rejected := chain.submitAndReject(t, first, cover)

	baseEpisodes := chain.episodes(t, rejected.ID)
	if baseEpisodes[0].AssetID == 0 {
		t.Fatal("the first submission froze no asset")
	}
	asset, err := chain.client.MediaAsset.Get(ctx, baseEpisodes[0].AssetID)
	if err != nil {
		t.Fatalf("read the frozen asset: %v", err)
	}
	// The object disappears under the revision, as a lost bucket object does.
	for _, path := range []string{
		filepath.Join(chain.storageRoot, "objects", filepath.FromSlash(asset.ObjectKey)),
		filepath.Join(chain.storageRoot, "versions", filepath.FromSlash(asset.ObjectKey)+"."+asset.ObjectVersion),
	} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove the stored object: %v", err)
		}
	}

	view, err := chain.catalog.CreateRevision(ctx, rejected.SeriesID, rejected.ID, 0, 0)
	if err != nil {
		t.Fatalf("create the new draft: %v", err)
	}
	next := view.Revision
	grant := seedResubmitGrant(t, chain.client, rejected.SeriesID, "AUTH-NEGATIVE-003")
	chain.freezeRevision(t, next.ID, chain.revisionVersion(t, next.ID), grant.ID, "operator")
	chain.runWorker(t)

	// The material was not reused: the task read the inherited input and
	// produced the object again.
	if prepared := chain.processor.prepares.Load(); prepared != 2 {
		t.Fatalf("the missing object was reused: %d transcodes", prepared)
	}
	if tasks := chain.reviewTasks(t, next.ID); len(tasks) != 1 {
		t.Fatalf("review tasks after the resubmission: %d", len(tasks))
	}
	nextEpisodes := chain.episodes(t, next.ID)
	if nextEpisodes[0].AssetState != string(biz.MediaAssetReady) {
		t.Fatalf("the reprocessed episode is not ready: %s", nextEpisodes[0].AssetState)
	}
	rebuilt, err := chain.client.MediaAsset.Get(ctx, nextEpisodes[0].AssetID)
	if err != nil {
		t.Fatalf("read the rebuilt asset: %v", err)
	}
	head, err := chain.storage.Head(ctx, biz.ObjectRef{
		Provider: rebuilt.Provider, Bucket: rebuilt.Bucket, Region: rebuilt.Region,
		Key: rebuilt.ObjectKey, VersionID: rebuilt.ObjectVersion,
	})
	if err != nil || head == nil {
		t.Fatalf("the object was not stored again: %v", err)
	}
	if head.SizeBytes != rebuilt.SizeBytes {
		t.Fatalf("the rebuilt object is %d bytes, the asset says %d", head.SizeBytes, rebuilt.SizeBytes)
	}
}

// TestRejectedRevisionIsResubmittedReviewedAndPublished runs the whole chain
// the product describes: a published revision stays on the shelf while a new
// revision is submitted, rejected, reworked in a new draft, submitted again,
// approved and finally published. The review history and the old revision are
// never rewritten, and the authorization gate keeps working after the
// republication.
func TestRejectedRevisionIsResubmittedReviewedAndPublished(t *testing.T) {
	chain := newResubmitChain(t)
	ctx := context.Background()
	video := generateCompatibleMP4(t, t.TempDir())
	cover := generateCoverPNG(t, t.TempDir())
	web := data.NewWebRepoWithDB(chain.client)

	// 1. The first revision is submitted, approved and published.
	first := seedRevision(t, chain.client, 1)
	seriesID := first.SeriesID
	episodes := chain.episodes(t, first.ID)
	chain.stageEpisode(t, first.ID, int(first.Version), episodes[0].ID, video, "operator")
	chain.stageCover(t, first.ID, int(first.Version), cover, "operator")
	firstGrant := seedResubmitGrant(t, chain.client, seriesID, "AUTH-CHAIN-001")
	chain.freezeRevision(t, first.ID, chain.revisionVersion(t, first.ID), firstGrant.ID, "operator")
	chain.runWorker(t)
	firstTask := chain.reviewTasks(t, first.ID)
	if len(firstTask) != 1 {
		t.Fatalf("review tasks of the first revision: %d", len(firstTask))
	}
	claimed, err := chain.reviews.ClaimReview(ctx, firstTask[0].ID, int(firstTask[0].Version), "reviewer")
	if err != nil {
		t.Fatalf("claim the first review: %v", err)
	}
	if _, err := chain.reviews.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: firstTask[0].ID, ExpectedVersion: claimed.Version,
		Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	}); err != nil {
		t.Fatalf("approve the first revision: %v", err)
	}
	if _, err := chain.reviews.PublishRevision(ctx, seriesID, first.ID, "operator"); err != nil {
		t.Fatalf("publish the first revision: %v", err)
	}
	shelf, err := web.GetPublished(ctx, seriesID)
	if err != nil {
		t.Fatalf("read the published series: %v", err)
	}
	if shelf.RevisionID != first.ID || shelf.Title != "测试剧集" {
		t.Fatalf("the shelf serves %+v", shelf)
	}

	// 2. A new revision is submitted and rejected with a reason. The shelf
	// keeps serving the first revision while that happens.
	secondView, err := chain.catalog.CreateRevision(ctx, seriesID, first.ID, 0, 0)
	if err != nil {
		t.Fatalf("create the second revision: %v", err)
	}
	second := secondView.Revision
	secondGrant := seedResubmitGrant(t, chain.client, seriesID, "AUTH-CHAIN-002")
	chain.freezeRevision(t, second.ID, chain.revisionVersion(t, second.ID), secondGrant.ID, "operator")
	chain.runWorker(t)
	secondTasks := chain.reviewTasks(t, second.ID)
	if len(secondTasks) != 1 {
		t.Fatalf("review tasks of the second revision: %d", len(secondTasks))
	}
	// The author cannot decide their own submission.
	if _, err := chain.reviews.ClaimReview(ctx, secondTasks[0].ID, int(secondTasks[0].Version), "operator"); !errors.Is(err, biz.ErrReviewSelfDecision) {
		t.Fatalf("the author claimed their own submission: %v", err)
	}
	claimedSecond, err := chain.reviews.ClaimReview(ctx, secondTasks[0].ID, int(secondTasks[0].Version), "reviewer")
	if err != nil {
		t.Fatalf("claim the second review: %v", err)
	}
	if _, err := chain.reviews.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: secondTasks[0].ID, ExpectedVersion: claimedSecond.Version,
		Decision: biz.DecisionReject, Reason: "第1集字幕缺失", Actor: "reviewer",
	}); err != nil {
		t.Fatalf("reject the second revision: %v", err)
	}
	// The old publication is untouched by the rejection.
	shelf, err = web.GetPublished(ctx, seriesID)
	if err != nil {
		t.Fatalf("the shelf disappeared after a rejection: %v", err)
	}
	if shelf.RevisionID != first.ID {
		t.Fatalf("the shelf switched to %d before a publication", shelf.RevisionID)
	}

	// 3. The operator creates a new draft from the rejected revision, edits
	// it and submits it again.
	thirdView, err := chain.catalog.CreateRevision(ctx, seriesID, second.ID, 0, 0)
	if err != nil {
		t.Fatalf("create the third revision: %v", err)
	}
	third := thirdView.Revision
	edited, err := chain.catalog.UpdateRevision(ctx, seriesID, third.ID, third.Version, true, "测试剧集（修订版）", false, "")
	if err != nil {
		t.Fatalf("edit the third revision: %v", err)
	}
	thirdGrant := seedResubmitGrant(t, chain.client, seriesID, "AUTH-CHAIN-003")
	chain.freezeRevision(t, third.ID, edited.Revision.Version, thirdGrant.ID, "operator")
	chain.runWorker(t)
	// The whole chain processed the file once: every later revision reused
	// the material it inherited.
	if prepared := chain.processor.prepares.Load(); prepared != 1 {
		t.Fatalf("the chain transcoded %d times, want the inherited material reused", prepared)
	}
	thirdTasks := chain.reviewTasks(t, third.ID)
	if len(thirdTasks) != 1 {
		t.Fatalf("review tasks of the third revision: %d", len(thirdTasks))
	}
	claimedThird, err := chain.reviews.ClaimReview(ctx, thirdTasks[0].ID, int(thirdTasks[0].Version), "reviewer")
	if err != nil {
		t.Fatalf("claim the third review: %v", err)
	}
	if _, err := chain.reviews.DecideReview(ctx, biz.DecideReviewRequest{
		TaskID: thirdTasks[0].ID, ExpectedVersion: claimedThird.Version,
		Decision: biz.DecisionApprove, Actor: "reviewer", Checklist: approvedChecklist(),
	}); err != nil {
		t.Fatalf("approve the third revision: %v", err)
	}
	// Approval alone does not publish.
	shelf, err = web.GetPublished(ctx, seriesID)
	if err != nil || shelf.RevisionID != first.ID {
		t.Fatalf("the shelf switched before the publication: %+v %v", shelf, err)
	}
	if _, err := chain.reviews.PublishRevision(ctx, seriesID, third.ID, "operator"); err != nil {
		t.Fatalf("publish the third revision: %v", err)
	}
	shelf, err = web.GetPublished(ctx, seriesID)
	if err != nil {
		t.Fatalf("read the republished series: %v", err)
	}
	if shelf.RevisionID != third.ID || shelf.Title != "测试剧集（修订版）" {
		t.Fatalf("the shelf serves %+v", shelf)
	}
	if len(shelf.Episodes) != 1 || shelf.Episodes[0].AssetID == 0 {
		t.Fatalf("the republished revision has no playable episode: %+v", shelf.Episodes)
	}

	// 4. The history is intact: the first decision is still an approval, the
	// rejection keeps its reason, and the replaced publication is retired.
	for _, check := range []struct {
		taskID int64
		status string
	}{
		{firstTask[0].ID, biz.ReviewApproved},
		{secondTasks[0].ID, biz.ReviewRejected},
		{thirdTasks[0].ID, biz.ReviewApproved},
	} {
		row, err := chain.client.ReviewTask.Get(ctx, check.taskID)
		if err != nil {
			t.Fatalf("read review task %d: %v", check.taskID, err)
		}
		if row.Status != check.status {
			t.Fatalf("review task %d is %s, want %s", check.taskID, row.Status, check.status)
		}
	}
	rejection, err := chain.client.ReviewDecision.Query().
		Where(reviewdecision.ReviewTaskIDEQ(secondTasks[0].ID)).
		Only(ctx)
	if err != nil {
		t.Fatalf("read the rejection: %v", err)
	}
	if rejection.Decision != biz.DecisionReject || rejection.Reason != "第1集字幕缺失" {
		t.Fatalf("the rejection was rewritten: %+v", rejection)
	}
	oldRevision, err := chain.client.ContentRevision.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("read the rejected revision: %v", err)
	}
	if oldRevision.State != biz.RevisionStateRejected || oldRevision.Title != "测试剧集" {
		t.Fatalf("the rejected revision was rewritten: %+v", oldRevision)
	}
	shelfRevision, err := chain.client.ContentRevision.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("read the first revision: %v", err)
	}
	if shelfRevision.State != biz.RevisionStateApproved {
		t.Fatalf("the first revision changed: %s", shelfRevision.State)
	}

	// 5. The authorization gate still applies to the published revision: a
	// revoked grant takes the series off the public surface immediately.
	grants := data.NewAuthorizationRepoWithDB(chain.client)
	if _, err := grants.RevokeGrant(ctx, thirdGrant.ID, 1, "admin", "授权到期撤回"); err != nil {
		t.Fatalf("revoke the grant: %v", err)
	}
	if _, err := web.GetPublished(ctx, seriesID); !errors.Is(err, biz.ErrWebUnavailable) {
		t.Fatalf("a revoked authorization still serves the series: %v", err)
	}
}
