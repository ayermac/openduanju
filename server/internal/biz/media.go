package biz

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MediaStage identifies one step of the single-episode media pipeline. The
// values are persisted in run records and structured logs, so they are stable.
type MediaStage string

// Pipeline stages, in execution order.
const (
	MediaStageAcquire  MediaStage = "acquire"
	MediaStageDownload MediaStage = "download"
	MediaStageValidate MediaStage = "validate"
	MediaStageProcess  MediaStage = "process"
	MediaStageUpload   MediaStage = "upload"
	MediaStageVerify   MediaStage = "verify"
)

// MediaErrorCode is a stable, machine-readable failure reason. Transport
// layers map it to API error reasons; operators use it to decide whether a
// retry can succeed.
type MediaErrorCode string

// Classified media failure codes.
const (
	MediaCodeConfigInvalid         MediaErrorCode = "MEDIA_CONFIG_INVALID"
	MediaCodeSourceUnavailable     MediaErrorCode = "SOURCE_UNAVAILABLE"
	MediaCodeSourceRejected        MediaErrorCode = "SOURCE_REJECTED"
	MediaCodeSourceRateLimited     MediaErrorCode = "SOURCE_RATE_LIMITED"
	MediaCodeMediaProtected        MediaErrorCode = "MEDIA_PROTECTED"
	MediaCodeMediaNotFound         MediaErrorCode = "MEDIA_NOT_FOUND"
	MediaCodeMediaInvalid          MediaErrorCode = "MEDIA_INVALID"
	MediaCodeMediaUnsupported      MediaErrorCode = "MEDIA_UNSUPPORTED"
	MediaCodeMediaTooLarge         MediaErrorCode = "MEDIA_TOO_LARGE"
	MediaCodeDownloadFailed        MediaErrorCode = "DOWNLOAD_FAILED"
	MediaCodeDownloadIncomplete    MediaErrorCode = "DOWNLOAD_INCOMPLETE"
	MediaCodeProcessFailed         MediaErrorCode = "PROCESS_FAILED"
	MediaCodeProcessTimeout        MediaErrorCode = "PROCESS_TIMEOUT"
	MediaCodeStorageAuthFailed     MediaErrorCode = "STORAGE_AUTH_FAILED"
	MediaCodeStorageUnavailable    MediaErrorCode = "STORAGE_UNAVAILABLE"
	MediaCodeStorageObjectMissing  MediaErrorCode = "STORAGE_OBJECT_MISSING"
	MediaCodeStorageObjectMismatch MediaErrorCode = "STORAGE_OBJECT_MISMATCH"
	MediaCodeCanceled              MediaErrorCode = "CANCELED"
)

// MediaError is a classified pipeline failure. Err carries the underlying
// cause for logs; it is never returned to an API caller verbatim.
type MediaError struct {
	Stage     MediaStage
	Code      MediaErrorCode
	Retryable bool
	Err       error
}

func (e *MediaError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("%s at %s", e.Code, e.Stage)
	}
	return fmt.Sprintf("%s at %s: %s", e.Code, e.Stage, e.Err)
}

func (e *MediaError) Unwrap() error { return e.Err }

// NewMediaError builds a classified error.
func NewMediaError(stage MediaStage, code MediaErrorCode, retryable bool, err error) *MediaError {
	return &MediaError{Stage: stage, Code: code, Retryable: retryable, Err: err}
}

// MediaErrorCodeOf extracts the stable code from err, if any.
func MediaErrorCodeOf(err error) (MediaErrorCode, bool) {
	var me *MediaError
	if errors.As(err, &me) {
		return me.Code, true
	}
	return "", false
}

// Retryable reports whether err is a classified retryable failure.
func Retryable(err error) bool {
	var me *MediaError
	if errors.As(err, &me) {
		return me.Retryable
	}
	return false
}

// ResolveRequest asks a media resolver for the playable input of one real
// external episode.
type ResolveRequest struct {
	SourceCode        string
	ExternalEpisodeID string
	Quality           string
}

// MediaSource describes a temporary, provider-issued media input. URL is
// short lived and must never be persisted as a permanent asset address.
type MediaSource struct {
	Provider    string
	URL         string
	BackupURL   string
	ContentType string
	ExpiresAt   time.Time
}

// Resolver turns a verified external episode identity into a media input.
type Resolver interface {
	Resolve(ctx context.Context, req ResolveRequest) (*MediaSource, error)
}

// MediaInfo is the probed technical shape of a media file.
type MediaInfo struct {
	Container    string
	DurationMS   int64
	VideoCodec   string
	AudioCodec   string
	Width        int
	Height       int
	SizeBytes    int64
	VideoBitrate int64
}

// Playable reports whether the file has a decodable video track.
func (i *MediaInfo) Playable() bool {
	return i != nil && i.VideoCodec != "" && i.Width > 0 && i.Height > 0 && i.DurationMS > 0
}

// ProcessRequest is one processing attempt for a trusted local input file.
type ProcessRequest struct {
	InputPath  string
	OutputDir  string
	ProfileKey string
}

// ProcessedMedia is the immutable output of processing. Path lives under the
// job's own temporary directory and is removed by the pipeline.
type ProcessedMedia struct {
	Path   string
	SHA256 string
	// MD5Base64 is the RFC 4648 base64 of the raw MD5 digest. It is sent as
	// Content-MD5 so the transport verifies the uploaded bytes; it is not a
	// content identity.
	MD5Base64  string
	SizeBytes  int64
	Info       *MediaInfo
	Remuxed    bool
	ProfileKey string
}

// MediaProcessor probes and normalizes media into the delivery format.
type MediaProcessor interface {
	Probe(ctx context.Context, path string) (*MediaInfo, error)
	Prepare(ctx context.Context, req ProcessRequest) (*ProcessedMedia, error)
}

// ObjectRef is the stable identity of one stored object. VersionID is empty
// when bucket versioning is disabled; the key itself is immutable because it
// embeds the content hash.
type ObjectRef struct {
	Provider  string
	Bucket    string
	Region    string
	Key       string
	VersionID string
}

// PutObjectRequest uploads one immutable local file under Key.
type PutObjectRequest struct {
	Key         string
	Path        string
	ContentType string
	SHA256      string
	// MD5Base64 enables the storage-side Content-MD5 payload check.
	MD5Base64 string
	SizeBytes int64
}

// StoredObject is the confirmed remote object identity.
type StoredObject struct {
	ObjectRef
	SizeBytes int64
	// MetadataSHA256 is the x-cos-meta-sha256 value written by the uploader.
	// It is a custom-metadata claim, not proof that the stored bytes match:
	// only a download-and-hash check proves transfer integrity.
	MetadataSHA256 string
	ContentType    string
	ETag           string
}

// SignedAccess is a short-lived read credential for exactly one object
// version. Object carries the identity that was signed.
type SignedAccess struct {
	URL       string
	ExpiresAt time.Time
	Object    ObjectRef
}

// PresignPutRequest asks for a short-lived write credential for exactly one
// staging object. The credential may only create or replace that key.
type PresignPutRequest struct {
	Object    ObjectRef
	SizeBytes int64
	SHA256    string
	TTL       time.Duration
	SessionID int64
}

// PresignedUpload is the temporary write authorization of one object.
type PresignedUpload struct {
	URL       string
	Method    string
	Headers   map[string]string
	ExpiresAt time.Time
}

// ObjectStorage is the object-storage boundary (COS is the first adapter).
type ObjectStorage interface {
	PutFile(ctx context.Context, req PutObjectRequest) (*StoredObject, error)
	Head(ctx context.Context, ref ObjectRef) (*StoredObject, error)
	PresignGet(ctx context.Context, ref ObjectRef, ttl time.Duration) (*SignedAccess, error)
	// PresignPut issues a short-lived write credential limited to one staging
	// object. It never returns a permanent secret.
	PresignPut(ctx context.Context, req PresignPutRequest) (*PresignedUpload, error)
	// GetToFile copies the stored object identified by ref - including its
	// VersionID, when the bucket keeps versions - into dir so the server can
	// verify its bytes. It stops before writing more than maxBytes and fails
	// with MediaCodeMediaTooLarge instead of reading the whole object first.
	// The caller removes the file.
	GetToFile(ctx context.Context, ref ObjectRef, dir string, maxBytes int64) (*DownloadedFile, error)
	// PreviewURL issues a short-lived read credential for one private object.
	// Reviewers preview material through it; it never makes the bucket public.
	PreviewURL(ctx context.Context, ref ObjectRef, ttl time.Duration) (*PresignedDownload, error)
	// PlaybackURL issues a public, short-lived read credential for one frozen
	// episode. It needs no back-office session: the token carries the scope.
	PlaybackURL(ctx context.Context, ref ObjectRef, ttl time.Duration) (*PresignedDownload, error)
}

// PresignedDownload is a short-lived read credential for one object.
type PresignedDownload struct {
	URL       string
	ExpiresAt time.Time
}

// DownloadedFile is a bounded local copy of a resolved media input.
type DownloadedFile struct {
	Path        string
	SizeBytes   int64
	SHA256      string
	ContentType string
}

// Downloader fetches one resolved media input into the job directory.
type Downloader interface {
	Download(ctx context.Context, src *MediaSource, dstDir string) (*DownloadedFile, error)
}

// MediaAssetState separates object confirmation from business readiness.
// Without a database revision link the pipeline stops at verified; MEDIA-002
// promotes the asset to ready inside the commit transaction.
type MediaAssetState string

// Asset states produced by this slice.
const (
	MediaAssetVerified MediaAssetState = "verified"
)

// MediaAsset is the verified output object of one episode.
type MediaAsset struct {
	Object         ObjectRef
	State          MediaAssetState
	SizeBytes      int64
	SHA256         string
	ContentType    string
	Info           *MediaInfo
	ProfileKey     string
	SourceProvider string
	ExternalID     string
	DownloadedSize int64
	DownloadedHash string
	Remuxed        bool
}

// StageRecord is one completed stage with bounded diagnostic detail.
type StageRecord struct {
	Stage      MediaStage
	StartedAt  time.Time
	FinishedAt time.Time
	Detail     string
}

// RunInput selects exactly one input path: a real external episode resolved
// through the SDK boundary, or an already-authorized local file.
type RunInput struct {
	SourceCode        string
	ExternalEpisodeID string
	LocalInputPath    string
	Quality           string
	// ProcessOnly stops after normalize and skips storage entirely.
	ProcessOnly      bool
	KeepProcessed    bool
	KeepProcessedDir string
}

// RunResult is the outcome of one pipeline run. Signed is only populated when
// the caller asks for a preview credential.
type RunResult struct {
	Asset         *MediaAsset
	Signed        *SignedAccess
	Stages        []StageRecord
	ProcessedPath string
}
