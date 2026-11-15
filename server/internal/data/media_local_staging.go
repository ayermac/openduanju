package data

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"openduanju-server/internal/biz"
)

// stagingPathPrefix is where the local driver serves browser uploads. The
// generated admin API cannot carry raw bytes, so the local driver exposes one
// narrowly signed PUT endpoint instead. The COS driver returns a real
// presigned URL and never uses this path.
const stagingPathPrefix = biz.StagingPathPrefix

// previewPathPrefix is where the local driver serves private preview reads.
const previewPathPrefix = biz.PreviewPathPrefix

// playbackPathPrefix is the public counterpart: a guest presents a signed
// token, not a staff session.
const playbackPathPrefix = biz.PlaybackPathPrefix

// stagingToken is the signed payload of one local upload authorization. It
// names exactly one object key, one size and one expiry; the signature keeps
// a caller from writing any other key.
type stagingToken struct {
	Key       string `json:"k"`
	SizeBytes int64  `json:"s"`
	SHA256    string `json:"h,omitempty"`
	SessionID int64  `json:"id"`
	ExpiresAt int64  `json:"e"`
	// Action is "put" for an upload and "get" for a private preview. An empty
	// value is an old upload token.
	Action string `json:"a,omitempty"`
	// VersionID pins a preview to the version the review froze.
	VersionID string `json:"v,omitempty"`
}

// PreviewURL issues a signed read URL for exactly one stored object.
func (s *LocalObjectStorage) PreviewURL(ctx context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.PresignedDownload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Key == "" {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("preview key is required"))
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	token, err := s.signStaging(stagingToken{
		Key:       ref.Key,
		VersionID: ref.VersionID,
		Action:    "get",
		ExpiresAt: time.Now().Add(ttl).UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	return &biz.PresignedDownload{URL: previewPathPrefix + token, ExpiresAt: time.Now().Add(ttl)}, nil
}

// PlaybackURL issues a public signed read URL for one frozen episode.
func (s *LocalObjectStorage) PlaybackURL(ctx context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.PresignedDownload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Key == "" {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, errors.New("playback key is required"))
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	token, err := s.signStaging(stagingToken{
		Key:       ref.Key,
		VersionID: ref.VersionID,
		Action:    "play",
		ExpiresAt: time.Now().Add(ttl).UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	return &biz.PresignedDownload{URL: playbackPathPrefix + token, ExpiresAt: time.Now().Add(ttl)}, nil
}

// servePreview streams one signed preview object with range support, so a
// reviewer can scrub a video without downloading it again.
func (s *LocalObjectStorage) servePreview(ctx context.Context, w http.ResponseWriter, r *http.Request, raw string) {
	token, err := s.verifyStaging(raw)
	if err != nil || token.Action != "get" || token.Key == "" {
		writeStagingError(w, http.StatusForbidden, "PREVIEW_AUTH_INVALID", "预览授权无效或已过期")
		return
	}
	objectPath, err := s.objectPath(token.Key)
	if err != nil {
		writeStagingError(w, http.StatusBadRequest, "PREVIEW_KEY_INVALID", "预览对象无效")
		return
	}
	path := objectPath
	if token.VersionID != "" {
		path, err = s.versionPath(token.Key, token.VersionID)
		if err != nil {
			writeStagingError(w, http.StatusBadRequest, "PREVIEW_KEY_INVALID", "预览对象无效")
			return
		}
	}
	file, err := os.Open(path)
	if err != nil {
		writeStagingError(w, http.StatusNotFound, "OBJECT_NOT_FOUND", "预览对象不存在")
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		writeStagingError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "预览读取失败")
		return
	}
	meta, _ := s.readMeta(token.Key)
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	// Private material: never let a shared cache keep it.
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, token.Key, info.ModTime(), file)
}

// uploadSecret derives the signing key of the local driver from its root. The
// driver is a development and acceptance adapter: a party that can read the
// root can already rewrite the objects, so no extra secret file is required.
func (s *LocalObjectStorage) uploadSecret() []byte {
	sum := sha256.Sum256([]byte("openduanju-local-upload:" + s.root))
	return sum[:]
}

// PresignPut issues a relative PUT URL for exactly one staging object. A
// relative URL keeps the admin UI on its own origin, so the dev proxy and the
// deployment proxy route it without a CORS exception.
func (s *LocalObjectStorage) PresignPut(ctx context.Context, req biz.PresignPutRequest) (*biz.PresignedUpload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Object.Key == "" {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("staging key is required"))
	}
	if req.TTL <= 0 {
		req.TTL = 15 * time.Minute
	}
	token, err := s.signStaging(stagingToken{
		Key:       req.Object.Key,
		SizeBytes: req.SizeBytes,
		SHA256:    req.SHA256,
		SessionID: req.SessionID,
		ExpiresAt: time.Now().Add(req.TTL).UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	return &biz.PresignedUpload{
		URL:       stagingPathPrefix + token,
		Method:    http.MethodPut,
		Headers:   map[string]string{"Content-Type": "application/octet-stream"},
		ExpiresAt: time.Now().Add(req.TTL),
	}, nil
}

// signStaging serializes and signs one staging token.
func (s *LocalObjectStorage) signStaging(token stagingToken) (string, error) {
	body, err := json.Marshal(token)
	if err != nil {
		return "", biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, fmt.Errorf("encode staging token: %w", err))
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.uploadSecret())
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

// verifyStaging checks the signature and the expiry of a staging token.
func (s *LocalObjectStorage) verifyStaging(raw string) (stagingToken, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return stagingToken{}, errors.New("malformed staging token")
	}
	mac := hmac.New(sha256.New, s.uploadSecret())
	mac.Write([]byte(parts[0]))
	signature, err := hex.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return stagingToken{}, errors.New("staging token signature is invalid")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return stagingToken{}, errors.New("staging token payload is invalid")
	}
	var token stagingToken
	if err := json.Unmarshal(body, &token); err != nil {
		return stagingToken{}, errors.New("staging token payload is invalid")
	}
	if token.ExpiresAt > 0 && time.Now().UnixMilli() > token.ExpiresAt {
		return stagingToken{}, errors.New("staging authorization expired")
	}
	return token, nil
}

// ServeHTTP accepts the bytes of one signed staging object. It is mounted at
// stagingPathPrefix; the token is the only credential, so it carries its own
// expiry and object scope and never grants access to another key.
func (s *LocalObjectStorage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, playbackPathPrefix) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeStagingError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "只支持GET播放")
			return
		}
		raw := strings.TrimPrefix(r.URL.Path, playbackPathPrefix)
		if raw == "" || strings.Contains(raw, "/") {
			writeStagingError(w, http.StatusNotFound, "NOT_FOUND", "播放地址无效")
			return
		}
		s.servePlayback(w, r, raw)
		return
	}
	if strings.HasPrefix(r.URL.Path, previewPathPrefix) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeStagingError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "只支持GET预览")
			return
		}
		raw := strings.TrimPrefix(r.URL.Path, previewPathPrefix)
		if raw == "" || strings.Contains(raw, "/") {
			writeStagingError(w, http.StatusNotFound, "NOT_FOUND", "预览地址无效")
			return
		}
		s.servePreview(r.Context(), w, r, raw)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeStagingError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "只支持PUT上传")
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, stagingPathPrefix)
	if raw == "" || strings.Contains(raw, "/") {
		writeStagingError(w, http.StatusNotFound, "NOT_FOUND", "上传地址无效")
		return
	}
	token, err := s.verifyStaging(raw)
	if err != nil {
		writeStagingError(w, http.StatusForbidden, "UPLOAD_AUTH_INVALID", "上传授权无效或已过期")
		return
	}
	if token.SizeBytes <= 0 {
		writeStagingError(w, http.StatusBadRequest, "UPLOAD_SIZE_INVALID", "上传大小无效")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, token.SizeBytes+1)
	temp, err := os.CreateTemp(s.root, ".staging-*")
	if err != nil {
		writeStagingError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "暂存写入失败")
		return
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), limited)
	if err != nil {
		_ = temp.Close()
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeStagingError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "上传超过声明大小")
			return
		}
		writeStagingError(w, http.StatusBadRequest, "UPLOAD_INCOMPLETE", "上传内容不完整")
		return
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		writeStagingError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "暂存写入失败")
		return
	}
	if err := temp.Close(); err != nil {
		writeStagingError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "暂存写入失败")
		return
	}
	if written != token.SizeBytes {
		writeStagingError(w, http.StatusBadRequest, "OBJECT_SIZE_MISMATCH", "上传字节数与声明不一致")
		return
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if token.SHA256 != "" && !strings.EqualFold(token.SHA256, digest) {
		writeStagingError(w, http.StatusBadRequest, "OBJECT_DIGEST_MISMATCH", "上传内容摘要与声明不一致")
		return
	}
	stored, err := s.PutFile(r.Context(), biz.PutObjectRequest{
		Key:         token.Key,
		Path:        tempPath,
		ContentType: r.Header.Get("Content-Type"),
		SHA256:      digest,
		SizeBytes:   written,
	})
	if err != nil {
		writeStagingError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "暂存对象写入失败")
		return
	}
	w.Header().Set("ETag", stored.ETag)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// writeStagingError answers a staging failure with a small JSON body. The
// response never carries a path or a storage detail.
func writeStagingError(w http.ResponseWriter, status int, reason string, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"code":%d,"reason":%q,"message":%q}`, status, reason, message)))
}

// GetToFile copies one stored object into dir for server-side verification.
// The reference is honored down to its version: a request for version A reads
// the snapshot of A, never the bytes that replaced them under the same key.
// The copy stops at maxBytes instead of reading an oversized object first.
func (s *LocalObjectStorage) GetToFile(ctx context.Context, ref biz.ObjectRef, dir string, maxBytes int64) (*biz.DownloadedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	objectPath, err := s.objectPath(ref.Key)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, err)
	}
	path := objectPath
	if ref.VersionID != "" {
		path, err = s.versionPath(ref.Key, ref.VersionID)
		if err != nil {
			return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, err)
		}
	}
	source, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMissing, false,
				fmt.Errorf("object %s is missing", ref.Key))
		}
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("open object: %w", err))
	}
	defer func() { _ = source.Close() }()
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
	body := io.Reader(source)
	if maxBytes > 0 {
		body = io.LimitReader(source, maxBytes+1)
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
	meta, _ := s.readMeta(ref.Key)
	return &biz.DownloadedFile{
		Path:        tempPath,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: meta.ContentType,
	}, nil
}

// Compile-time check that the local driver serves the staging route.
var _ http.Handler = (*LocalObjectStorage)(nil)

// servePlayback streams one signed episode. The token already fixes the object
// version; nothing here can redirect it to another key.
func (s *LocalObjectStorage) servePlayback(w http.ResponseWriter, r *http.Request, raw string) {
	token, err := s.verifyStaging(raw)
	if err != nil || token.Action != "play" || token.Key == "" {
		writeStagingError(w, http.StatusForbidden, "PLAYBACK_AUTH_INVALID", "播放授权无效或已过期")
		return
	}
	objectPath, err := s.objectPath(token.Key)
	if err != nil {
		writeStagingError(w, http.StatusBadRequest, "PLAYBACK_KEY_INVALID", "播放对象无效")
		return
	}
	path := objectPath
	if token.VersionID != "" {
		path, err = s.versionPath(token.Key, token.VersionID)
		if err != nil {
			writeStagingError(w, http.StatusBadRequest, "PLAYBACK_KEY_INVALID", "播放对象无效")
			return
		}
	}
	file, err := os.Open(path)
	if err != nil {
		writeStagingError(w, http.StatusNotFound, "OBJECT_NOT_FOUND", "播放对象不存在")
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		writeStagingError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "播放读取失败")
		return
	}
	meta, _ := s.readMeta(token.Key)
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, token.Key, info.ModTime(), file)
}
