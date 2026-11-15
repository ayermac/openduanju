// Command openduanju-coscheck is the real-COS acceptance harness. It uploads
// one file twice under the same key, confirms the exact object versions,
// presigns each version, downloads them over HTTPS without credentials and
// compares the post-download SHA-256 with the local file.
//
// It requires an isolated bucket with versioning enabled and a credential
// scoped to the acceptance prefix. Missing credentials or disabled versioning
// fail the check; it never claims success from local adapters alone.
package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/data"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
)

var (
	flagConf     string
	flagFile     string
	flagKey      string
	flagPrintURL bool
)

func init() {
	flag.StringVar(&flagConf, "conf", "../../configs", "config path")
	flag.StringVar(&flagFile, "file", "", "local media file to upload")
	flag.StringVar(&flagKey, "key", "", "object key (default cos-acceptance/<sha256>.mp4)")
	flag.BoolVar(&flagPrintURL, "print-url", false, "include signed URLs in the result (short lived)")
}

type versionCheck struct {
	VersionID      string `json:"version_id"`
	MetadataSHA256 string `json:"metadata_sha256"`
	DownloadSHA256 string `json:"download_sha256"`
	HashMatched    bool   `json:"hash_matched"`
	SignedURL      string `json:"signed_url,omitempty"`
}

type checkResult struct {
	OK            bool          `json:"ok"`
	Bucket        string        `json:"bucket"`
	Key           string        `json:"key"`
	LocalSHA256   string        `json:"local_sha256"`
	SizeBytes     int64         `json:"size_bytes"`
	Versioning    bool          `json:"versioning_enabled"`
	FirstVersion  *versionCheck `json:"first_version,omitempty"`
	SecondVersion *versionCheck `json:"second_version,omitempty"`
	Failure       string        `json:"failure,omitempty"`
	CheckedAtUTC  string        `json:"checked_at_utc"`
}

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if flagFile == "" {
		fail(errors.New("-file is required"))
		return 2
	}
	c := config.New(config.WithSource(file.NewSource(flagConf), env.NewSource("KRATOS")))
	defer c.Close()
	if err := c.Load(); err != nil {
		fail(fmt.Errorf("load config: %w", err))
		return 2
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		fail(fmt.Errorf("scan config: %w", err))
		return 2
	}
	media := bc.GetMedia()
	if media == nil {
		fail(errors.New("media configuration is missing"))
		return 2
	}
	cosCfg := media.GetStorage().GetCos()
	storage, err := data.NewCOSStorage(data.COSStorageConfig{
		Bucket:       cosCfg.GetBucket(),
		Region:       cosCfg.GetRegion(),
		SecretID:     cosCfg.GetSecretId(),
		SecretKey:    cosCfg.GetSecretKey(),
		SessionToken: cosCfg.GetSessionToken(),
	})
	if err != nil {
		fail(err)
		return 2
	}
	sha, md5Base64, size, err := fileDigests(flagFile)
	if err != nil {
		fail(err)
		return 2
	}
	key := flagKey
	if key == "" {
		key = filepath.ToSlash(filepath.Join("cos-acceptance", sha+".mp4"))
	}
	result := checkResult{
		Bucket:       cosCfg.GetBucket(),
		Key:          key,
		LocalSHA256:  sha,
		SizeBytes:    size,
		CheckedAtUTC: time.Now().UTC().Format(time.RFC3339),
	}
	var ttl time.Duration
	if signTTL := media.GetStorage().GetSignTtl(); signTTL != nil {
		ttl = signTTL.AsDuration()
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	first, err := uploadAndVerify(ctx, storage, flagFile, key, sha, md5Base64, size, ttl, flagPrintURL)
	if err != nil {
		result.Failure = err.Error()
		emit(result)
		return 1
	}
	result.FirstVersion = first
	result.Versioning = first.VersionID != ""
	if !result.Versioning {
		result.Failure = "bucket versioning is not enabled: a signature cannot be bound to an object version"
		emit(result)
		return 1
	}

	second, err := uploadAndVerify(ctx, storage, flagFile, key, sha, md5Base64, size, ttl, flagPrintURL)
	if err != nil {
		result.Failure = err.Error()
		emit(result)
		return 1
	}
	result.SecondVersion = second
	if second.VersionID == first.VersionID {
		result.Failure = "the second upload did not create a new version"
		emit(result)
		return 1
	}

	// The first version must still be addressable after the overwrite.
	oldHead, err := storage.Head(ctx, biz.ObjectRef{Key: key, VersionID: first.VersionID})
	if err != nil {
		result.Failure = "first version is no longer readable: " + err.Error()
		emit(result)
		return 1
	}
	if oldHead.VersionID != first.VersionID || oldHead.MetadataSHA256 != sha {
		result.Failure = "first version identity changed after the second upload"
		emit(result)
		return 1
	}
	result.OK = true
	emit(result)
	return 0
}

func uploadAndVerify(ctx context.Context, storage biz.ObjectStorage, path, key, sha, md5Base64 string, size int64, ttl time.Duration, printURL bool) (*versionCheck, error) {
	uploaded, err := storage.PutFile(ctx, biz.PutObjectRequest{
		Key:         key,
		Path:        path,
		ContentType: "video/mp4",
		SHA256:      sha,
		MD5Base64:   md5Base64,
		SizeBytes:   size,
	})
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	confirmed, err := storage.Head(ctx, uploaded.ObjectRef)
	if err != nil {
		return nil, fmt.Errorf("head uploaded version: %w", err)
	}
	if confirmed.SizeBytes != size || confirmed.MetadataSHA256 != sha {
		return nil, fmt.Errorf("head mismatch: size=%d metadata=%q", confirmed.SizeBytes, confirmed.MetadataSHA256)
	}
	signed, err := storage.PresignGet(ctx, confirmed.ObjectRef, ttl)
	if err != nil {
		return nil, fmt.Errorf("presign: %w", err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		return nil, fmt.Errorf("parse signed url: %w", err)
	}
	if confirmed.VersionID != "" && parsed.Query().Get("versionId") != confirmed.VersionID {
		return nil, errors.New("signed URL does not bind the confirmed object version")
	}
	downloadSHA, err := downloadSHA256(ctx, signed.URL)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	check := &versionCheck{
		VersionID:      confirmed.VersionID,
		MetadataSHA256: confirmed.MetadataSHA256,
		DownloadSHA256: downloadSHA,
		HashMatched:    downloadSHA == sha,
	}
	if printURL {
		check.SignedURL = signed.URL
	}
	if !check.HashMatched {
		return check, errors.New("post-download SHA-256 does not match the local file")
	}
	return check, nil
}

func downloadSHA256(ctx context.Context, rawURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download status %d", resp.StatusCode)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(resp.Body, 8<<30)); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func fileDigests(path string) (string, string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", 0, err
	}
	defer file.Close()
	sha := sha256.New()
	rawMD5 := md5.New()
	size, err := io.Copy(io.MultiWriter(sha, rawMD5), file)
	if err != nil {
		return "", "", 0, err
	}
	if size <= 0 {
		return "", "", 0, errors.New("media file is empty")
	}
	return hex.EncodeToString(sha.Sum(nil)), base64.StdEncoding.EncodeToString(rawMD5.Sum(nil)), size, nil
}

func emit(result checkResult) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(result)
}

func fail(err error) {
	_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
}
