package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openduanju-server/internal/biz"
)

// HTTPDownloaderConfig bounds one media download.
type HTTPDownloaderConfig struct {
	Timeout   time.Duration
	MaxBytes  int64
	UserAgent string
	// Transport is an optional seam for tests; production uses the default
	// HTTPS transport built below.
	Transport http.RoundTripper
}

var errUnsafeRedirect = errors.New("redirect to a non-HTTPS URL is not allowed")

// HTTPDownloader fetches a resolved media input over HTTPS. It never resumes
// or appends to an existing file: a fresh run starts from zero until the
// identity/Range checks required for resumption exist.
type HTTPDownloader struct {
	client    *http.Client
	maxBytes  int64
	timeout   time.Duration
	userAgent string
}

// NewHTTPDownloader builds a downloader with a private transport.
func NewHTTPDownloader(cfg HTTPDownloaderConfig) (*HTTPDownloader, error) {
	if cfg.MaxBytes <= 0 {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeConfigInvalid, false, errors.New("download max bytes must be positive"))
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "openduanju-media/0.1"
	}
	transport := cfg.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConns:          4,
			MaxIdleConnsPerHost:   2,
		}
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errUnsafeRedirect
			}
			if req.URL.User != nil {
				return errors.New("redirect URL must not carry credentials")
			}
			return nil
		},
	}
	return &HTTPDownloader{client: client, maxBytes: cfg.MaxBytes, timeout: cfg.Timeout, userAgent: cfg.UserAgent}, nil
}

// Download writes the remote input into dstDir and returns its local identity.
func (d *HTTPDownloader) Download(ctx context.Context, src *biz.MediaSource, dstDir string) (*biz.DownloadedFile, error) {
	if src == nil || strings.TrimSpace(src.URL) == "" {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeSourceUnavailable, false, errors.New("media source has no URL"))
	}
	parsed, err := url.Parse(src.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeSourceRejected, false, errors.New("media URL must be an absolute HTTPS URL"))
	}
	if !src.ExpiresAt.IsZero() && time.Now().After(src.ExpiresAt) {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeSourceRejected, true, errors.New("media URL expired before download"))
	}
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadFailed, false, fmt.Errorf("create download directory: %w", err))
	}
	nameBytes := make([]byte, 12)
	if _, err := rand.Read(nameBytes); err != nil {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadFailed, false, err)
	}
	path := filepath.Join(dstDir, "download-"+hex.EncodeToString(nameBytes)+".bin")

	downloadCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeSourceRejected, false, err)
	}
	req.Header.Set("User-Agent", d.userAgent)
	req.Header.Set("Accept", "video/*,application/octet-stream;q=0.9,*/*;q=0.1")
	resp, err := d.client.Do(req)
	if err != nil {
		if downloadCtx.Err() != nil {
			return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeProcessTimeout, true, downloadCtx.Err())
		}
		if errors.Is(err, errUnsafeRedirect) {
			return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeSourceRejected, false, errUnsafeRedirect)
		}
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeSourceUnavailable, true, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(biz.MediaStageDownload, resp.StatusCode)
	}
	if resp.ContentLength > d.maxBytes {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeMediaTooLarge, false,
			fmt.Errorf("remote media is %d bytes, limit is %d", resp.ContentLength, d.maxBytes))
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadFailed, false, err)
	}
	digest := sha256.New()
	limited := io.LimitReader(resp.Body, d.maxBytes+1)
	written, copyErr := io.Copy(file, io.TeeReader(limited, digest))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if downloadCtx.Err() != nil {
			return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeProcessTimeout, true, downloadCtx.Err())
		}
		err := copyErr
		if err == nil {
			err = closeErr
		}
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadIncomplete, true,
			fmt.Errorf("download interrupted after %d bytes: %w", written, err))
	}
	if written > d.maxBytes {
		_ = os.Remove(path)
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeMediaTooLarge, false,
			fmt.Errorf("downloaded media exceeds %d bytes", d.maxBytes))
	}
	if written == 0 {
		_ = os.Remove(path)
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadIncomplete, true, errors.New("remote media is empty"))
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		_ = os.Remove(path)
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadIncomplete, true,
			fmt.Errorf("downloaded %d bytes, content-length is %d", written, resp.ContentLength))
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != written {
		_ = os.Remove(path)
		return nil, biz.NewMediaError(biz.MediaStageDownload, biz.MediaCodeDownloadIncomplete, true, fmt.Errorf("downloaded file size mismatch"))
	}
	return &biz.DownloadedFile{
		Path:        path,
		SizeBytes:   written,
		SHA256:      hex.EncodeToString(digest.Sum(nil)),
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// statusError maps an upstream HTTP status to a classified media error.
func statusError(stage biz.MediaStage, status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return biz.NewMediaError(stage, biz.MediaCodeSourceRejected, true, fmt.Errorf("upstream returned %d", status))
	case status == http.StatusNotFound || status == http.StatusGone:
		return biz.NewMediaError(stage, biz.MediaCodeMediaNotFound, false, fmt.Errorf("upstream returned %d", status))
	case status == http.StatusTooManyRequests:
		return biz.NewMediaError(stage, biz.MediaCodeSourceRateLimited, true, fmt.Errorf("upstream returned %d", status))
	case status == http.StatusRequestTimeout || status >= 500:
		return biz.NewMediaError(stage, biz.MediaCodeSourceUnavailable, true, fmt.Errorf("upstream returned %d", status))
	default:
		return biz.NewMediaError(stage, biz.MediaCodeDownloadFailed, false, fmt.Errorf("upstream returned %d", status))
	}
}
