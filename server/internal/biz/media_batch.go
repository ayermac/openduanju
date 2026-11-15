package biz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MediaBatchStatus is the lifecycle of a persistent media batch.
type MediaBatchStatus string

// Batch states. A batch only reaches pending_review when every selected
// episode and the cover are ready.
const (
	MediaBatchQueued          MediaBatchStatus = "queued"
	MediaBatchRunning         MediaBatchStatus = "running"
	MediaBatchWaiting         MediaBatchStatus = "waiting"
	MediaBatchPartiallyFailed MediaBatchStatus = "partially_failed"
	MediaBatchFailed          MediaBatchStatus = "failed"
	MediaBatchSucceeded       MediaBatchStatus = "succeeded"
	MediaBatchCancelling      MediaBatchStatus = "cancelling"
	MediaBatchCancelled       MediaBatchStatus = "cancelled"
	MediaBatchPendingReview   MediaBatchStatus = "pending_review"
)

// MediaItemStatus is the state of one episode task inside a batch.
type MediaItemStatus string

// Item states, in execution order.
const (
	MediaItemQueued       MediaItemStatus = "queued"
	MediaItemResolving    MediaItemStatus = "resolving"
	MediaItemAcquiring    MediaItemStatus = "acquiring"
	MediaItemValidating   MediaItemStatus = "validating"
	MediaItemProcessing   MediaItemStatus = "processing"
	MediaItemUploading    MediaItemStatus = "uploading"
	MediaItemVerifying    MediaItemStatus = "verifying"
	MediaItemReady        MediaItemStatus = "ready"
	MediaItemWaitingRetry MediaItemStatus = "waiting_retry"
	MediaItemFailed       MediaItemStatus = "failed"
	MediaItemCancelled    MediaItemStatus = "cancelled"
)

// Cover states of a batch. The cover is a separate input with its own
// upload session, so it can lag behind the episodes.
const (
	MediaCoverPending string = "pending"
	MediaCoverReady   string = "ready"
	MediaCoverFailed  string = "failed"
)

// MediaAssetState separates object confirmation from business readiness.
const (
	// MediaAssetReady marks an asset that is confirmed and linked to a
	// revision episode in the same transaction.
	MediaAssetReady MediaAssetState = "ready"
	// MediaAssetOrphan marks an object that is no longer referenced.
	MediaAssetOrphan MediaAssetState = "orphan"
)

// Revision states a batch drives.
const (
	// RevisionStateDraft is an editable revision.
	RevisionStateDraft = "draft"
	// RevisionStateProcessing is a revision frozen by a running batch. The
	// selection may not be changed in this state.
	RevisionStateProcessing = "processing"
	// RevisionStatePendingReview is a revision whose material is ready and
	// that waits for a review decision.
	RevisionStatePendingReview = "pending_review"
	// RevisionStateApproved is a revision the review accepted. It is not
	// published: publication is a separate, explicit step.
	RevisionStateApproved = "approved"
	// RevisionStateRejected is a revision the review refused. It is history:
	// its material and its metadata stay as they were judged, so the operator
	// creates a new draft from it instead of editing it in place.
	RevisionStateRejected = "rejected"
)

// MediaBatch is one persistent processing run over a frozen revision.
type MediaBatch struct {
	ID              int64
	SeriesID        int64
	RevisionID      int64
	SelectionHash   string
	ProfileVersion  string
	InputKind       string
	Status          MediaBatchStatus
	CoverStatus     string
	ItemTotal       int
	ItemReady       int
	ItemFailed      int
	CancelRequested bool
	Version         int
	ErrorCode       string
	ErrorMessage    string
	CreatedBy       string
	// TraceID is the identity of the request that submitted the batch.
	TraceID      string
	ReviewTaskID int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// MediaItem is one episode task of a batch. The lease fields fence stale
// workers: only the worker that holds the current generation may commit.
type MediaItem struct {
	ID         int64
	BatchID    int64
	EpisodeID  int64
	ExternalID string
	EpisodeNo  int
	// TraceID is the identity of the submission that created the task; the
	// worker logs it with the stage it is working on.
	TraceID         string
	Status          MediaItemStatus
	Attempt         int
	Retryable       bool
	NextEligibleAt  *time.Time
	ErrorCode       string
	ErrorMessage    string
	LeaseGeneration int
	LeaseOwner      string
	LeaseUntil      *time.Time
	InputVersion    int
	// InputUploadID names the confirmed upload that feeds this task instead
	// of the resolver. Zero keeps the SDK path.
	InputUploadID int64
	AssetID       int64
	Object        ObjectRef
	// InputArtifactPath and InputArtifactSHA256 point at the downloaded
	// input that may be reused by a resumed task.
	InputArtifactPath   string
	InputArtifactSHA256 string
	// OutputArtifactPath, OutputArtifactSHA256 and OutputArtifactSize point
	// at the processed file that may be reused by a resumed task.
	OutputArtifactPath   string
	OutputArtifactSHA256 string
	OutputArtifactSize   int64
	SHA256               string
	SizeBytes            int64
	ContentType          string
	Codec                string
	DurationMS           int64
	Width                int
	Height               int
	Version              int
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// MediaEpisodeTarget is one selected episode a batch has to process.
type MediaEpisodeTarget struct {
	ID         int64
	ExternalID string
	Number     int
}

// InheritedAsset is the ready material a new revision inherited with its
// selection, together with the task that produced it.
//
// The producing task is what proves the material: it names the confirmed
// input the bytes came from and the processing profile they were produced
// with. A worker compares both with its own task before it reuses the
// material, so a changed input is processed again instead of being served
// from an older object.
type InheritedAsset struct {
	Asset MediaAsset
	// ProducerInputUploadID is the confirmed manual upload the asset was
	// processed from; zero means the episode was resolved through the source.
	ProducerInputUploadID int64
	// ProducerExternalID is the source episode the asset was resolved from.
	ProducerExternalID string
	// ProducerProfile is the processing profile of the producing batch.
	ProducerProfile string
}

// BatchQuery filters media batches.
type BatchQuery struct {
	SeriesID   int64
	RevisionID int64
	Status     string
	Page       int
	PageSize   int
}

// ItemQuery filters the items of a batch.
type ItemQuery struct {
	BatchID  int64
	Status   string
	Page     int
	PageSize int
}

// FreezeBatchRequest asks for a batch over a frozen revision. The grant is
// chosen explicitly: a revision never inherits an authorization.
type FreezeBatchRequest struct {
	RevisionID                   int64
	ExpectedVersion              int
	ProfileVersion               string
	InputKind                    string
	Actor                        string
	AuthorizationGrantID         int64
	ExpectedAuthorizationVersion int
}

// SubmitBatchRequest asks for a persistent batch over a frozen revision.
type SubmitBatchRequest struct {
	RevisionID                   int64
	ExpectedVersion              int
	ProfileVersion               string
	InputKind                    string
	Actor                        string
	AuthorizationGrantID         int64
	ExpectedAuthorizationVersion int
}

// Media batch failures.
var (
	ErrBatchNotFound       = errors.New("media batch not found")
	ErrBatchVersion        = errors.New("media batch version conflict")
	ErrBatchNotCancellable = errors.New("media batch cannot be cancelled")
	ErrBatchNotRetryable   = errors.New("media batch has no retryable item")
	ErrBatchAlreadyActive  = errors.New("media batch is already active")
	ErrRevisionNotEditable = errors.New("revision cannot be processed")
	ErrNoSelectedEpisodes  = errors.New("revision has no selected episode")
	ErrProfileRequired     = errors.New("processing profile is required")
	// ErrVersionRequired reports a submission without the expected revision
	// version. The version guards a submission against a revision that moved
	// on, so it may not be skipped.
	ErrVersionRequired = errors.New("expected revision version is required")
	ErrItemLeaseLost   = errors.New("media item lease was lost")
	ErrBatchCancelled  = errors.New("media batch was cancelled")
	// ErrRevisionNotOwned reports that the revision of a batch left the
	// processing state: it was withdrawn, replaced or already reviewed. A
	// batch may not advance such a revision any more.
	ErrRevisionNotOwned = errors.New("revision is no longer owned by the batch")
	// ErrBatchInputNotReady reports a selected episode whose manual upload
	// was declared but not confirmed by the server.
	ErrBatchInputNotReady = errors.New("selected episode has no confirmed manual input")
	// ErrBatchAuthorizationInvalid reports a batch whose frozen grant is no
	// longer in force or no longer allows the next step.
	ErrBatchAuthorizationInvalid = errors.New("the frozen authorization is not in force")
)

// MediaBatchRepo persists batches, items and assets.
type MediaBatchRepo interface {
	// FreezeAndCreateBatch locks the revision, checks the expected version,
	// freezes the selected set and the processing parameters and creates the
	// batch with one task per selected episode. The revision leaves the draft
	// state in the same transaction, so the selection cannot be changed while
	// the batch runs.
	FreezeAndCreateBatch(ctx context.Context, request FreezeBatchRequest) (*MediaBatch, error)
	GetBatch(ctx context.Context, id int64) (*MediaBatch, error)
	GetBatchByRevision(ctx context.Context, revisionID int64) (*MediaBatch, error)
	ListBatches(ctx context.Context, query BatchQuery) ([]MediaBatch, int, error)
	ListItems(ctx context.Context, query ItemQuery) ([]MediaItem, int, error)
	GetItem(ctx context.Context, id int64) (*MediaItem, error)
	ClaimItems(ctx context.Context, worker string, lease time.Duration, limit int) ([]MediaItem, error)
	SaveItem(ctx context.Context, item MediaItem) (*MediaItem, error)
	// RenewItemLease extends the lease of an item the worker still owns. A
	// worker that lost the lease gets ErrItemLeaseLost.
	RenewItemLease(ctx context.Context, item MediaItem, lease time.Duration) (*MediaItem, error)
	CommitItemReady(ctx context.Context, item MediaItem, asset MediaAsset) (*MediaItem, *MediaBatch, error)
	FailItem(ctx context.Context, item MediaItem, code MediaErrorCode, message string, retryable bool, nextEligibleAt *time.Time) (*MediaItem, error)
	RequestCancel(ctx context.Context, id int64, expectedVersion int, reason string) (*MediaBatch, error)
	RetryItems(ctx context.Context, id int64, expectedVersion int, itemIDs []int64) (*MediaBatch, error)
	RefreshBatchStatus(ctx context.Context, id int64) (*MediaBatch, error)
	// RefreshReadyBatches re-evaluates the review gate for the batches whose
	// items are all ready. It closes the gap between the cover upload and the
	// review task: the party that marks the cover ready does not have to know
	// how the gate is applied.
	RefreshReadyBatches(ctx context.Context, limit int) (int, error)
	// EnsureBatchAuthorization re-checks the frozen grant before a task starts
	// or commits ready. A revoked or expired grant stops the work.
	EnsureBatchAuthorization(ctx context.Context, batchID int64, permission Permission) error
	// InheritedAssetForItem returns the ready material the revision of a
	// task inherited with its selection, or nil when the task has none. The
	// material is a candidate only: the worker verifies the stored object and
	// the processing parameters before it reuses it.
	InheritedAssetForItem(ctx context.Context, item MediaItem) (*InheritedAsset, error)
}

// MediaContentRepo reads the frozen content a batch is built from.
type MediaContentRepo interface {
	GetRevisionForMedia(ctx context.Context, id int64) (*ContentRevision, error)
	ListSelectedEpisodes(ctx context.Context, revisionID int64) ([]MediaEpisodeTarget, error)
}

// MediaBatches is the use case over persistent media batches.
type MediaBatches struct {
	repo    MediaBatchRepo
	content MediaContentRepo
	audit   *Auditor
}

// NewMediaBatches builds the use case. The auditor may be nil in a unit test
// that checks business rules only.
func NewMediaBatches(repo MediaBatchRepo, content MediaContentRepo, auditor *Auditor) *MediaBatches {
	return &MediaBatches{repo: repo, content: content, audit: auditor}
}

// SubmitRevision freezes the current revision selection and creates the
// batch with one task per selected episode. The revision itself is not
// modified; the batch keeps the selection it was created with.
func (m *MediaBatches) SubmitRevision(ctx context.Context, req SubmitBatchRequest) (batch *MediaBatch, err error) {
	ctx, scope := m.audit.Begin(ctx, AuditActionBatchSubmit, AuditResourceBatch)
	defer func() { scope.End(ctx, err) }()
	profile := strings.TrimSpace(req.ProfileVersion)
	if profile == "" {
		return nil, ErrProfileRequired
	}
	if req.ExpectedVersion <= 0 {
		return nil, ErrVersionRequired
	}
	// The revision is read once to answer a missing revision early. The
	// authoritative check - version, draft state, selected set - happens
	// under the lock of the freeze transaction, because the selection must
	// not change between the check and the freeze.
	revision, err := m.content.GetRevisionForMedia(ctx, req.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, ErrRevisionNotFound
	}
	// An authorization is chosen explicitly for every submission; a revision
	// never inherits one from the series or from a previous batch.
	if req.AuthorizationGrantID <= 0 {
		return nil, ErrGrantNotFound
	}
	inputKind := strings.TrimSpace(req.InputKind)
	if inputKind == "" {
		inputKind = "sdk"
	}
	return m.repo.FreezeAndCreateBatch(ctx, FreezeBatchRequest{
		RevisionID:                   req.RevisionID,
		ExpectedVersion:              req.ExpectedVersion,
		ProfileVersion:               profile,
		InputKind:                    inputKind,
		Actor:                        req.Actor,
		AuthorizationGrantID:         req.AuthorizationGrantID,
		ExpectedAuthorizationVersion: req.ExpectedAuthorizationVersion,
	})
}

// GetBatch returns one batch.
func (m *MediaBatches) GetBatch(ctx context.Context, id int64) (*MediaBatch, error) {
	batch, err := m.repo.GetBatch(ctx, id)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, ErrBatchNotFound
	}
	return batch, nil
}

// ListBatches returns the batches matching the query.
func (m *MediaBatches) ListBatches(ctx context.Context, query BatchQuery) ([]MediaBatch, int, error) {
	return m.repo.ListBatches(ctx, query)
}

// ListItems returns the items of a batch.
func (m *MediaBatches) ListItems(ctx context.Context, query ItemQuery) ([]MediaItem, int, error) {
	if _, err := m.GetBatch(ctx, query.BatchID); err != nil {
		return nil, 0, err
	}
	return m.repo.ListItems(ctx, query)
}

// CancelBatch requests the cancellation of a batch. In-flight work stops at
// the next checkpoint; already stored objects are not deleted.
func (m *MediaBatches) CancelBatch(ctx context.Context, id int64, expectedVersion int, reason string) (batch *MediaBatch, err error) {
	ctx, scope := m.audit.Begin(ctx, AuditActionBatchCancel, AuditResourceBatch)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	batch, err = m.repo.GetBatch(ctx, id)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, ErrBatchNotFound
	}
	switch batch.Status {
	case MediaBatchQueued, MediaBatchRunning, MediaBatchWaiting, MediaBatchPartiallyFailed:
	default:
		return nil, ErrBatchNotCancellable
	}
	return m.repo.RequestCancel(ctx, id, expectedVersion, reason)
}

// RetryFailures requeues the failed items of a batch. Items that already
// reached ready are never reprocessed.
func (m *MediaBatches) RetryFailures(ctx context.Context, id int64, expectedVersion int, itemIDs []int64) (batch *MediaBatch, err error) {
	ctx, scope := m.audit.Begin(ctx, AuditActionBatchRetry, AuditResourceBatch)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	batch, err = m.repo.GetBatch(ctx, id)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, ErrBatchNotFound
	}
	switch batch.Status {
	case MediaBatchFailed, MediaBatchPartiallyFailed, MediaBatchWaiting, MediaBatchSucceeded:
	default:
		return nil, ErrBatchAlreadyActive
	}
	return m.repo.RetryItems(ctx, id, expectedVersion, itemIDs)
}

// SelectionHash fingerprints the selected episodes of a batch. The hash is
// stored with the batch, so a later change of the revision selection is
// visible as a mismatch.
func SelectionHash(episodes []MediaEpisodeTarget) string {
	parts := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		parts = append(parts, fmt.Sprintf("%d:%s", episode.Number, episode.ExternalID))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%x", sum[:])
}
