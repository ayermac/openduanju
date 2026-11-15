package data_test

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"hash/crc64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

func newTestCOSStorage(t *testing.T) *data.COSStorage {
	t.Helper()
	storage, err := data.NewCOSStorage(data.COSStorageConfig{
		Bucket:    "openduanju-test-1250000000",
		Region:    "ap-guangzhou",
		SecretID:  "AKIDEXAMPLE",
		SecretKey: "secret-example",
	})
	if err != nil {
		t.Fatalf("NewCOSStorage: %v", err)
	}
	return storage
}

func TestCOSStorageRejectsInvalidConfig(t *testing.T) {
	cases := map[string]data.COSStorageConfig{
		"empty bucket":  {Region: "ap-guangzhou", SecretID: "a", SecretKey: "b"},
		"bad bucket":    {Bucket: "Bad/Bucket", Region: "ap-guangzhou", SecretID: "a", SecretKey: "b"},
		"empty region":  {Bucket: "openduanju-1250000000", SecretID: "a", SecretKey: "b"},
		"bad region":    {Bucket: "openduanju-1250000000", Region: "gz/../x", SecretID: "a", SecretKey: "b"},
		"no credential": {Bucket: "openduanju-1250000000", Region: "ap-guangzhou"},
		"http endpoint": {Bucket: "openduanju-1250000000", Region: "ap-guangzhou", SecretID: "a", SecretKey: "b", BucketURL: "http://127.0.0.1:9000"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := data.NewCOSStorage(cfg)
			if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeConfigInvalid {
				t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeConfigInvalid)
			}
		})
	}
}

func TestCOSStoragePresignsExactObjectVersion(t *testing.T) {
	storage := newTestCOSStorage(t)
	ref := biz.ObjectRef{Key: "media-dev/ab/abcdef.mp4", VersionID: "v123"}
	signed, err := storage.PresignGet(context.Background(), ref, 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatalf("signed URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "openduanju-test-1250000000.cos.ap-guangzhou.myqcloud.com" {
		t.Fatalf("signed URL host = %s://%s", parsed.Scheme, parsed.Host)
	}
	if !strings.Contains(parsed.Path, ref.Key) {
		t.Fatalf("signed path %q does not contain the object key", parsed.Path)
	}
	query := parsed.Query()
	if query.Get("q-sign-algorithm") == "" || query.Get("q-signature") == "" {
		t.Fatalf("signed URL is missing the COS signature: %v", query)
	}
	if query.Get("versionId") != "v123" {
		t.Fatalf("versionId = %q, want v123; a signature must bind the object version", query.Get("versionId"))
	}
	if signed.Object != ref {
		t.Fatalf("signed identity = %+v, want %+v", signed.Object, ref)
	}
	if signed.ExpiresAt.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatalf("expiry %s is not the requested ttl", signed.ExpiresAt)
	}
}

func TestCOSStoragePresignWithoutVersionKeepsKeyOnly(t *testing.T) {
	storage := newTestCOSStorage(t)
	signed, err := storage.PresignGet(context.Background(), biz.ObjectRef{Key: "media-dev/ab/abcdef.mp4"}, time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	parsed, _ := url.Parse(signed.URL)
	if parsed.Query().Has("versionId") {
		t.Fatalf("unversioned object must not carry a versionId: %s", signed.URL)
	}
}

func TestCOSStoragePresignRequiresKeyAndTTL(t *testing.T) {
	storage := newTestCOSStorage(t)
	if _, err := storage.PresignGet(context.Background(), biz.ObjectRef{}, time.Minute); err == nil {
		t.Fatal("empty key must fail")
	}
	if _, err := storage.PresignGet(context.Background(), biz.ObjectRef{Key: "a/b.mp4"}, 0); err == nil {
		t.Fatal("zero ttl must fail")
	}
}

func TestCOSStoragePutRequiresExistingFile(t *testing.T) {
	storage := newTestCOSStorage(t)
	_, err := storage.PutFile(context.Background(), biz.PutObjectRequest{
		Key:       "media-dev/ab/missing.mp4",
		Path:      t.TempDir() + "/missing.mp4",
		SizeBytes: 10,
	})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaNotFound {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeMediaNotFound)
	}
}

func TestCOSStorageHeadRequiresKey(t *testing.T) {
	storage := newTestCOSStorage(t)
	if _, err := storage.Head(context.Background(), biz.ObjectRef{}); err == nil {
		t.Fatal("empty key must fail")
	}
}

type cosCapture struct {
	mu          sync.Mutex
	putHeader   http.Header
	putKey      string
	headVersion string
	headKey     string
}

func (c *cosCapture) snapshot() (http.Header, string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.putHeader.Clone(), c.putKey, c.headVersion, c.headKey
}

func TestCOSStorageSendsIntegrityHeadersAndReadsIdentity(t *testing.T) {
	capture := &cosCapture{}
	body := "object-bytes"
	sha := "8a1e2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8"
	rawMD5 := md5.Sum([]byte(body))
	md5Base64 := base64.StdEncoding.EncodeToString(rawMD5[:])

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			capture.putHeader = r.Header.Clone()
			capture.putKey = r.URL.Path
			payload, _ := io.ReadAll(r.Body)
			checksum := crc64.Checksum(payload, crc64.MakeTable(crc64.ECMA))
			w.Header().Set("X-Cos-Version-Id", "v1")
			w.Header().Set("X-Cos-Hash-Crc64ecma", strconv.FormatUint(checksum, 10))
			w.WriteHeader(http.StatusOK)
		case http.MethodHead:
			capture.headKey = r.URL.Path
			capture.headVersion = r.URL.Query().Get("versionId")
			w.Header().Set("Content-Length", "12")
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("X-Cos-Version-Id", capture.headVersion)
			w.Header().Set("X-Cos-Meta-Sha256", sha)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	storage, err := data.NewCOSStorage(data.COSStorageConfig{
		Bucket:    "openduanju-test-1250000000",
		Region:    "ap-guangzhou",
		SecretID:  "AKIDEXAMPLE",
		SecretKey: "secret-example",
		BucketURL: srv.URL,
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatalf("NewCOSStorage: %v", err)
	}
	file := filepath.Join(t.TempDir(), "media.mp4")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	put, err := storage.PutFile(context.Background(), biz.PutObjectRequest{
		Key:         "media-dev/ab/object.mp4",
		Path:        file,
		ContentType: "video/mp4",
		SHA256:      sha,
		MD5Base64:   md5Base64,
		SizeBytes:   int64(len(body)),
	})
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if put.VersionID != "v1" {
		t.Fatalf("upload version = %q, want v1", put.VersionID)
	}
	if put.MetadataSHA256 != sha {
		t.Fatalf("metadata sha = %q", put.MetadataSHA256)
	}
	header, putKey, _, _ := capture.snapshot()
	if header.Get("Content-MD5") != md5Base64 {
		t.Fatalf("Content-MD5 = %q, want %q", header.Get("Content-MD5"), md5Base64)
	}
	if header.Get("X-Cos-Meta-Sha256") != sha {
		t.Fatalf("X-Cos-Meta-Sha256 = %q", header.Get("X-Cos-Meta-Sha256"))
	}
	if putKey != "/media-dev/ab/object.mp4" {
		t.Fatalf("put path = %q", putKey)
	}

	head, err := storage.Head(context.Background(), biz.ObjectRef{Key: "media-dev/ab/object.mp4", VersionID: "v1"})
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.VersionID != "v1" || head.MetadataSHA256 != sha || head.SizeBytes != int64(len(body)) {
		t.Fatalf("head identity = %+v", head)
	}
	_, _, headVersion, headKey := capture.snapshot()
	if headVersion != "v1" {
		t.Fatalf("head version query = %q, a versioned identity must be requested explicitly", headVersion)
	}
	if headKey != "/media-dev/ab/object.mp4" {
		t.Fatalf("head path = %q", headKey)
	}
}

func TestCOSPreviewURLBindsTheVersion(t *testing.T) {
	storage, err := data.NewCOSStorage(data.COSStorageConfig{
		Bucket:    "examplebucket-1250000000",
		Region:    "ap-guangzhou",
		SecretID:  "AKIDEXAMPLE",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatalf("cos storage: %v", err)
	}
	signed, err := storage.PreviewURL(context.Background(), biz.ObjectRef{
		Key: "openduanju-media/uploads/1/episodes/1/object.mp4", VersionID: "version-a",
	}, 5*time.Minute)
	if err != nil {
		t.Fatalf("preview url: %v", err)
	}
	if !strings.Contains(signed.URL, "versionId=version-a") {
		t.Fatalf("the preview URL does not pin the frozen version: %s", signed.URL)
	}
	if signed.ExpiresAt.Before(time.Now()) {
		t.Fatal("the preview URL is already expired")
	}
}
