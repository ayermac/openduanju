package main

import (
	"context"
	"log/slog"

	"openduanju-server/internal/biz"
)

// traceAttrs puts the identity of the current operation on every log line.
//
// The logger extracts attributes from the context of each call, so a use case,
// a repository or a worker that logs with its context does not have to repeat
// the identity. The value is the same one the response header and the audit row
// carry; without it the field would come from a span of an exporter this
// deployment does not run, and would be empty.
func traceAttrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	id := biz.TraceID(ctx)
	if id == "" {
		return nil
	}
	return []slog.Attr{slog.String("trace_id", id)}
}
