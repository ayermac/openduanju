package data_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

func newTestDownloader(t *testing.T, srv *httptest.Server, maxBytes int64) *data.HTTPDownloader {
	t.Helper()
	downloader, err := data.NewHTTPDownloader(data.HTTPDownloaderConfig{
		Timeout:   5 * time.Second,
		MaxBytes:  maxBytes,
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatalf("NewHTTPDownloader: %v", err)
	}
	return downloader
}

func TestDownloaderWritesBoundedFile(t *testing.T) {
	body := "media-payload"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	downloader := newTestDownloader(t, srv, 1024)

	file, err := downloader.Download(context.Background(), &biz.MediaSource{URL: srv.URL + "/ep.mp4"}, t.TempDir())
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if file.SizeBytes != int64(len(body)) {
		t.Fatalf("size = %d, want %d", file.SizeBytes, len(body))
	}
	sum := sha256.Sum256([]byte(body))
	if file.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %s", file.SHA256)
	}
	if file.ContentType != "video/mp4" {
		t.Fatalf("content type = %q", file.ContentType)
	}
	got, err := os.ReadFile(file.Path)
	if err != nil || string(got) != body {
		t.Fatalf("file content = %q err=%v", got, err)
	}
}

func TestDownloaderClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		status    int
		code      biz.MediaErrorCode
		retryable bool
	}{
		{http.StatusUnauthorized, biz.MediaCodeSourceRejected, true},
		{http.StatusForbidden, biz.MediaCodeSourceRejected, true},
		{http.StatusNotFound, biz.MediaCodeMediaNotFound, false},
		{http.StatusGone, biz.MediaCodeMediaNotFound, false},
		{http.StatusTooManyRequests, biz.MediaCodeSourceRateLimited, true},
		{http.StatusInternalServerError, biz.MediaCodeSourceUnavailable, true},
		{http.StatusBadGateway, biz.MediaCodeSourceUnavailable, true},
		{http.StatusBadRequest, biz.MediaCodeDownloadFailed, false},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			downloader := newTestDownloader(t, srv, 1024)
			_, err := downloader.Download(context.Background(), &biz.MediaSource{URL: srv.URL + "/ep.mp4"}, t.TempDir())
			code, ok := biz.MediaErrorCodeOf(err)
			if !ok || code != tc.code {
				t.Fatalf("code = %s (%v), want %s", code, err, tc.code)
			}
			if biz.Retryable(err) != tc.retryable {
				t.Fatalf("retryable = %v, want %v", biz.Retryable(err), tc.retryable)
			}
		})
	}
}

func TestDownloaderRejectsOversizeBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()
	downloader := newTestDownloader(t, srv, 4)
	dir := t.TempDir()

	_, err := downloader.Download(context.Background(), &biz.MediaSource{URL: srv.URL + "/ep.mp4"}, dir)
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaTooLarge {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeMediaTooLarge)
	}
	assertDirEmpty(t, dir)
}

func TestDownloaderDetectsTruncatedBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	downloader := newTestDownloader(t, srv, 1024)
	dir := t.TempDir()

	_, err := downloader.Download(context.Background(), &biz.MediaSource{URL: srv.URL + "/ep.mp4"}, dir)
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeDownloadIncomplete {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeDownloadIncomplete)
	}
	assertDirEmpty(t, dir)
}

func TestDownloaderRejectsExpiredSourceWithoutRequest(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	downloader := newTestDownloader(t, srv, 1024)

	_, err := downloader.Download(context.Background(), &biz.MediaSource{
		URL:       srv.URL + "/ep.mp4",
		ExpiresAt: time.Now().Add(-time.Minute),
	}, t.TempDir())
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceRejected {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeSourceRejected)
	}
	if hits != 0 {
		t.Fatalf("expired source caused %d upstream requests", hits)
	}
}

func TestDownloaderRejectsNonHTTPSURL(t *testing.T) {
	downloader, err := data.NewHTTPDownloader(data.HTTPDownloaderConfig{Timeout: time.Second, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	_, err = downloader.Download(context.Background(), &biz.MediaSource{URL: "http://example.com/ep.mp4"}, t.TempDir())
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceRejected {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeSourceRejected)
	}
}

func TestDownloaderRejectsNonHTTPSRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/elsewhere.mp4", http.StatusFound)
	}))
	defer srv.Close()
	downloader := newTestDownloader(t, srv, 1024)

	_, err := downloader.Download(context.Background(), &biz.MediaSource{URL: srv.URL + "/ep.mp4"}, t.TempDir())
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceRejected {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeSourceRejected)
	}
	if biz.Retryable(err) {
		t.Fatal("an unsafe redirect must not be marked retryable")
	}
}

func TestDownloaderRequiresPositiveLimit(t *testing.T) {
	if _, err := data.NewHTTPDownloader(data.HTTPDownloaderConfig{MaxBytes: 0}); err == nil {
		t.Fatal("a downloader without a byte limit must be rejected")
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Fatalf("failed download left %s behind", entry.Name())
	}
}
