package data

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"openduanju-server/internal/biz"

	"github.com/tencentyun/cos-go-sdk-v5"
)

var (
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$`)
	regionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}$`)
)

// COSStorageConfig identifies one isolated bucket and credential set. The
// credential should be scoped to the configured prefix by the operator.
type COSStorageConfig struct {
	Bucket       string
	Region       string
	SecretID     string
	SecretKey    string
	SessionToken string
	// BucketURL overrides the derived bucket endpoint. It exists for tests
	// against a local TLS server; production leaves it empty.
	BucketURL string
	// Transport overrides the HTTPS transport for tests only.
	Transport http.RoundTripper
}

// COSStorage stores immutable media objects in Tencent Cloud COS.
type COSStorage struct {
	client *cos.Client
	bucket string
	region string
}

// NewCOSStorage builds a COS client. It performs no network call, so a
// missing or invalid configuration fails before any upload starts.
func NewCOSStorage(cfg COSStorageConfig) (*COSStorage, error) {
	if !bucketPattern.MatchString(cfg.Bucket) {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, fmt.Errorf("cos bucket %q is invalid", cfg.Bucket))
	}
	if !regionPattern.MatchString(cfg.Region) {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, fmt.Errorf("cos region %q is invalid", cfg.Region))
	}
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("cos credentials are not configured"))
	}
	endpoint := cfg.BucketURL
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.cos.%s.myqcloud.com", cfg.Bucket, cfg.Region)
	}
	bucketURL, err := url.Parse(endpoint)
	if err != nil || bucketURL.Scheme != "https" || bucketURL.Host == "" {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("cos bucket URL must be an absolute HTTPS URL"))
	}
	client := cos.NewClient(
		&cos.BaseURL{BucketURL: bucketURL},
		&http.Client{
			Transport: &cos.AuthorizationTransport{
				SecretID:     cfg.SecretID,
				SecretKey:    cfg.SecretKey,
				SessionToken: cfg.SessionToken,
				Expire:       time.Hour,
				Transport:    cfg.Transport,
			},
			Timeout: 10 * time.Minute,
		},
	)
	return &COSStorage{client: client, bucket: cfg.Bucket, region: cfg.Region}, nil
}

// PutFile uploads one local file as an immutable object and records the
// pipeline's own SHA-256 as object metadata (COS ETag is not a whole-file
// SHA-256 for multipart uploads).
func (s *COSStorage) PutFile(ctx context.Context, req biz.PutObjectRequest) (*biz.StoredObject, error) {
	if strings.TrimSpace(req.Key) == "" || strings.TrimSpace(req.Path) == "" {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("object key and source path are required"))
	}
	info, err := os.Stat(req.Path)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeMediaNotFound, false, fmt.Errorf("open media output: %w", err))
	}
	if req.SizeBytes > 0 && info.Size() != req.SizeBytes {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageObjectMismatch, false, fmt.Errorf("local file size %d does not match expected %d", info.Size(), req.SizeBytes))
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	md5Base64 := req.MD5Base64
	if md5Base64 == "" {
		computed, md5Err := fileMD5Base64(req.Path)
		if md5Err != nil {
			return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeProcessFailed, true, fmt.Errorf("compute upload checksum: %w", md5Err))
		}
		md5Base64 = computed
	}
	header := http.Header{}
	if req.SHA256 != "" {
		// Custom metadata is a durable claim for later HEAD comparisons; it is
		// not an integrity check of the received bytes.
		header.Set("X-Cos-Meta-Sha256", req.SHA256)
	}
	resp, err := s.client.Object.PutFromFile(ctx, req.Key, req.Path, &cos.ObjectPutOptions{
		ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{
			ContentType:   contentType,
			ContentLength: info.Size(),
			ContentMD5:    md5Base64,
			XCosMetaXXX:   &header,
			// Media objects are immutable; a long cache only helps private
			// preview clients and never makes the object public.
			CacheControl: "private, max-age=31536000, immutable",
		},
	})
	if err != nil {
		return nil, cosError(biz.MediaStageUpload, err)
	}
	versionID := ""
	if resp != nil {
		versionID = resp.Header.Get("X-Cos-Version-Id")
	}
	return &biz.StoredObject{
		ObjectRef: biz.ObjectRef{
			Provider:  "cos",
			Bucket:    s.bucket,
			Region:    s.region,
			Key:       req.Key,
			VersionID: versionID,
		},
		SizeBytes:      info.Size(),
		MetadataSHA256: req.SHA256,
		ContentType:    contentType,
	}, nil
}

// Head confirms the remote object identity without downloading it again.
func (s *COSStorage) Head(ctx context.Context, ref biz.ObjectRef) (*biz.StoredObject, error) {
	if strings.TrimSpace(ref.Key) == "" {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("object key is required"))
	}
	var resp *cos.Response
	var err error
	if ref.VersionID != "" {
		resp, err = s.client.Object.Head(ctx, ref.Key, nil, ref.VersionID)
	} else {
		resp, err = s.client.Object.Head(ctx, ref.Key, nil)
	}
	if err != nil {
		if cos.IsNotFoundError(err) {
			return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMissing, false, fmt.Errorf("object %s is missing", ref.Key))
		}
		return nil, cosError(biz.MediaStageVerify, err)
	}
	size, parseErr := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if parseErr != nil || size < 0 {
		size = 0
	}
	versionID := resp.Header.Get("X-Cos-Version-Id")
	if ref.VersionID == "" {
		ref.VersionID = versionID
	}
	return &biz.StoredObject{
		ObjectRef: biz.ObjectRef{
			Provider:  "cos",
			Bucket:    s.bucket,
			Region:    s.region,
			Key:       ref.Key,
			VersionID: ref.VersionID,
		},
		SizeBytes:      size,
		MetadataSHA256: resp.Header.Get("X-Cos-Meta-Sha256"),
		ContentType:    resp.Header.Get("Content-Type"),
		ETag:           strings.Trim(resp.Header.Get("ETag"), `"`),
	}, nil
}

// fileMD5Base64 computes the RFC 4648 base64 Content-MD5 for a local file.
func fileMD5Base64(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := md5.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(digest.Sum(nil)), nil
}

// PresignGet issues a short-lived read credential for exactly one object.
func (s *COSStorage) PresignGet(ctx context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.SignedAccess, error) {
	if strings.TrimSpace(ref.Key) == "" {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("object key is required"))
	}
	if ttl <= 0 {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("signature ttl must be positive"))
	}
	// A typed nil pointer would panic inside the SDK's type assertion, so the
	// interface stays nil when no version is bound.
	var opt interface{}
	if ref.VersionID != "" {
		// The version id is part of the signed query, so a signature cannot be
		// reused for a newer object version written under the same key.
		query := url.Values{"versionId": []string{ref.VersionID}}
		opt = &cos.PresignedURLOptions{Query: &query}
	}
	signed, err := s.client.Object.GetPresignedURL3(ctx, http.MethodGet, ref.Key, ttl, opt)
	if err != nil {
		return nil, cosError(biz.MediaStageVerify, err)
	}
	return &biz.SignedAccess{URL: signed.String(), ExpiresAt: time.Now().Add(ttl), Object: ref}, nil
}

// PresignPut issues a short-lived write credential for exactly one staging
// object. The signature is bound to the key and the expiry; the caller never
// receives a permanent secret.
func (s *COSStorage) PresignPut(ctx context.Context, req biz.PresignPutRequest) (*biz.PresignedUpload, error) {
	if strings.TrimSpace(req.Object.Key) == "" {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("object key is required"))
	}
	if req.TTL <= 0 {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("signature ttl must be positive"))
	}
	signed, err := s.client.Object.GetPresignedURL3(ctx, http.MethodPut, req.Object.Key, req.TTL, nil)
	if err != nil {
		return nil, cosError(biz.MediaStageUpload, err)
	}
	return &biz.PresignedUpload{
		URL:       signed.String(),
		Method:    http.MethodPut,
		Headers:   map[string]string{"Content-Type": "application/octet-stream"},
		ExpiresAt: time.Now().Add(req.TTL),
	}, nil
}

// GetToFile downloads one stored object for server-side verification.
//
// The object identity is part of the request: when the reference carries a
// version, the read is pinned to that version, so an object overwritten under
// the same key cannot silently satisfy a task that froze the older bytes. The
// body is copied through a limiter, so an oversized object stops before it is
// written in full.
func (s *COSStorage) GetToFile(ctx context.Context, ref biz.ObjectRef, dir string, maxBytes int64) (*biz.DownloadedFile, error) {
	if strings.TrimSpace(ref.Key) == "" {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("object key is required"))
	}
	var resp *cos.Response
	var err error
	if ref.VersionID != "" {
		resp, err = s.client.Object.Get(ctx, ref.Key, nil, ref.VersionID)
	} else {
		resp, err = s.client.Object.Get(ctx, ref.Key, nil)
	}
	if err != nil {
		return nil, cosError(biz.MediaStageVerify, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("create download directory: %w", err))
	}
	temp, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("create download file: %w", err))
	}
	tempPath := temp.Name()
	body := io.Reader(resp.Body)
	if maxBytes > 0 {
		// One extra byte distinguishes "exactly at the limit" from "over it"
		// without trusting Content-Length.
		body = io.LimitReader(resp.Body, maxBytes+1)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), body)
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tempPath)
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageUnavailable, true, errors.New("copy stored object"))
	}
	if maxBytes > 0 && written > maxBytes {
		_ = os.Remove(tempPath)
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeMediaTooLarge, false,
			fmt.Errorf("stored object exceeds the configured limit of %d bytes", maxBytes))
	}
	return &biz.DownloadedFile{
		Path:        tempPath,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// cosError maps a COS failure to a stable media error without leaking the
// request URL or credentials. Only the service code and request id survive.
func cosError(stage biz.MediaStage, err error) error {
	if err == nil {
		return nil
	}
	if cos.IsNotFoundError(err) {
		return biz.NewMediaError(stage, biz.MediaCodeStorageObjectMissing, false, errors.New("cos object is missing"))
	}
	var er *cos.ErrorResponse
	if errors.As(err, &er) {
		detail := fmt.Sprintf("cos code=%s request_id=%s status=%d", er.Code, er.RequestID, statusOf(er))
		if er.Response != nil && (er.Response.StatusCode == http.StatusUnauthorized || er.Response.StatusCode == http.StatusForbidden) {
			return biz.NewMediaError(stage, biz.MediaCodeStorageAuthFailed, false, errors.New(detail))
		}
		return biz.NewMediaError(stage, biz.MediaCodeStorageUnavailable, true, errors.New(detail))
	}
	return biz.NewMediaError(stage, biz.MediaCodeStorageUnavailable, true, errors.New("cos request failed"))
}

func statusOf(er *cos.ErrorResponse) int {
	if er == nil || er.Response == nil {
		return 0
	}
	return er.Response.StatusCode
}

// cosPreviewOptions carries the query parameters of a presigned preview. The
// version id is signed together with the key, so a reader that froze version A
// keeps reading A even after the key is overwritten.
type cosPreviewOptions struct {
	VersionID string `url:"versionId,omitempty"`
}

// PlaybackURL is the public read URL of one frozen episode. It signs the same
// version query as a preview.
func (s *COSStorage) PlaybackURL(ctx context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.PresignedDownload, error) {
	return s.PreviewURL(ctx, ref, ttl)
}

// PreviewURL issues a short-lived presigned GET for one private object. The
// bucket stays private; only this URL is readable, and only for its window.
// COS signing does not bind a version, so the reviewer sees the current object
// of the key; the review still decides on the frozen bytes recorded by the
// batch.
func (s *COSStorage) PreviewURL(ctx context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.PresignedDownload, error) {
	if strings.TrimSpace(ref.Key) == "" {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("preview key is required"))
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	// The version is part of the signed query, so the URL cannot be pointed at
	// newer bytes under the same key.
	options := &cosPreviewOptions{VersionID: ref.VersionID}
	url, err := s.client.Object.GetPresignedURL2(ctx, http.MethodGet, ref.Key, ttl, options)
	if err != nil {
		return nil, cosError(biz.MediaStageVerify, err)
	}
	return &biz.PresignedDownload{URL: url.String(), ExpiresAt: time.Now().Add(ttl)}, nil
}
