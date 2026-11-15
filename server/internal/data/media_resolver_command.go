package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"openduanju-server/internal/biz"
)

// CommandResolverConfig points at a trusted, operator-supplied SDK bridge.
// The executable owns its own credentials and device profile; this process
// only receives the resolved short-lived media URL.
type CommandResolverConfig struct {
	Command string
	Args    []string
	Timeout time.Duration
}

// CommandResolver resolves one external episode by invoking the bridge as a
// short-lived subprocess (no shell) and parsing a strict JSON response.
//
// Bridge contract:
//
//	argv: <command> [configured args...] --episode-id <id> --source <code> --quality <q>
//	stdout: {"ok":true,"url":"https://...","expires_at":"RFC3339","content_type":"video/mp4"}
//	        {"ok":false,"code":"MEDIA_PROTECTED","retryable":false}
//	exit:   0 when the JSON contract is followed
type CommandResolver struct {
	path    string
	args    []string
	timeout time.Duration
}

// NewCommandResolver validates the configured executable without running it.
func NewCommandResolver(cfg CommandResolverConfig) (*CommandResolver, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, errors.New("media resolver command is not configured"))
	}
	path, err := exec.LookPath(cfg.Command)
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, fmt.Errorf("media resolver command %q is not executable: %w", cfg.Command, err))
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &CommandResolver{path: path, args: append([]string(nil), cfg.Args...), timeout: cfg.Timeout}, nil
}

type resolverResponse struct {
	OK          bool   `json:"ok"`
	URL         string `json:"url"`
	BackupURL   string `json:"backup_url"`
	ExpiresAt   string `json:"expires_at"`
	ContentType string `json:"content_type"`
	Code        string `json:"code"`
	Retryable   bool   `json:"retryable"`
}

// Resolve runs one bridge invocation with a deadline.
func (r *CommandResolver) Resolve(ctx context.Context, req biz.ResolveRequest) (*biz.MediaSource, error) {
	if strings.TrimSpace(req.ExternalEpisodeID) == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, errors.New("external episode id is required"))
	}
	if strings.TrimSpace(req.SourceCode) == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, errors.New("source code is required"))
	}
	args := append([]string(nil), r.args...)
	args = append(args,
		"--episode-id", req.ExternalEpisodeID,
		"--source", req.SourceCode,
	)
	if req.Quality != "" {
		args = append(args, "--quality", req.Quality)
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, r.path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = os.Environ()
	var stdout, stderr boundedBuffer
	stdout.limit = 1 << 20
	stderr.limit = 4 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runCtx.Err() != nil {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, true, fmt.Errorf("media resolver timed out after %s", r.timeout))
	}
	if runErr != nil {
		// A bridge may still explain the failure in its JSON body.
		if resp, parseErr := parseResolverResponse(stdout.Bytes()); parseErr == nil && !resp.OK {
			return nil, resolverFailure(resp)
		}
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, true, fmt.Errorf("media resolver exited with an error: %w", runErr))
	}
	resp, parseErr := parseResolverResponse(stdout.Bytes())
	if parseErr != nil {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, false, fmt.Errorf("media resolver returned an invalid payload: %w", parseErr))
	}
	if !resp.OK {
		return nil, resolverFailure(resp)
	}
	parsed, err := url.Parse(resp.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceRejected, false, errors.New("media resolver returned a non-HTTPS URL"))
	}
	source := &biz.MediaSource{
		Provider:    req.SourceCode,
		URL:         resp.URL,
		BackupURL:   resp.BackupURL,
		ContentType: resp.ContentType,
	}
	if source.ContentType == "" {
		source.ContentType = "video/mp4"
	}
	if resp.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, resp.ExpiresAt)
		if err != nil {
			return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, false, errors.New("media resolver returned an invalid expiry"))
		}
		if time.Now().After(expiresAt) {
			return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceRejected, true, errors.New("media resolver returned an expired URL"))
		}
		source.ExpiresAt = expiresAt
	}
	return source, nil
}

func parseResolverResponse(raw []byte) (*resolverResponse, error) {
	var resp resolverResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func resolverFailure(resp *resolverResponse) error {
	code, ok := bridgeCode(resp.Code)
	if !ok {
		code = biz.MediaCodeSourceUnavailable
	}
	return biz.NewMediaError(biz.MediaStageAcquire, code, resp.Retryable, fmt.Errorf("media resolver reported %s", code))
}

// bridgeCode only accepts the stable codes this service understands; any
// other bridge value becomes SOURCE_UNAVAILABLE instead of leaking through.
func bridgeCode(raw string) (biz.MediaErrorCode, bool) {
	switch biz.MediaErrorCode(strings.ToUpper(strings.TrimSpace(raw))) {
	case biz.MediaCodeSourceUnavailable:
		return biz.MediaCodeSourceUnavailable, true
	case biz.MediaCodeSourceRejected:
		return biz.MediaCodeSourceRejected, true
	case biz.MediaCodeSourceRateLimited:
		return biz.MediaCodeSourceRateLimited, true
	case biz.MediaCodeMediaProtected:
		return biz.MediaCodeMediaProtected, true
	case biz.MediaCodeMediaNotFound:
		return biz.MediaCodeMediaNotFound, true
	case biz.MediaCodeMediaUnsupported:
		return biz.MediaCodeMediaUnsupported, true
	default:
		return "", false
	}
}
