package data

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"openduanju-server/internal/biz"

	"github.com/beilunyang/openhongguo/adapters"
	"github.com/beilunyang/openhongguo/sdk"
	"github.com/beilunyang/openhongguo/sdk/model"
)

// playbackClient is the narrow SDK surface this adapter uses. It never exposes
// ContentKey or device reset paths.
type playbackClient interface {
	ResolvePlayback(ctx context.Context, episodeID string, opts sdk.PlaybackOptions) (*model.Playback, error)
}

// SDKResolverConfig pins the SDK client to a single anonymous profile.
type SDKResolverConfig struct {
	ProfilePath string
	Quality     string
	Timeout     time.Duration
}

// SDKResolver resolves playback models through the pinned Apache-2.0
// openhongguo SDK. Protected models are rejected; content keys are never
// requested or derived.
type SDKResolver struct {
	client  playbackClient
	quality string
	timeout time.Duration
}

// NewSDKResolver builds the real SDK client. Construction performs no network
// call; the anonymous profile is registered lazily by the SDK.
func NewSDKResolver(cfg SDKResolverConfig) (*SDKResolver, error) {
	if strings.TrimSpace(cfg.ProfilePath) == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, errors.New("sdk resolver profile path is required"))
	}
	zero := 0
	client, err := adapters.NewClient(cfg.ProfilePath, sdk.Options{
		// No automatic device rotation and no hidden retries; the pipeline owns
		// retry policy and the operator owns identity lifecycle.
		DeviceResets:  sdk.Int(zero),
		RetryAttempts: 1,
	})
	if err != nil {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, err)
	}
	return NewSDKResolverWithClient(client, cfg), nil
}

func NewSDKResolverWithClient(client playbackClient, cfg SDKResolverConfig) *SDKResolver {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	quality := strings.TrimSpace(cfg.Quality)
	if quality == "best" {
		quality = ""
	}
	return &SDKResolver{client: client, quality: quality, timeout: timeout}
}

// Resolve returns one HTTPS MP4 stream. Protected content is classified
// without calling ContentKey.
func (r *SDKResolver) Resolve(ctx context.Context, req biz.ResolveRequest) (*biz.MediaSource, error) {
	if strings.TrimSpace(req.ExternalEpisodeID) == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeConfigInvalid, false, errors.New("external episode id is required"))
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	playback, err := r.client.ResolvePlayback(runCtx, req.ExternalEpisodeID, sdk.PlaybackOptions{})
	if err != nil {
		return nil, mapSDKError(err)
	}
	if playback == nil {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, true, errors.New("sdk returned no playback model"))
	}
	if playback.Protected {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeMediaProtected, false, errors.New("sdk playback is DRM protected"))
	}
	stream, err := chooseMP4Stream(playback, r.quality)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(stream.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceRejected, false, errors.New("sdk returned a non-HTTPS media URL"))
	}
	source := &biz.MediaSource{
		Provider:    req.SourceCode,
		URL:         stream.URL,
		ContentType: "video/mp4",
	}
	if backup := strings.TrimSpace(stream.BackupURL); backup != "" {
		if parsedBackup, err := url.Parse(backup); err == nil && parsedBackup.Scheme == "https" && parsedBackup.Host != "" {
			source.BackupURL = backup
		}
	}
	if playback.ExpiresAt > 0 {
		source.ExpiresAt = time.Unix(playback.ExpiresAt, 0).UTC()
		if time.Now().After(source.ExpiresAt) {
			return nil, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceRejected, true, errors.New("sdk returned an expired media URL"))
		}
	}
	return source, nil
}

func chooseMP4Stream(playback *model.Playback, quality string) (model.Stream, error) {
	candidates := make([]model.Stream, 0, len(playback.Streams))
	for _, stream := range playback.Streams {
		if strings.TrimSpace(stream.URL) == "" || stream.HLS || stream.Dash || stream.Protected {
			continue
		}
		candidates = append(candidates, stream)
	}
	if len(candidates) == 0 {
		return model.Stream{}, biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeMediaUnsupported, false, errors.New("sdk playback has no plain MP4 stream"))
	}
	if quality != "" {
		for _, stream := range candidates {
			if strings.EqualFold(stream.Definition, quality) || strings.EqualFold(stream.Quality, quality) {
				return stream, nil
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].Width * candidates[i].Height
		right := candidates[j].Width * candidates[j].Height
		if left != right {
			return left > right
		}
		return candidates[i].Bitrate > candidates[j].Bitrate
	})
	return candidates[0], nil
}

func mapSDKError(err error) error {
	switch {
	case errors.Is(err, sdk.ErrDRMProtected):
		return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeMediaProtected, false, err)
	case errors.Is(err, sdk.ErrRateLimited):
		return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceRateLimited, true, err)
	case errors.Is(err, sdk.ErrContentUnavailable):
		return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeMediaNotFound, false, err)
	case errors.Is(err, sdk.ErrAuthenticationRequired), errors.Is(err, sdk.ErrChallengeRequired), errors.Is(err, sdk.ErrDeviceRejected), errors.Is(err, sdk.ErrSignatureRejected):
		return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceRejected, false, err)
	case errors.Is(err, sdk.ErrTransport), errors.Is(err, sdk.ErrAnonymousLimited):
		return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, true, err)
	default:
		return biz.NewMediaError(biz.MediaStageAcquire, biz.MediaCodeSourceUnavailable, true, fmt.Errorf("sdk playback failed: %w", err))
	}
}
