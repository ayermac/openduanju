package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// credentialMarker is a synthetic value, not a real signature: the tests below
// assert that it never reaches a log line, whatever endpoint it is presented at.
const credentialMarker = "synthetic-signed-credential-sentinel"

// TestTraceFilterDoesNotLogMediaCredentials covers OBS-003. Three endpoints
// carry a signed credential in the path because a browser fetches them
// directly: the upload staging PUT, the private preview read and the guest
// playback read. The request log keeps the route and must never keep the value.
func TestTraceFilterDoesNotLogMediaCredentials(t *testing.T) {
	paths := map[string]string{
		"upload staging": "/api/admin/v1/uploads/staging/" + credentialMarker,
		"media preview":  "/api/admin/v1/media-preview/" + credentialMarker,
		"guest playback": "/api/web/v1/media/" + credentialMarker,
	}
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			restore := swapDefaultLogger(&output)
			defer restore()

			request := httptest.NewRequest(stdhttp.MethodGet, path, nil)
			recorder := httptest.NewRecorder()
			traceFilter(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				w.WriteHeader(stdhttp.StatusOK)
			})).ServeHTTP(recorder, request)

			if recorder.Header().Get("X-Trace-Id") == "" {
				t.Fatal("the response lost its identity")
			}
			line := output.String()
			if strings.Contains(line, credentialMarker) {
				t.Fatalf("the request log carries the signed credential: %s", line)
			}
			// The line is still useful: the route, the method, the status and
			// the identity are what an operator searches with.
			entry := decodeLogLine(t, line)
			if entry["path"] != strings.TrimSuffix(path, credentialMarker)+"{token}" {
				t.Fatalf("path field = %v", entry["path"])
			}
			if entry["method"] != stdhttp.MethodGet || entry["status"] != float64(200) {
				t.Fatalf("method/status = %v/%v", entry["method"], entry["status"])
			}
			// The identity field itself is added by the logger wired in main;
			// that it travels with the line is covered by
			// TestTraceFilterSetsTheHeaderAndCarriesTheIdentity.
		})
	}
}

// TestTraceFilterKeepsOrdinaryPathsReadable proves the redaction is narrow: the
// identifiers an operator searches for survive, so the log is not emptied to
// protect a credential.
func TestTraceFilterKeepsOrdinaryPathsReadable(t *testing.T) {
	ordinary := []string{
		"/api/admin/v1/series/1844674407370955161/revisions",
		"/api/admin/v1/authorization-grants/42/revoke",
		"/api/web/v1/series/7",
		"/api/admin/v1/media-batches",
	}
	for _, path := range ordinary {
		t.Run(path, func(t *testing.T) {
			var output bytes.Buffer
			restore := swapDefaultLogger(&output)
			defer restore()

			recorder := httptest.NewRecorder()
			traceFilter(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				w.WriteHeader(stdhttp.StatusOK)
			})).ServeHTTP(recorder, httptest.NewRequest(stdhttp.MethodGet, path, nil))

			if entry := decodeLogLine(t, output.String()); entry["path"] != path {
				t.Fatalf("path field = %v, want %s", entry["path"], path)
			}
		})
	}
}

// swapDefaultLogger sends the default log to buffer for the duration of one
// test, so an assertion reads the same line an operator would.
func swapDefaultLogger(buffer *bytes.Buffer) func() {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, nil)))
	return func() { slog.SetDefault(previous) }
}

// decodeLogLine reads the last JSON line the default logger wrote.
func decodeLogLine(t *testing.T, line string) map[string]any {
	t.Helper()
	trimmed := strings.TrimSpace(line)
	if index := strings.LastIndex(trimmed, "\n"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	entry := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
		t.Fatalf("log line is not JSON: %s (%v)", trimmed, err)
	}
	return entry
}
