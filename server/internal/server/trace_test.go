package server

import (
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openduanju-server/internal/biz"

	"github.com/go-kratos/kratos/v3/errors"
)

// TestTraceFilterSetsTheHeaderAndCarriesTheIdentity covers the promise an
// operator relies on: every response carries the identity of its request, and
// the request context carries the same value.
func TestTraceFilterSetsTheHeaderAndCarriesTheIdentity(t *testing.T) {
	var seen string
	handler := traceFilter(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		seen = biz.TraceID(r.Context())
		w.WriteHeader(stdhttp.StatusNoContent)
	}))
	request := httptest.NewRequest(stdhttp.MethodGet, "/api/admin/v1/audit", nil)
	request.Header.Set(biz.HeaderTraceID, "caller-trace-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get(biz.HeaderTraceID); got != "caller-trace-1" {
		t.Fatalf("response header = %q", got)
	}
	if seen != "caller-trace-1" {
		t.Fatalf("context identity = %q", seen)
	}
	if recorder.Code != stdhttp.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
}

// TestTraceFilterReplacesAnInvalidIdentity covers the refusal side: a header
// that is not a usable identity is not echoed back, and the response still
// names a request.
func TestTraceFilterReplacesAnInvalidIdentity(t *testing.T) {
	handler := traceFilter(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
	}))
	request := httptest.NewRequest(stdhttp.MethodGet, "/", nil)
	request.Header.Set(biz.HeaderTraceID, "not a valid identity")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	got := recorder.Header().Get(biz.HeaderTraceID)
	if got == "not a valid identity" {
		t.Fatalf("the invalid header was echoed back")
	}
	if !biz.ValidTraceID(got) {
		t.Fatalf("response identity = %q", got)
	}
}

// TestTraceFilterNamesARequestThatNeverReachedAUseCase covers the refusals the
// session layer produces: the identity is set before the middleware runs, so a
// 401 is as traceable as a success.
func TestTraceFilterNamesARequestThatNeverReachedAUseCase(t *testing.T) {
	handler := traceFilter(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		// A refusal written by an inner layer.
		w.WriteHeader(stdhttp.StatusUnauthorized)
	}))
	request := httptest.NewRequest(stdhttp.MethodGet, "/api/admin/v1/audit", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !biz.ValidTraceID(recorder.Header().Get(biz.HeaderTraceID)) {
		t.Fatalf("a refused request carries no identity: %q", recorder.Header().Get(biz.HeaderTraceID))
	}
}

// TestTraceErrorEncoderNamesTheFailedRequest covers the error body: the report
// a user copies from the page names the request, and the reason is unchanged.
func TestTraceErrorEncoderNamesTheFailedRequest(t *testing.T) {
	request := httptest.NewRequest(stdhttp.MethodGet, "/api/admin/v1/audit", nil)
	request = request.WithContext(biz.WithTraceID(request.Context(), "trace-in-error"))
	recorder := httptest.NewRecorder()

	traceErrorEncoder(recorder, request, errors.Forbidden("FORBIDDEN", "当前角色无权执行该操作"))

	if recorder.Code != stdhttp.StatusForbidden {
		t.Fatalf("status = %d", recorder.Code)
	}
	if got := recorder.Header().Get(biz.HeaderTraceID); got != "trace-in-error" {
		t.Fatalf("header = %q", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{`"trace_id":"trace-in-error"`, `"reason":"FORBIDDEN"`, `"code":403`} {
		if !strings.Contains(body, want) {
			t.Fatalf("error body %s does not contain %s", body, want)
		}
	}
}

// TestTraceErrorEncoderLeavesAnUntracedRequestAlone covers a call without an
// identity: the error body is written as it was.
func TestTraceErrorEncoderLeavesAnUntracedRequestAlone(t *testing.T) {
	request := httptest.NewRequest(stdhttp.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	traceErrorEncoder(recorder, request, errors.BadRequest("INVALID", "参数错误"))

	if strings.Contains(recorder.Body.String(), "trace_id") {
		t.Fatalf("an untraced request gained a trace: %s", recorder.Body.String())
	}
}
