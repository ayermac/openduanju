package data

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openduanju-server/internal/biz"
)

// LocalObjectStorage keeps immutable media objects in a local directory tree.
//
// It is a controllable adapter for development and acceptance runs: it makes
// the persistent media pipeline verifiable end to end without COS credentials.
// It is not a production storage backend. It issues no signed access, so a
// deployment that serves playback must use the COS driver.
type LocalObjectStorage struct {
	root string
}

// localObjectMeta is the sidecar that keeps the facts a HEAD must answer
// without downloading the object again.
type localObjectMeta struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ETag        string `json:"etag"`
}

// NewLocalObjectStorage builds the local storage adapter.
func NewLocalObjectStorage(root string) (*LocalObjectStorage, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, errors.New("local storage root is required"))
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, fmt.Errorf("resolve local storage root: %w", err))
	}
	for _, dir := range []string{filepath.Join(abs, "objects"), filepath.Join(abs, "meta")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, fmt.Errorf("create local storage directory: %w", err))
		}
	}
	return &LocalObjectStorage{root: abs}, nil
}

// PutFile stores one immutable local file and records its digest sidecar.
func (s *LocalObjectStorage) PutFile(ctx context.Context, req biz.PutObjectRequest) (*biz.StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	objectPath, err := s.objectPath(req.Key)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, err)
	}
	source, err := os.Open(req.Path)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeMediaNotFound, false, fmt.Errorf("open media output: %w", err))
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeMediaNotFound, false, fmt.Errorf("stat media output: %w", err))
	}
	if req.SizeBytes > 0 && info.Size() != req.SizeBytes {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageObjectMismatch, false,
			fmt.Errorf("local file size %d does not match expected %d", info.Size(), req.SizeBytes))
	}
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o750); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("create object directory: %w", err))
	}
	temp, err := os.CreateTemp(filepath.Dir(objectPath), ".upload-*")
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("create temporary object: %w", err))
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	digest := sha256.New()
	md5Digest := md5.New()
	if _, err := io.Copy(io.MultiWriter(temp, digest, md5Digest), source); err != nil {
		_ = temp.Close()
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("write temporary object: %w", err))
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("sync temporary object: %w", err))
	}
	if err := temp.Close(); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("close temporary object: %w", err))
	}
	if err := os.Rename(tempPath, objectPath); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("publish object: %w", err))
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	if req.SHA256 != "" && req.SHA256 != sum {
		return nil, biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageObjectMismatch, false,
			errors.New("uploaded bytes do not match the declared sha256"))
	}
	// The content identity is also the version: a snapshot is kept under the
	// key, so a reader that confirmed version A still reads A after the same
	// key was written with B. The local driver is a development adapter, so
	// the extra copy is acceptable; the COS driver uses bucket versioning.
	if err := s.snapshotVersion(req.Key, sum, objectPath); err != nil {
		return nil, err
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	meta := localObjectMeta{
		SHA256:      sum,
		ContentType: contentType,
		SizeBytes:   info.Size(),
		ETag:        base64.StdEncoding.EncodeToString(md5Digest.Sum(nil)),
	}
	if err := s.writeMeta(req.Key, meta); err != nil {
		return nil, err
	}
	return &biz.StoredObject{
		ObjectRef: biz.ObjectRef{
			Provider:  "local",
			Bucket:    filepath.Base(s.root),
			Region:    "local",
			Key:       req.Key,
			VersionID: sum,
		},
		SizeBytes:      meta.SizeBytes,
		MetadataSHA256: meta.SHA256,
		ContentType:    meta.ContentType,
		ETag:           meta.ETag,
	}, nil
}

// snapshotVersion keeps an immutable copy of one content identity under a
// key. A hard link is used first (same file system, no extra bytes); a copy
// is the fallback.
func (s *LocalObjectStorage) snapshotVersion(key string, versionID string, objectPath string) error {
	path, err := s.versionPath(key, versionID)
	if err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("create version directory: %w", err))
	}
	if _, err := os.Stat(path); err == nil {
		// The same bytes under the same key are already snapshotted.
		return nil
	}
	if err := os.Link(objectPath, path); err == nil {
		return nil
	}
	if err := copyFile(objectPath, path); err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("snapshot object version: %w", err))
	}
	return nil
}

// versionPath is the snapshot of one content identity under a key.
func (s *LocalObjectStorage) versionPath(key string, versionID string) (string, error) {
	if strings.ContainsAny(versionID, "/\\") || versionID == "" {
		return "", errors.New("object version is not allowed")
	}
	objectPath, err := s.objectPath(key)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(filepath.Join(s.root, "objects"), objectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "versions", relative+"."+versionID), nil
}

// copyFile copies one local file.
func copyFile(sourcePath string, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}

// Head confirms the stored object identity without downloading it again.
// When the reference names a version, the answer describes that snapshot, so
// a caller that confirmed version A does not read the newer B by accident.
func (s *LocalObjectStorage) Head(ctx context.Context, ref biz.ObjectRef) (*biz.StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	objectPath, err := s.objectPath(ref.Key)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, err)
	}
	path := objectPath
	versionID := ref.VersionID
	if versionID != "" {
		path, err = s.versionPath(ref.Key, versionID)
		if err != nil {
			return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if versionID != "" {
				return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMissing, false,
					fmt.Errorf("object %s version %s is missing", ref.Key, versionID))
			}
			return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMissing, false,
				fmt.Errorf("object %s is missing", ref.Key))
		}
		return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("stat object: %w", err))
	}
	meta, err := s.readMeta(ref.Key)
	if err != nil {
		return nil, err
	}
	digest := meta.SHA256
	if versionID == "" {
		versionID = meta.SHA256
	} else {
		// The version is the content identity: the snapshot under this name
		// was written with exactly these bytes.
		digest = versionID
	}
	return &biz.StoredObject{
		ObjectRef: biz.ObjectRef{
			Provider:  "local",
			Bucket:    filepath.Base(s.root),
			Region:    "local",
			Key:       ref.Key,
			VersionID: versionID,
		},
		SizeBytes:      info.Size(),
		MetadataSHA256: digest,
		ContentType:    meta.ContentType,
		ETag:           meta.ETag,
	}, nil
}

// PresignGet is not supported: the local backend issues no signed access, so
// a deployment that serves playback must use the COS driver.
func (s *LocalObjectStorage) PresignGet(ctx context.Context, ref biz.ObjectRef, ttl time.Duration) (*biz.SignedAccess, error) {
	return nil, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false,
		errors.New("local storage issues no signed access"))
}

// objectPath maps an object key to a path inside the root. A key that tries
// to escape the root is rejected instead of being cleaned away.
func (s *LocalObjectStorage) objectPath(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("object key is required")
	}
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return "", fmt.Errorf("object key %q is not allowed", key)
	}
	return filepath.Join(s.root, "objects", filepath.FromSlash(key)), nil
}

func (s *LocalObjectStorage) metaPath(key string) (string, error) {
	objectPath, err := s.objectPath(key)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(filepath.Join(s.root, "objects"), objectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, "meta", filepath.FromSlash(relative)+".json"), nil
}

func (s *LocalObjectStorage) writeMeta(key string, meta localObjectMeta) error {
	path, err := s.metaPath(key)
	if err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeConfigInvalid, false, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("create metadata directory: %w", err))
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("encode object metadata: %w", err))
	}
	if err := os.WriteFile(path, body, 0o640); err != nil {
		return biz.NewMediaError(biz.MediaStageUpload, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("write object metadata: %w", err))
	}
	return nil
}

func (s *LocalObjectStorage) readMeta(key string) (localObjectMeta, error) {
	path, err := s.metaPath(key)
	if err != nil {
		return localObjectMeta{}, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeConfigInvalid, false, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return localObjectMeta{}, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMismatch, false,
				fmt.Errorf("object %s has no metadata sidecar", key))
		}
		return localObjectMeta{}, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageUnavailable, true, fmt.Errorf("read object metadata: %w", err))
	}
	var meta localObjectMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return localObjectMeta{}, biz.NewMediaError(biz.MediaStageVerify, biz.MediaCodeStorageObjectMismatch, false,
			fmt.Errorf("decode object metadata: %w", err))
	}
	return meta, nil
}
