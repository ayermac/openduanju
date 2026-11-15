package data_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
)

func writeBridge(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bridge.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func newResolver(t *testing.T, command string, timeout time.Duration) *data.CommandResolver {
	t.Helper()
	resolver, err := data.NewCommandResolver(data.CommandResolverConfig{Command: command, Timeout: timeout})
	if err != nil {
		t.Fatalf("NewCommandResolver: %v", err)
	}
	return resolver
}

func TestCommandResolverParsesBridgeResponse(t *testing.T) {
	expires := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`set -e
[ "$1" = "--episode-id" ] || exit 9
[ "$2" = "ep-1" ] || exit 9
[ "$3" = "--source" ] || exit 9
[ "$4" = "hongguo" ] || exit 9
[ "$5" = "--quality" ] || exit 9
[ "$6" = "best" ] || exit 9
echo '{"ok":true,"url":"https://cdn.example/ep.mp4","content_type":"video/mp4","expires_at":"%s"}'
`, expires)
	resolver := newResolver(t, writeBridge(t, body), 5*time.Second)

	src, err := resolver.Resolve(context.Background(), biz.ResolveRequest{
		SourceCode:        "hongguo",
		ExternalEpisodeID: "ep-1",
		Quality:           "best",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if src.URL != "https://cdn.example/ep.mp4" || src.ContentType != "video/mp4" {
		t.Fatalf("source = %+v", src)
	}
	if src.ExpiresAt.IsZero() {
		t.Fatal("expiry was not parsed")
	}
}

func TestCommandResolverMapsBridgeFailureCodes(t *testing.T) {
	cases := []struct {
		bridge    string
		code      biz.MediaErrorCode
		retryable bool
	}{
		{`echo '{"ok":false,"code":"MEDIA_PROTECTED","retryable":false}'; exit 1`, biz.MediaCodeMediaProtected, false},
		{`echo '{"ok":false,"code":"SOURCE_RATE_LIMITED","retryable":true}'; exit 1`, biz.MediaCodeSourceRateLimited, true},
		{`echo '{"ok":false,"code":"SOMETHING_NEW","retryable":true}'; exit 1`, biz.MediaCodeSourceUnavailable, true},
		{`echo 'not json'; exit 1`, biz.MediaCodeSourceUnavailable, true},
	}
	for _, tc := range cases {
		resolver := newResolver(t, writeBridge(t, tc.bridge), 5*time.Second)
		_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
		code, ok := biz.MediaErrorCodeOf(err)
		if !ok || code != tc.code {
			t.Fatalf("bridge %q: code = %s (%v), want %s", tc.bridge, code, err, tc.code)
		}
		if biz.Retryable(err) != tc.retryable {
			t.Fatalf("bridge %q: retryable = %v, want %v", tc.bridge, biz.Retryable(err), tc.retryable)
		}
	}
}

func TestCommandResolverRejectsNonHTTPSURL(t *testing.T) {
	body := `echo '{"ok":true,"url":"http://cdn.example/ep.mp4"}'`
	resolver := newResolver(t, writeBridge(t, body), 5*time.Second)
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceRejected {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeSourceRejected)
	}
}

func TestCommandResolverRejectsExpiredURL(t *testing.T) {
	expires := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`echo '{"ok":true,"url":"https://cdn.example/ep.mp4","expires_at":"%s"}'`, expires)
	resolver := newResolver(t, writeBridge(t, body), 5*time.Second)
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceRejected {
		t.Fatalf("code = %s, want %s", code, biz.MediaCodeSourceRejected)
	}
	if !biz.Retryable(err) {
		t.Fatal("an expired URL can be retried after a fresh resolve")
	}
}

func TestCommandResolverRejectsInvalidPayload(t *testing.T) {
	resolver := newResolver(t, writeBridge(t, `echo '{"ok":true}'`), 5*time.Second)
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceRejected {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeSourceRejected)
	}
}

func TestCommandResolverTimesOut(t *testing.T) {
	resolver := newResolver(t, writeBridge(t, "sleep 5"), 300*time.Millisecond)
	start := time.Now()
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if code, _ := biz.MediaErrorCodeOf(err); code != biz.MediaCodeSourceUnavailable {
		t.Fatalf("code = %s (%v), want %s", code, err, biz.MediaCodeSourceUnavailable)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("resolver timeout took %s", elapsed)
	}
}

func TestCommandResolverRequiresConfiguration(t *testing.T) {
	if _, err := data.NewCommandResolver(data.CommandResolverConfig{}); err == nil {
		t.Fatal("empty command must fail")
	}
	if _, err := data.NewCommandResolver(data.CommandResolverConfig{Command: "/nonexistent/bridge"}); err == nil {
		t.Fatal("missing executable must fail")
	}
}

func TestCommandResolverRequiresEpisodeAndSource(t *testing.T) {
	resolver := newResolver(t, writeBridge(t, `echo '{"ok":true,"url":"https://cdn.example/ep.mp4"}'`), 5*time.Second)
	if _, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo"}); err == nil {
		t.Fatal("missing episode id must fail")
	}
	if _, err := resolver.Resolve(context.Background(), biz.ResolveRequest{ExternalEpisodeID: "ep-1"}); err == nil {
		t.Fatal("missing source code must fail")
	}
}

func TestCommandResolverDoesNotEchoBridgeMessage(t *testing.T) {
	body := `echo '{"ok":false,"code":"SOURCE_REJECTED","retryable":false,"message":"secret-device-profile"}'; exit 1`
	resolver := newResolver(t, writeBridge(t, body), 5*time.Second)
	_, err := resolver.Resolve(context.Background(), biz.ResolveRequest{SourceCode: "hongguo", ExternalEpisodeID: "ep-1"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "secret-device-profile") {
		t.Fatalf("bridge message leaked into the error: %v", err)
	}
}
