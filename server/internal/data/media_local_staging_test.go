package data_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

// stagingFixture builds a local storage with one signed staging object.
func stagingFixture(t *testing.T, body []byte, ttl time.Duration) (*data.LocalObjectStorage, *biz.PresignedUpload, string) {
	t.Helper()
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	key := "openduanju-media/uploads/1/episodes/2/staged"
	presigned, err := storage.PresignPut(context.Background(), biz.PresignPutRequest{
		Object:    biz.ObjectRef{Provider: "local", Key: key},
		SizeBytes: int64(len(body)),
		TTL:       ttl,
		SessionID: 11,
	})
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	return storage, presigned, key
}

func TestLocalStagingAcceptsOneSignedObject(t *testing.T) {
	body := []byte("staged episode bytes")
	storage, presigned, key := stagingFixture(t, body, time.Minute)
	request := httptest.NewRequest(presigned.Method, presigned.URL, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	storage.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("staging upload status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err := storage.Head(context.Background(), biz.ObjectRef{Key: key})
	if err != nil {
		t.Fatalf("head staged object: %v", err)
	}
	if stored.SizeBytes != int64(len(body)) {
		t.Fatalf("stored size: %d", stored.SizeBytes)
	}
	if !strings.EqualFold(stored.MetadataSHA256, digestOfStaging(string(body))) {
		t.Fatalf("stored digest: %s", stored.MetadataSHA256)
	}
}

func TestLocalStagingRejectsATamperedToken(t *testing.T) {
	body := []byte("staged episode bytes")
	storage, presigned, _ := stagingFixture(t, body, time.Minute)
	// Flip a character in the middle of the token; changing only the last one
	// is flaky because it can already be the replacement.
	index := len(presigned.URL) / 2
	replacement := "0"
	if presigned.URL[index:index+1] == "0" {
		replacement = "1"
	}
	tampered := presigned.URL[:index] + replacement + presigned.URL[index+1:]
	request := httptest.NewRequest(http.MethodPut, tampered, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	storage.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("tampered token status: %d", recorder.Code)
	}
}

func TestLocalStagingRejectsOversizedBody(t *testing.T) {
	body := []byte("0123456789")
	storage, presigned, _ := stagingFixture(t, body[:4], time.Minute)
	request := httptest.NewRequest(http.MethodPut, presigned.URL, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	storage.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge && recorder.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status: %d", recorder.Code)
	}
}

func TestLocalStagingRejectsAWrongDigest(t *testing.T) {
	body := []byte("staged episode bytes")
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	presigned, err := storage.PresignPut(context.Background(), biz.PresignPutRequest{
		Object:    biz.ObjectRef{Key: "uploads/1/episodes/2/staged"},
		SizeBytes: int64(len(body)),
		SHA256:    digestOfStaging("something else"),
		TTL:       time.Minute,
	})
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, presigned.URL, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	storage.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong digest status: %d", recorder.Code)
	}
}

func TestLocalStagingRejectsAnExpiredAuthorization(t *testing.T) {
	body := []byte("staged episode bytes")
	storage, presigned, _ := stagingFixture(t, body, time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	request := httptest.NewRequest(http.MethodPut, presigned.URL, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	storage.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expired authorization status: %d", recorder.Code)
	}
}

// digestOfStaging is a local copy of the test digest helper.
func digestOfStaging(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestLocalStorageReadsTheConfirmedVersion(t *testing.T) {
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	ctx := context.Background()
	key := "uploads/1/episodes/2/object"
	first := []byte("version-a-bytes")
	second := []byte("version-b-bytes")

	write := func(body []byte) *biz.StoredObject {
		t.Helper()
		path := filepath.Join(t.TempDir(), "object")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		stored, err := storage.PutFile(ctx, biz.PutObjectRequest{
			Key: key, Path: path, SizeBytes: int64(len(body)), SHA256: digestOfStaging(string(body)),
		})
		if err != nil {
			t.Fatalf("put object: %v", err)
		}
		return stored
	}
	storedA := write(first)
	if storedA.VersionID == "" {
		t.Fatal("the stored object has no version")
	}
	// The same key is overwritten with different bytes.
	write(second)

	confirmed, err := storage.GetToFile(ctx, storedA.ObjectRef, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("read confirmed version: %v", err)
	}
	body, err := os.ReadFile(confirmed.Path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != string(first) {
		t.Fatalf("read %q instead of the confirmed version", string(body))
	}
	head, err := storage.Head(ctx, storedA.ObjectRef)
	if err != nil {
		t.Fatalf("head confirmed version: %v", err)
	}
	if head.SizeBytes != int64(len(first)) || head.VersionID != storedA.VersionID {
		t.Fatalf("head of the confirmed version: size=%d version=%s", head.SizeBytes, head.VersionID)
	}
	// A read without a version still sees the newest bytes.
	latest, err := storage.GetToFile(ctx, biz.ObjectRef{Key: key}, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("read latest version: %v", err)
	}
	latestBody, _ := os.ReadFile(latest.Path)
	if string(latestBody) != string(second) {
		t.Fatalf("latest read returned %q", string(latestBody))
	}
}

func TestLocalStorageStopsAnOversizedRead(t *testing.T) {
	storage, err := data.NewLocalObjectStorage(t.TempDir())
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	ctx := context.Background()
	body := bytes.Repeat([]byte("x"), 1024)
	path := filepath.Join(t.TempDir(), "object")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	stored, err := storage.PutFile(ctx, biz.PutObjectRequest{Key: "uploads/oversized", Path: path, SizeBytes: int64(len(body))})
	if err != nil {
		t.Fatalf("put object: %v", err)
	}
	dir := t.TempDir()
	_, err = storage.GetToFile(ctx, stored.ObjectRef, dir, int64(len(body))-1)
	if !isMediaCode(err, biz.MediaCodeMediaTooLarge) {
		t.Fatalf("oversized read: %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("list download dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("an oversized read left %d files behind", len(entries))
	}
}

// isMediaCode reports whether err carries the given media error code.
func isMediaCode(err error, code biz.MediaErrorCode) bool {
	var mediaErr *biz.MediaError
	if errors.As(err, &mediaErr) {
		return mediaErr.Code == code
	}
	return false
}
