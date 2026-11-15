package server

import (
	"context"
	"log/slog"
	stdhttp "net/http"
	"strings"
	"time"

	"openduanju-server/internal/biz"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	grpccore "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Trace is the identity of one operation. It is minted at the request entry -
// or taken from a valid caller identity - carried in the context through
// authentication, permissions, the service, the use cases and the
// repositories, and returned on every response, including the ones that never
// reach a use case.
//
// This is deliberately not a tracing service: no collector is deployed, and an
// operator reads the identity from the response header, the error report, the
// logs and the audit row. An async task keeps it in its own row, so the
// association survives the request and a worker restart.

// traceFilter resolves the identity of an HTTP request before anything else
// runs, so a rejection of the session layer still carries it. The header is set
// on the response before the body is written.
func traceFilter(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		id := biz.TraceFromHeaders(r.Header.Get("Traceparent"), r.Header.Get(biz.HeaderTraceID))
		w.Header().Set(biz.HeaderTraceID, id)
		recorder := &traceResponseWriter{ResponseWriter: w, status: stdhttp.StatusOK}
		ctx := biz.WithTraceID(r.Context(), id)
		started := time.Now()
		next.ServeHTTP(recorder, r.WithContext(ctx))
		slog.InfoContext(ctx, "request",
			"method", r.Method,
			// The signed media endpoints carry their credential in the path.
			// The log keeps the route and drops the value.
			"path", biz.RedactedPath(r.URL.Path),
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

// traceResponseWriter records the status the response was sent with, so the
// request log can show a refusal as well as a success.
type traceResponseWriter struct {
	stdhttp.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader records the status of the response.
func (w *traceResponseWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write sends the body with the recorded status.
func (w *traceResponseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(stdhttp.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// traceErrorEncoder adds the identity of the request to an error response, so
// the report a user copies from the page names the request that failed. The
// message itself is not changed: the reason and the trace are the two things a
// report needs.
func traceErrorEncoder(w stdhttp.ResponseWriter, r *stdhttp.Request, err error) {
	id := biz.TraceID(r.Context())
	if id == "" {
		kratoshttp.DefaultErrorEncoder(w, r, err)
		return
	}
	w.Header().Set(biz.HeaderTraceID, id)
	encoded := errors.Clone(errors.FromError(err))
	if encoded.Metadata == nil {
		encoded.Metadata = make(map[string]string, 1)
	}
	encoded.Metadata["trace_id"] = id
	kratoshttp.DefaultErrorEncoder(w, r, encoded)
}

// TraceMiddleware gives a gRPC call the same identity, header and error
// association as an HTTP call: the two transports cannot disagree about which
// request an error belongs to.
func TraceMiddleware() middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return next(ctx, req)
			}
			header := tr.RequestHeader()
			id := biz.TraceFromHeaders(header.Get("Traceparent"), header.Get(biz.HeaderTraceID))
			ctx = biz.WithTraceID(ctx, id)
			// A trailer carries the identity back to a gRPC caller; it is
			// best effort, because a streaming or already written header
			// cannot be replaced.
			_ = grpccore.SetTrailer(ctx, metadata.Pairs(strings.ToLower(biz.HeaderTraceID), id))
			started := time.Now()
			reply, err := next(ctx, req)
			slog.InfoContext(ctx, "request",
				"kind", tr.Kind().String(),
				"operation", tr.Operation(),
				"duration_ms", time.Since(started).Milliseconds(),
				"failed", err != nil,
			)
			if err == nil {
				return reply, nil
			}
			encoded := errors.Clone(errors.FromError(err))
			if encoded.Metadata == nil {
				encoded.Metadata = make(map[string]string, 1)
			}
			encoded.Metadata["trace_id"] = id
			return reply, encoded
		}
	}
}
