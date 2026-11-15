package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UploadKind separates an episode file from a cover image. A cover may never
// enter the episode pipeline: the two have different validation and only an
// episode input becomes publishable material after processing.
const (
	UploadKindEpisode = "episode_input"
	UploadKindCover   = "cover"
)

// Upload session states.
const (
	UploadPending   = "pending"
	UploadCompleted = "completed"
	UploadCancelled = "cancelled"
)

// Input states of a revision episode. A confirmed upload is an input, not a
// ready asset: the batch still validates, processes, uploads and confirms it.
const (
	InputStateReady = "ready"
)

// UploadSession is one short-lived upload of an episode file or a cover.
type UploadSession struct {
	ID              int64
	RevisionID      int64
	EpisodeID       int64
	Kind            string
	Status          string
	Version         int
	Provider        string
	Bucket          string
	Region          string
	StagingKey      string
	ObjectKey       string
	ObjectVersion   string
	Filename        string
	ContentType     string
	SizeBytes       int64
	ExpectedSHA256  string
	SHA256          string
	Width           int
	Height          int
	AssetID         int64
	CreatedBy       string
	RevisionVersion int
	ExpiresAt       time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// UploadAuthorization is the short-lived credential for exactly one staging
// object. It carries no permanent secret: the caller may only write the key
// of its own session.
type UploadAuthorization struct {
	URL       string
	Method    string
	Headers   map[string]string
	ExpiresAt time.Time
}

// UploadView is a session together with the authorization of the moment.
type UploadView struct {
	Session       *UploadSession
	Authorization *UploadAuthorization
}

// UploadQuery filters upload sessions of a revision.
type UploadQuery struct {
	RevisionID int64
	Kind       string
	Status     string
	Page       int
	PageSize   int
}

// CreateUploadRequest asks for a staging object and a short-lived credential.
type CreateUploadRequest struct {
	RevisionID              int64
	ExpectedRevisionVersion int
	EpisodeID               int64
	Kind                    string
	Filename                string
	SizeBytes               int64
	ContentType             string
	ExpectedSHA256          string
	Actor                   string
}

// CreateUploadRecord is the validated request a repository persists.
type CreateUploadRecord struct {
	RevisionID      int64
	EpisodeID       int64
	Kind            string
	Filename        string
	ContentType     string
	SizeBytes       int64
	ExpectedSHA256  string
	StagingKey      string
	Provider        string
	Bucket          string
	Region          string
	Actor           string
	RevisionVersion int
	ExpiresAt       time.Time
}

// CompleteCoverRecord registers a confirmed cover object against a revision.
type CompleteCoverRecord struct {
	SessionID       int64
	ExpectedVersion int
	Object          ObjectRef
	SHA256          string
	SizeBytes       int64
	ContentType     string
	Width           int
	Height          int
}

// CompletedInput is the confirmed object an episode task reads instead of
// resolving the source SDK.
//
// The episode identity travels with the object: a new revision copies the
// episodes of its base revision, so a task may name a copied episode whose
// confirmed upload still belongs to the same episode of the same series.
type CompletedInput struct {
	SessionID  int64
	RevisionID int64
	SeriesID   int64
	EpisodeID  int64
	EpisodeNo  int
	ExternalID string
	Kind       string
	Object     ObjectRef
	SizeBytes  int64
	SHA256     string
	Status     string
}

// UploadAcceptsRevision reports whether a revision state may open or confirm
// a session of this kind.
//
// An episode file may only enter while the revision is a draft: a frozen
// revision has fixed inputs, so a late file cannot replace what the batch was
// created with. A cover is a separate input that the gate waits for, so it may
// still arrive while the revision is being processed. Neither is accepted once
// the revision was withdrawn or replaced.
func UploadAcceptsRevision(kind string, state string) bool {
	if kind == UploadKindCover {
		return state == RevisionStateDraft || state == RevisionStateProcessing
	}
	return state == RevisionStateDraft
}

// UploadChecksRevisionVersion reports whether a session is tied to the exact
// revision version. An episode input is: a changed selection invalidates it.
// A cover is not: freezing a revision advances its version, and the cover the
// operator is uploading is still the cover of that revision.
func UploadChecksRevisionVersion(kind string) bool {
	return kind != UploadKindCover
}

// Upload failures.
var (
	ErrUploadNotFound         = errors.New("upload session not found")
	ErrUploadExpired          = errors.New("upload session expired")
	ErrUploadNotPending       = errors.New("upload session is not pending")
	ErrUploadAlreadyBound     = errors.New("upload session is already bound")
	ErrUploadRevisionChanged  = errors.New("revision changed since the upload session was created")
	ErrUploadEpisodeInvalid   = errors.New("episode does not belong to the revision")
	ErrUploadKindInvalid      = errors.New("upload kind is invalid")
	ErrUploadSizeInvalid      = errors.New("upload size is invalid")
	ErrUploadTooLarge         = errors.New("upload exceeds the configured limit")
	ErrUploadObjectMissing    = errors.New("uploaded object is missing")
	ErrUploadObjectSize       = errors.New("uploaded object size does not match")
	ErrUploadObjectDigest     = errors.New("uploaded object digest does not match")
	ErrUploadObjectMismatch   = errors.New("uploaded object does not match the session")
	ErrCoverImageInvalid      = errors.New("cover image is invalid")
	ErrCoverImageUnsupported  = errors.New("cover image type is not supported")
	ErrUploadActorNotOwner    = errors.New("the upload session belongs to another operator")
	ErrUploadVersionRequired  = errors.New("expected upload version is required")
	ErrUploadStateUnsupported = errors.New("upload session state does not allow this operation")
)

// UploadRepo persists upload sessions and the input they register.
type UploadRepo interface {
	CreateUpload(ctx context.Context, record CreateUploadRecord) (*UploadSession, error)
	GetUpload(ctx context.Context, id int64) (*UploadSession, error)
	ListUploads(ctx context.Context, query UploadQuery) ([]UploadSession, int, error)
	RenewUpload(ctx context.Context, id int64, expectedVersion int, actor string, expiresAt time.Time) (*UploadSession, error)
	CompleteUploadInput(ctx context.Context, sessionID int64, expectedVersion int, actor string, object ObjectRef, sha256 string, sizeBytes int64, contentType string) (*UploadSession, error)
	CompleteUploadCover(ctx context.Context, record CompleteCoverRecord, actor string) (*UploadSession, error)
	CancelUpload(ctx context.Context, id int64, expectedVersion int, actor string) (*UploadSession, error)
	// GetCompletedInput returns the confirmed object of an episode upload. A
	// worker reads it instead of the resolver and rejects a session that is
	// not completed.
	GetCompletedInput(ctx context.Context, sessionID int64) (*CompletedInput, error)
}

// MediaInputReader is the narrow view the media worker needs: the confirmed
// object of the upload a task was frozen with.
type MediaInputReader interface {
	GetCompletedInput(ctx context.Context, sessionID int64) (*CompletedInput, error)
}

// UploadContentRepo reads the revision and episode an upload belongs to.
type UploadContentRepo interface {
	// GetRevisionForUpload returns the revision state and version.
	GetRevisionForUpload(ctx context.Context, revisionID int64) (*ContentRevision, error)
	// HasRevisionEpisode reports whether the episode belongs to the revision.
	HasRevisionEpisode(ctx context.Context, revisionID int64, episodeID int64) (bool, error)
}

// UploadOptions are the tunables of manual upload.
type UploadOptions struct {
	// TTL is how long one staging authorization lives.
	TTL time.Duration
	// MaxEpisodeBytes and MaxCoverBytes bound the declared size.
	MaxEpisodeBytes int64
	MaxCoverBytes   int64
	// KeyPrefix names the staging namespace inside the bucket.
	KeyPrefix string
	// TempDir is the root for the download used to validate a cover.
	TempDir string
	// MinCoverSide and MaxCoverSide bound the cover dimension.
	MinCoverSide int
	MaxCoverSide int
	// Provider, Bucket and Region identify the storage a session writes to.
	// They are persisted with the session so a worker can read the object
	// without re-deriving the configuration.
	Provider string
	Bucket   string
	Region   string
}

// Uploads is the use case over manual uploads.
type Uploads struct {
	repo    UploadRepo
	content UploadContentRepo
	storage ObjectStorage
	opts    UploadOptions
}

// NewUploads builds the upload use case.
func NewUploads(repo UploadRepo, content UploadContentRepo, storage ObjectStorage, opts UploadOptions) *Uploads {
	if opts.TTL <= 0 {
		opts.TTL = 15 * time.Minute
	}
	if opts.MaxEpisodeBytes <= 0 {
		opts.MaxEpisodeBytes = 4 << 30
	}
	if opts.MaxCoverBytes <= 0 {
		opts.MaxCoverBytes = 10 << 20
	}
	if opts.MinCoverSide <= 0 {
		opts.MinCoverSide = 240
	}
	if opts.MaxCoverSide <= 0 {
		opts.MaxCoverSide = 10000
	}
	return &Uploads{repo: repo, content: content, storage: storage, opts: opts}
}

// Create opens a staging session and signs exactly one object key.
func (u *Uploads) Create(ctx context.Context, req CreateUploadRequest) (*UploadView, error) {
	kind := strings.TrimSpace(req.Kind)
	if kind != UploadKindEpisode && kind != UploadKindCover {
		return nil, ErrUploadKindInvalid
	}
	if req.SizeBytes <= 0 {
		return nil, ErrUploadSizeInvalid
	}
	limit := u.opts.MaxEpisodeBytes
	if kind == UploadKindCover {
		limit = u.opts.MaxCoverBytes
		if req.EpisodeID != 0 {
			return nil, ErrUploadEpisodeInvalid
		}
	}
	if req.SizeBytes > limit {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrUploadTooLarge, limit)
	}
	if kind == UploadKindEpisode && req.EpisodeID <= 0 {
		return nil, ErrUploadEpisodeInvalid
	}
	if req.ExpectedRevisionVersion <= 0 {
		return nil, ErrVersionRequired
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("%w: filename is required", ErrUploadSizeInvalid)
	}
	session, err := u.repo.CreateUpload(ctx, CreateUploadRecord{
		RevisionID:      req.RevisionID,
		EpisodeID:       req.EpisodeID,
		Kind:            kind,
		Filename:        safeFilename(req.Filename),
		ContentType:     strings.TrimSpace(req.ContentType),
		SizeBytes:       req.SizeBytes,
		ExpectedSHA256:  strings.ToLower(strings.TrimSpace(req.ExpectedSHA256)),
		StagingKey:      u.stagingKey(req),
		Provider:        u.opts.Provider,
		Bucket:          u.opts.Bucket,
		Region:          u.opts.Region,
		Actor:           req.Actor,
		RevisionVersion: req.ExpectedRevisionVersion,
		ExpiresAt:       time.Now().Add(u.opts.TTL),
	})
	if err != nil {
		return nil, err
	}
	auth, err := u.sign(ctx, session)
	if err != nil {
		return nil, err
	}
	return &UploadView{Session: session, Authorization: auth}, nil
}

// Renew signs the same staging key again. A chunked upload continues on one
// object identity; the session version guards a concurrent completion.
func (u *Uploads) Renew(ctx context.Context, id int64, expectedVersion int, actor string) (*UploadView, error) {
	if expectedVersion <= 0 {
		return nil, ErrUploadVersionRequired
	}
	session, err := u.repo.RenewUpload(ctx, id, expectedVersion, actor, time.Now().Add(u.opts.TTL))
	if err != nil {
		return nil, err
	}
	auth, err := u.sign(ctx, session)
	if err != nil {
		return nil, err
	}
	return &UploadView{Session: session, Authorization: auth}, nil
}

// Complete confirms the stored object and registers a trusted input. The
// client callback is never trusted on its own: the server reads the object
// before anything is bound to the revision.
func (u *Uploads) Complete(ctx context.Context, id int64, expectedVersion int, actor string) (*UploadSession, error) {
	if expectedVersion <= 0 {
		return nil, ErrUploadVersionRequired
	}
	session, err := u.repo.GetUpload(ctx, id)
	if err != nil {
		return nil, err
	}
	if session.Status == UploadCompleted {
		// A repeated confirmation returns the same session without binding a
		// second input.
		return session, nil
	}
	if session.Status != UploadPending {
		return nil, ErrUploadNotPending
	}
	if err := checkUploadOwner(session, actor); err != nil {
		return nil, err
	}
	now := time.Now()
	if now.After(session.ExpiresAt) {
		return nil, ErrUploadExpired
	}
	// The revision must still be the one the session was opened for. A
	// revision that moved on - a changed selection, a withdrawal, a
	// replacement - rejects the confirmation instead of binding an old file.
	revision, err := u.content.GetRevisionForUpload(ctx, session.RevisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil {
		return nil, ErrRevisionNotFound
	}
	if !UploadAcceptsRevision(session.Kind, revision.State) {
		return nil, fmt.Errorf("%w: state %s", ErrUploadRevisionChanged, revision.State)
	}
	if UploadChecksRevisionVersion(session.Kind) && revision.Version != session.RevisionVersion {
		return nil, fmt.Errorf("%w: revision version %d, session version %d", ErrUploadRevisionChanged, revision.Version, session.RevisionVersion)
	}
	if u.storage == nil {
		return nil, NewMediaError(MediaStageUpload, MediaCodeConfigInvalid, false, errors.New("object storage is not configured"))
	}
	ref := sessionObject(session)
	head, err := u.storage.Head(ctx, ref)
	if err != nil {
		if isCode(err, MediaCodeStorageObjectMissing) {
			return nil, ErrUploadObjectMissing
		}
		return nil, err
	}
	if head == nil {
		return nil, ErrUploadObjectMissing
	}
	if head.SizeBytes != session.SizeBytes {
		return nil, fmt.Errorf("%w: declared %d, stored %d", ErrUploadObjectSize, session.SizeBytes, head.SizeBytes)
	}
	// The confirmed identity - including the object version the HEAD named -
	// is what the download and the record use from here on.
	if session.Kind == UploadKindCover {
		return u.completeCover(ctx, session, head.ObjectRef, expectedVersion, actor)
	}
	return u.completeEpisodeInput(ctx, session, head.ObjectRef, expectedVersion, actor)
}

// completeEpisodeInput verifies the bytes and registers the input. The video
// is not ready material: a batch still validates, processes and uploads it.
func (u *Uploads) completeEpisodeInput(ctx context.Context, session *UploadSession, ref ObjectRef, expectedVersion int, actor string) (*UploadSession, error) {
	downloaded, err := u.downloadStaged(ctx, session, ref)
	if err != nil {
		return nil, err
	}
	defer removeFile(downloaded.Path)
	return u.repo.CompleteUploadInput(ctx, session.ID, expectedVersion, actor,
		ref, downloaded.SHA256, downloaded.SizeBytes, downloaded.ContentType)
}

// completeCover validates the image itself, stores an immutable copy and
// binds it to the revision. A cover never reaches ready by declaration.
func (u *Uploads) completeCover(ctx context.Context, session *UploadSession, ref ObjectRef, expectedVersion int, actor string) (*UploadSession, error) {
	downloaded, err := u.downloadStaged(ctx, session, ref)
	if err != nil {
		return nil, err
	}
	defer removeFile(downloaded.Path)
	info, err := validateCoverFile(downloaded.Path, u.opts)
	if err != nil {
		return nil, err
	}
	key := CoverObjectKey(u.opts.KeyPrefix, downloaded.SHA256, info.Extension)
	stored, err := u.storage.PutFile(ctx, PutObjectRequest{
		Key:         key,
		Path:        downloaded.Path,
		ContentType: info.ContentType,
		SHA256:      downloaded.SHA256,
		SizeBytes:   downloaded.SizeBytes,
	})
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, NewMediaError(MediaStageUpload, MediaCodeStorageUnavailable, true, errors.New("storage returned no cover object"))
	}
	if stored.SizeBytes != downloaded.SizeBytes || !strings.EqualFold(stored.MetadataSHA256, downloaded.SHA256) {
		return nil, fmt.Errorf("%w: the stored cover does not match the validated file", ErrUploadObjectMismatch)
	}
	if stored.Key == "" {
		stored.Key = key
	}
	// The immutable copy is confirmed before the revision may reference it.
	confirmed, err := u.storage.Head(ctx, stored.ObjectRef)
	if err != nil {
		return nil, err
	}
	if confirmed == nil || confirmed.SizeBytes != downloaded.SizeBytes {
		return nil, fmt.Errorf("%w: the cover object is missing after the upload", ErrUploadObjectMissing)
	}
	return u.repo.CompleteUploadCover(ctx, CompleteCoverRecord{
		SessionID:       session.ID,
		ExpectedVersion: expectedVersion,
		Object:          confirmed.ObjectRef,
		SHA256:          downloaded.SHA256,
		SizeBytes:       downloaded.SizeBytes,
		ContentType:     info.ContentType,
		Width:           info.Width,
		Height:          info.Height,
	}, actor)
}

// downloadStaged copies the staging object to a local file and checks its
// size and, when declared, its digest. The read is bounded by the declared
// size, so an object that grew after the session was opened is rejected while
// it streams instead of after a full download.
func (u *Uploads) downloadStaged(ctx context.Context, session *UploadSession, ref ObjectRef) (*DownloadedFile, error) {
	downloaded, err := u.storage.GetToFile(ctx, ref, u.opts.TempDir, session.SizeBytes)
	if err != nil {
		if isCode(err, MediaCodeStorageObjectMissing) {
			return nil, ErrUploadObjectMissing
		}
		if isCode(err, MediaCodeMediaTooLarge) {
			return nil, fmt.Errorf("%w: declared %d, stored object is larger", ErrUploadObjectSize, session.SizeBytes)
		}
		return nil, err
	}
	if downloaded == nil || downloaded.Path == "" {
		return nil, ErrUploadObjectMissing
	}
	if downloaded.SizeBytes != session.SizeBytes {
		removeFile(downloaded.Path)
		return nil, fmt.Errorf("%w: declared %d, downloaded %d", ErrUploadObjectSize, session.SizeBytes, downloaded.SizeBytes)
	}
	if session.ExpectedSHA256 != "" && !strings.EqualFold(session.ExpectedSHA256, downloaded.SHA256) {
		removeFile(downloaded.Path)
		return nil, fmt.Errorf("%w: declared %s, downloaded %s", ErrUploadObjectDigest, session.ExpectedSHA256, downloaded.SHA256)
	}
	return downloaded, nil
}

// Cancel releases a pending session. A completed session is already bound
// and may not be cancelled.
func (u *Uploads) Cancel(ctx context.Context, id int64, expectedVersion int, actor string) (*UploadSession, error) {
	if expectedVersion <= 0 {
		return nil, ErrUploadVersionRequired
	}
	return u.repo.CancelUpload(ctx, id, expectedVersion, actor)
}

// Get returns one session.
func (u *Uploads) Get(ctx context.Context, id int64) (*UploadSession, error) {
	return u.repo.GetUpload(ctx, id)
}

// List returns the sessions of a revision.
func (u *Uploads) List(ctx context.Context, query UploadQuery) ([]UploadSession, int, error) {
	return u.repo.ListUploads(ctx, query)
}

// sign issues the short-lived credential of the session's own object.
func (u *Uploads) sign(ctx context.Context, session *UploadSession) (*UploadAuthorization, error) {
	if u.storage == nil {
		return nil, NewMediaError(MediaStageUpload, MediaCodeConfigInvalid, false, errors.New("object storage is not configured"))
	}
	presigned, err := u.storage.PresignPut(ctx, PresignPutRequest{
		Object:    sessionObject(session),
		SizeBytes: session.SizeBytes,
		SHA256:    session.ExpectedSHA256,
		TTL:       u.opts.TTL,
		SessionID: session.ID,
	})
	if err != nil {
		return nil, err
	}
	if presigned == nil || presigned.URL == "" {
		return nil, NewMediaError(MediaStageUpload, MediaCodeStorageUnavailable, true, errors.New("storage issued no upload authorization"))
	}
	return &UploadAuthorization{
		URL:       presigned.URL,
		Method:    presigned.Method,
		Headers:   presigned.Headers,
		ExpiresAt: presigned.ExpiresAt,
	}, nil
}

// stagingKey builds an unguessable key per session. The revision and episode
// are part of the path for operators; the random suffix keeps one operator
// from overwriting another operator's object.
func (u *Uploads) stagingKey(req CreateUploadRequest) string {
	prefix := strings.Trim(u.opts.KeyPrefix, "/")
	if prefix == "" {
		prefix = "openduanju-media"
	}
	suffix := randomToken(16)
	if req.Kind == UploadKindCover {
		return fmt.Sprintf("%s/uploads/%d/cover/%s", prefix, req.RevisionID, suffix)
	}
	return fmt.Sprintf("%s/uploads/%d/episodes/%d/%s", prefix, req.RevisionID, req.EpisodeID, suffix)
}

// sessionObject is the staging object identity of a session.
func sessionObject(session *UploadSession) ObjectRef {
	return ObjectRef{
		Provider:  session.Provider,
		Bucket:    session.Bucket,
		Region:    session.Region,
		Key:       session.StagingKey,
		VersionID: session.ObjectVersion,
	}
}

// checkUploadOwner keeps one operator from renewing or completing the
// session of another. The shared token is one identity for now; the staff
// slice replaces the check with a real account comparison.
func checkUploadOwner(session *UploadSession, actor string) error {
	if session.CreatedBy == "" || actor == "" {
		return nil
	}
	if session.CreatedBy != actor {
		return ErrUploadActorNotOwner
	}
	return nil
}

// safeFilename keeps the operator-facing file name and never builds a path
// from it. The name is diagnostics only; the object key is server-chosen.
func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// CoverObjectKey is the immutable, content-addressed key of a cover image.
func CoverObjectKey(prefix string, sha256 string, extension string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		prefix = "openduanju-media"
	}
	if extension == "" {
		extension = ".jpg"
	}
	return fmt.Sprintf("%s/covers/%s%s", prefix, sha256, extension)
}
