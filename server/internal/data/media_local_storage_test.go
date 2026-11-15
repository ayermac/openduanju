package data_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

func digestOfBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func TestLocalObjectStorageStoresAndConfirmsAnObject(t *testing.T) {
	root := t.TempDir()
	storage, err := data.NewLocalObjectStorage(root)
	if err != nil {
		t.Fatalf("NewLocalObjectStorage: %v", err)
	}
	source := filepath.Join(t.TempDir(), "processed.mp4")
	payload := []byte("normalized-media-bytes")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stored, err := storage.PutFile(ctx, biz.PutObjectRequest{
		Key: "media/ab/abcdef.mp4", Path: source, ContentType: "video/mp4",
		SizeBytes: int64(len(payload)), SHA256: digestOfBytes(payload),
	})
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if stored.Provider != "local" {
		t.Fatalf("provider = %q, want local", stored.Provider)
	}
	if stored.SizeBytes != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", stored.SizeBytes, len(payload))
	}
	confirmed, err := storage.Head(ctx, stored.ObjectRef)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if confirmed.MetadataSHA256 != stored.MetadataSHA256 {
		t.Fatalf("sha256 = %q, want %q", confirmed.MetadataSHA256, stored.MetadataSHA256)
	}
	if confirmed.SizeBytes != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", confirmed.SizeBytes, len(payload))
	}
	object := filepath.Join(root, "objects", "media", "ab", "abcdef.mp4")
	content, err := os.ReadFile(object)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if string(content) != string(payload) {
		t.Fatal("the stored object does not match the uploaded bytes")
	}
}

func TestLocalObjectStorageRejectsAWrongDigest(t *testing.T) {
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStorage: %v", err)
	}
	source := filepath.Join(t.TempDir(), "processed.mp4")
	if err := os.WriteFile(source, []byte("normalized-media-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = storage.PutFile(context.Background(), biz.PutObjectRequest{
		Key: "media/one.mp4", Path: source, SHA256: digestOfBytes([]byte("another-object")),
	})
	if err == nil {
		t.Fatal("a digest that does not match the bytes must be rejected")
	}
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMismatch {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeStorageObjectMismatch)
	}
}

func TestLocalObjectStorageRejectsAKeyOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	storage, err := data.NewLocalObjectStorage(root)
	if err != nil {
		t.Fatalf("NewLocalObjectStorage: %v", err)
	}
	source := filepath.Join(t.TempDir(), "processed.mp4")
	if err := os.WriteFile(source, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = storage.PutFile(context.Background(), biz.PutObjectRequest{
		Key: "../escape.mp4", Path: source,
	})
	if err == nil {
		t.Fatal("a key that escapes the root must be rejected")
	}
	if _, err := storage.Head(context.Background(), biz.ObjectRef{Key: "../escape.mp4"}); err == nil {
		t.Fatal("a key that escapes the root must be rejected")
	}
}

func TestLocalObjectStorageIssuesNoSignedAccess(t *testing.T) {
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStorage: %v", err)
	}
	if _, err := storage.PresignGet(context.Background(), biz.ObjectRef{Key: "media/one.mp4"}, 60_000_000_000); err == nil {
		t.Fatal("the local backend must not pretend to sign an access")
	}
}

func TestLocalObjectStorageReportsAMissingObject(t *testing.T) {
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStorage: %v", err)
	}
	_, err = storage.Head(context.Background(), biz.ObjectRef{Key: "media/missing.mp4"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeStorageObjectMissing {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeStorageObjectMissing)
	}
}

func TestNewLocalObjectStorageRequiresARoot(t *testing.T) {
	if _, err := data.NewLocalObjectStorage("  "); err == nil {
		t.Fatal("an empty root must be rejected")
	}
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalObjectStorage: %v", err)
	}
	if storage == nil {
		t.Fatal("the storage must be built")
	}
}
