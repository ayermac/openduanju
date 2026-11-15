package data_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"

	"github.com/beilunyang/openhongguo/sdk"
	"github.com/beilunyang/openhongguo/sdk/model"
)

type fakePlaybackClient struct {
	playback *model.Playback
	err      error
	calls    int
}

func (f *fakePlaybackClient) ResolvePlayback(ctx context.Context, _ string, _ sdk.PlaybackOptions) (*model.Playback, error) {
	f.calls++
	if f.err != nil {
		return f.playback, f.err
	}
	return f.playback, nil
}

func newSDKResolverForTest(t *testing.T, client *fakePlaybackClient, quality string) *data.SDKResolver {
	t.Helper()
	return data.NewSDKResolverWithClient(client, data.SDKResolverConfig{Quality: quality, Timeout: time.Second})
}

func playbackWith(streams ...model.Stream) *model.Playback {
	return &model.Playback{EpisodeID: "ep-1", Streams: streams}
}

func TestSDKResolverPicksPlainMP4(t *testing.T) {
	client := &fakePlaybackClient{playback: playbackWith(
		model.Stream{Definition: "480p", URL: "https://cdn.example/480.mp4", Width: 854, Height: 480, Bitrate: 1000},
		model.Stream{Definition: "720p", URL: "https://cdn.example/720.mp4", Width: 1280, Height: 720, Bitrate: 2000, BackupURL: "https://backup.example/720.mp4"},
		model.Stream{Definition: "hls", URL: "https://cdn.example/index.m3u8", HLS: true, Width: 1920, Height: 1080},
	)}
	client.playback.ExpiresAt = time.Now().Add(10 * time.Minute).Unix()
	resolver := newSDKResolverForTest(t, client, "")
	source, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if source.URL != "https://cdn.example/720.mp4" || source.BackupURL != "https://backup.example/720.mp4" {
		t.Fatalf("source = %+v", source)
	}
	if client.calls != 1 {
		t.Fatalf("calls = %d", client.calls)
	}
}

func TestSDKResolverHonorsQuality(t *testing.T) {
	client := &fakePlaybackClient{playback: playbackWith(
		model.Stream{Definition: "480p", URL: "https://cdn.example/480.mp4", Width: 854, Height: 480},
		model.Stream{Definition: "720p", URL: "https://cdn.example/720.mp4", Width: 1280, Height: 720},
	)}
	resolver := newSDKResolverForTest(t, client, "480p")
	source, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if err != nil || source.URL != "https://cdn.example/480.mp4" {
		t.Fatalf("source = %+v err=%v", source, err)
	}
}

func TestSDKResolverRejectsProtectedWithoutKey(t *testing.T) {
	client := &fakePlaybackClient{playback: playbackWith(model.Stream{URL: "https://cdn.example/x.mp4"}), err: sdk.ErrDRMProtected}
	resolver := newSDKResolverForTest(t, client, "")
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaProtected {
		t.Fatalf("code = %s (%v)", code, err)
	}
	if biz.Retryable(err) {
		t.Fatal("protected media must not be retryable")
	}
}

func TestSDKResolverRejectsProtectedModelFlag(t *testing.T) {
	pb := playbackWith(model.Stream{URL: "https://cdn.example/x.mp4"})
	pb.Protected = true
	client := &fakePlaybackClient{playback: pb}
	resolver := newSDKResolverForTest(t, client, "")
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaProtected {
		t.Fatalf("code = %s (%v)", code, err)
	}
}

func TestSDKResolverRejectsHLSOnly(t *testing.T) {
	client := &fakePlaybackClient{playback: playbackWith(
		model.Stream{URL: "https://cdn.example/index.m3u8", HLS: true, Width: 1080, Height: 1920},
	)}
	resolver := newSDKResolverForTest(t, client, "")
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeMediaUnsupported {
		t.Fatalf("code = %s (%v)", code, err)
	}
}

func TestSDKResolverClassifiesErrors(t *testing.T) {
	cases := []struct {
		err   error
		code  biz.MediaErrorCode
		retry bool
	}{
		{sdk.ErrRateLimited, biz.MediaCodeSourceRateLimited, true},
		{sdk.ErrContentUnavailable, biz.MediaCodeMediaNotFound, false},
		{sdk.ErrAuthenticationRequired, biz.MediaCodeSourceRejected, false},
		{sdk.ErrDeviceRejected, biz.MediaCodeSourceRejected, false},
		{sdk.ErrTransport, biz.MediaCodeSourceUnavailable, true},
		{errors.New("boom"), biz.MediaCodeSourceUnavailable, true},
	}
	for _, tc := range cases {
		client := &fakePlaybackClient{err: tc.err}
		resolver := newSDKResolverForTest(t, client, "")
		_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
		code, _ := biz.MediaErrorCodeOf(err)
		if code != tc.code || biz.Retryable(err) != tc.retry {
			t.Fatalf("err %v -> %s retry=%v, want %s retry=%v", tc.err, code, biz.Retryable(err), tc.code, tc.retry)
		}
	}
}

func TestSDKResolverRejectsUnsafeURLAndExpiry(t *testing.T) {
	client := &fakePlaybackClient{playback: playbackWith(model.Stream{URL: "http://cdn.example/x.mp4"})}
	resolver := newSDKResolverForTest(t, client, "")
	if _, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"}); err == nil {
		t.Fatal("non-HTTPS stream must fail")
	}
	expired := &fakePlaybackClient{playback: playbackWith(model.Stream{URL: "https://cdn.example/x.mp4"})}
	expired.playback.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	resolver = newSDKResolverForTest(t, expired, "")
	if _, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"}); err == nil {
		t.Fatal("expired stream must fail")
	}
}

func TestSDKResolverRequiresProfilePath(t *testing.T) {
	if _, err := data.NewSDKResolver(data.SDKResolverConfig{}); err == nil {
		t.Fatal("empty profile path must fail")
	}
}
