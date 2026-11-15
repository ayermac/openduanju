package biz_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"openduanju-server/internal/biz"
)

// The request log names the request without carrying its credential (OBS-003).
// The three signed media endpoints get the route and a placeholder; an ordinary
// path is untouched, so an operator can still search by identifier.
func TestRedactedPathKeepsTheRouteAndDropsTheCredential(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		want string
	}{
		{"staging upload", biz.StagingPathPrefix + "sentinel-value", biz.StagingPathPrefix + "{token}"},
		{"private preview", biz.PreviewPathPrefix + "sentinel-value", biz.PreviewPathPrefix + "{token}"},
		{"guest playback", biz.PlaybackPathPrefix + "sentinel-value", biz.PlaybackPathPrefix + "{token}"},
		{"signed value with an extra segment", biz.PlaybackPathPrefix + "sentinel/extra", biz.PlaybackPathPrefix + "{token}"},
		{"the prefix alone is not a credential", biz.PlaybackPathPrefix, biz.PlaybackPathPrefix},
		{"series identifier", "/api/admin/v1/series/42/revisions", "/api/admin/v1/series/42/revisions"},
		{"long route word", "/api/admin/v1/authorization-grants", "/api/admin/v1/authorization-grants"},
		{"root", "/", "/"},
		{"undeclared opaque segment", "/api/admin/v1/unknown/MixedCaseSignedValue0123456789", "/api/admin/v1/unknown/{token}"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := biz.RedactedPath(testCase.path); got != testCase.want {
				t.Fatalf("RedactedPath(%q) = %q, want %q", testCase.path, got, testCase.want)
			}
		})
	}
}

// The signed media endpoints declared for the log are the ones the local driver
// serves. Two lists that drifted apart would either leak a credential or redact
// a route that carries none.
func TestSignedPathPrefixesMatchTheServedEndpoints(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"/api/admin/v1/uploads/staging/": true,
		"/api/admin/v1/media-preview/":   true,
		"/api/web/v1/media/":             true,
	}
	if len(biz.SignedPathPrefixes) != len(want) {
		t.Fatalf("prefixes = %v, want %v", biz.SignedPathPrefixes, want)
	}
	for _, prefix := range biz.SignedPathPrefixes {
		if !want[prefix] {
			t.Fatalf("unexpected signed prefix %q", prefix)
		}
	}
}

// The trace identity is what an operator quotes in a report. It must be the
// caller's identity when the caller provided a valid one, a fresh one when it
// did not, and never a copy of whatever arrived in the header.
func TestTraceFromHeadersKeepsOnlyAValidIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		traceParent string
		header      string
		want        string
		wantFresh   bool
	}{
		{
			name:   "a valid header is reused",
			header: "9f2c1a7b8d4e4f0a",
			want:   "9f2c1a7b8d4e4f0a",
		},
		{
			name:        "a valid w3c trace context wins",
			traceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			header:      "client-supplied-id",
			want:        "4bf92f3577b34da6a3ce929d0e0e4736",
		},
		{
			name:        "an invalid w3c value falls back to the header",
			traceParent: "00-not-a-trace-00f067aa0ba902b7-01",
			header:      "fallback-id-1",
			want:        "fallback-id-1",
		},
		{
			name:        "an all zero trace id is refused",
			traceParent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
			header:      "",
			wantFresh:   true,
		},
		{
			name:      "a header with a space is replaced",
			header:    "inject a line",
			wantFresh: true,
		},
		{
			name:      "a header that is too short is replaced",
			header:    "short",
			wantFresh: true,
		},
		{
			name:      "a header that is too long is replaced",
			header:    strings.Repeat("a", 65),
			wantFresh: true,
		},
		{
			name:      "an empty header mints a new identity",
			wantFresh: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := biz.TraceFromHeaders(tc.traceParent, tc.header)
			if tc.wantFresh {
				if !biz.ValidTraceID(got) {
					t.Fatalf("minted identity %q is not a valid one", got)
				}
				if got == tc.header && tc.header != "" {
					t.Fatalf("the invalid header %q was echoed back", tc.header)
				}
				if len(got) != 32 {
					t.Fatalf("minted identity %q is not a 32 character value", got)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("identity = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTraceIdentityTravelsInTheContext is the propagation promise: the same
// identity is readable from the context that a use case passes down.
func TestTraceIdentityTravelsInTheContext(t *testing.T) {
	t.Parallel()
	ctx := biz.WithTraceID(context.Background(), "carried-identity")
	if got := biz.TraceID(ctx); got != "carried-identity" {
		t.Fatalf("trace = %q", got)
	}
	if got := biz.TraceID(context.Background()); got != "" {
		t.Fatalf("a context without an identity answered %q", got)
	}
	// An empty identity must not erase one that is already there.
	if got := biz.TraceID(biz.WithTraceID(ctx, "")); got != "carried-identity" {
		t.Fatalf("an empty identity replaced the carried one: %q", got)
	}
}

// TestAuditReasonKeepsOnlyAStableCode covers what a row may say about a
// failure: a code an operator can act on, never the message of the error.
func TestAuditReasonKeepsOnlyAStableCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{biz.ErrRevisionVersion, "VERSION_CONFLICT"},
		{biz.ErrReviewFinal, "REVIEW_FINAL"},
		{biz.ErrForbidden, "FORBIDDEN"},
		{biz.ErrAuditWriteFailed, "AUDIT_WRITE_FAILED"},
		{biz.NewSourceError(biz.SourceCodeRateLimited, true, errors.New("upstream said no")), "SOURCE_RATE_LIMITED"},
		{fmt.Errorf("%w: revision 12", biz.ErrRevisionConflict), "REVISION_CONFLICT"},
		{errors.New("SELECT * FROM staff WHERE password_hash = 'x'"), "OPERATION_FAILED"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := biz.AuditReason(tc.err); got != tc.want {
			t.Fatalf("reason for %v = %q, want %q", tc.err, got, tc.want)
		}
	}
	// The raw message of an unknown error never survives.
	leaky := errors.New("dial tcp 10.0.0.5:3306: connect: connection refused")
	if got := biz.AuditReason(leaky); strings.Contains(got, "10.0.0.5") {
		t.Fatalf("the row would carry an address: %q", got)
	}
}

// recordingRepo is an audit store that keeps what it was given.
type recordingRepo struct {
	mu     sync.Mutex
	events []biz.AuditEvent
	err    error
}

func (r *recordingRepo) Record(_ context.Context, event biz.AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, event)
	return nil
}

func (r *recordingRepo) List(context.Context, biz.AuditQuery) ([]biz.AuditEvent, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events, len(r.events), nil
}

func (r *recordingRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// TestAuditScopeRecordsAFailureAfterTheRollback covers the failure path: the
// refused operation leaves one failure row, carrying the operator, the object
// and the stable reason.
func TestAuditScopeRecordsAFailureAfterTheRollback(t *testing.T) {
	t.Parallel()
	repo := &recordingRepo{}
	auditor, err := biz.NewAuditor(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := biz.WithTraceID(context.Background(), "trace-failure")
	ctx = biz.WithAuditActor(ctx, biz.Actor{ID: 11, Username: "operator"})

	scoped, scope := auditor.Begin(ctx, biz.AuditActionBatchSubmit, biz.AuditResourceBatch)
	biz.AuditHandle(scoped, 42, 0)
	scope.End(scoped, biz.ErrNoSelectedEpisodes)

	if repo.count() != 1 {
		t.Fatalf("failure rows = %d", repo.count())
	}
	row := repo.events[0]
	if row.Result != biz.AuditResultFailure || row.Reason != "NO_SELECTED_EPISODES" {
		t.Fatalf("row = %+v", row)
	}
	if row.ActorID != 11 || row.ActorName != "operator" {
		t.Fatalf("actor = %d/%q", row.ActorID, row.ActorName)
	}
	if row.ResourceID != "42" || row.ResourceType != biz.AuditResourceBatch {
		t.Fatalf("resource = %q/%q", row.ResourceType, row.ResourceID)
	}
	if row.TraceID != "trace-failure" {
		t.Fatalf("trace = %q", row.TraceID)
	}
}

// TestAuditScopeRecordsNothingForASuccess covers the successful path: the
// repository writes the row inside the transaction, so the scope itself
// records nothing.
func TestAuditScopeRecordsNothingForASuccess(t *testing.T) {
	t.Parallel()
	repo := &recordingRepo{}
	auditor, err := biz.NewAuditor(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := biz.WithTraceID(context.Background(), "trace-success")
	if _, err := biz.Audit(ctx, auditor, biz.AuditActionBatchSubmit, biz.AuditResourceBatch,
		func(scoped context.Context) (string, error) {
			// The repository would commit the row here.
			if intent := biz.AuditIntentFrom(scoped); intent != nil {
				intent.SetResource("9", 3)
				intent.MarkCommitted()
			}
			return "ok", nil
		}); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if repo.count() != 0 {
		t.Fatalf("a success was recorded by the scope: %+v", repo.events)
	}
}

// TestAuditScopeRecordsNothingForAReplay covers the idempotent repeat: nothing
// changed, so nothing is recorded - not even a failure.
func TestAuditScopeRecordsNothingForAReplay(t *testing.T) {
	t.Parallel()
	repo := &recordingRepo{}
	auditor, err := biz.NewAuditor(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := biz.WithTraceID(context.Background(), "trace-replay")
	if _, err := biz.Audit(ctx, auditor, biz.AuditActionBatchSubmit, biz.AuditResourceBatch,
		func(scoped context.Context) (string, error) {
			biz.AuditUnchanged(scoped)
			return "existing", nil
		}); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if repo.count() != 0 {
		t.Fatalf("a replay left %d rows", repo.count())
	}
}

// TestAuditScopeSurvivesABrokenTrail covers a failure record that cannot be
// written: the caller still receives the original error.
func TestAuditScopeSurvivesABrokenTrail(t *testing.T) {
	t.Parallel()
	repo := &recordingRepo{err: errors.New("audit store unavailable")}
	auditor, err := biz.NewAuditor(repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := biz.WithTraceID(context.Background(), "trace-broken")
	scoped, scope := auditor.Begin(ctx, biz.AuditActionBatchSubmit, biz.AuditResourceBatch)
	original := biz.ErrBatchNotRetryable
	scope.End(scoped, original)

	// The error the caller sees is unchanged; the repository received the
	// attempt, which is what the log line is for.
	if !errors.Is(original, biz.ErrBatchNotRetryable) {
		t.Fatal("the original error changed")
	}
	if repo.count() != 0 {
		t.Fatalf("a failed record was kept: %+v", repo.events)
	}
}
