package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

// Trace identity of one operation.
//
// The identity is minted at the request entry, carried in the context through
// authentication, permissions, the service, the use case and the repositories,
// and returned to the caller as X-Trace-Id. An async task keeps it in its own
// row, so a task that outlives the request - and a task that is resumed by a
// worker that never saw the request - can be followed back to its trigger.
//
// There is no separate tracing service here: the identity is a string that the
// logs and the audit rows share, and it works with the export switched off.

// traceIDPattern is the shape of an identity this server accepts from a
// caller. It is deliberately narrow: an identity ends up in logs, headers and
// audit rows, so a caller may not inject arbitrary text.
var traceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// traceparentPattern is the W3C trace context header. Only a valid one is
// followed; anything else is replaced by a locally minted identity.
var traceparentPattern = regexp.MustCompile(`^([0-9a-f]{2})-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)

// HeaderTraceID is the response and request header that carries the identity.
const HeaderTraceID = "X-Trace-Id"

// The signed media endpoints carry their credential in the path instead of a
// header, because a browser fetches them directly (<video src>, a PUT from the
// upload form). They are named here, next to the redaction that uses them, so
// the local driver and the request log cannot drift apart: a new endpoint either
// reuses one of these prefixes or has to be added here.
const (
	StagingPathPrefix  = "/api/admin/v1/uploads/staging/"
	PreviewPathPrefix  = "/api/admin/v1/media-preview/"
	PlaybackPathPrefix = "/api/web/v1/media/"
)

// SignedPathPrefixes lists the endpoints whose last segment is a credential.
var SignedPathPrefixes = []string{StagingPathPrefix, PreviewPathPrefix, PlaybackPathPrefix}

// opaqueSegmentLength is the shortest segment treated as a credential when a
// path is not one of the declared prefixes. Route segments of this API are
// lower-case words and decimal identifiers; a long segment in another alphabet
// is a signed value, and a log line must not carry it even if a future endpoint
// forgets to declare its prefix.
const opaqueSegmentLength = 24

// routeSegmentPattern is what an ordinary route segment looks like: lower-case
// letters, digits and dashes. A signed value mixes case and URL-safe base64
// punctuation, so it does not match.
var routeSegmentPattern = regexp.MustCompile(`^[a-z0-9-]*$`)

// RedactedPath returns a request path that is safe to log: the credential of a
// signed endpoint is replaced by a placeholder, and so is any long segment that
// is not shaped like a route. Everything else - the route, the identifiers an
// operator searches for - is kept, so the line still says which request it was.
func RedactedPath(path string) string {
	for _, prefix := range SignedPathPrefixes {
		if remainder, ok := strings.CutPrefix(path, prefix); ok {
			if remainder == "" {
				return path
			}
			return prefix + "{token}"
		}
	}
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if len(segment) >= opaqueSegmentLength && !routeSegmentPattern.MatchString(segment) {
			segments[index] = "{token}"
		}
	}
	return strings.Join(segments, "/")
}

// traceIDKey carries the request identity in the context.
type traceIDKey struct{}

// WithTraceID returns a context carrying the trace identity.
func WithTraceID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey{}, id)
}

// TraceID returns the trace identity of the current request, if any.
func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceIDKey{}).(string)
	return id
}

// NewTraceID mints an identity for one operation.
func NewTraceID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		// A trace identity is a diagnostic aid; a failed read must not fail
		// the operation it describes.
		return ""
	}
	return hex.EncodeToString(buffer)
}

// TraceFromHeaders resolves the identity of a request. A valid W3C trace
// context wins, because that is the standard propagation a caller may already
// implement; otherwise a valid X-Trace-Id is reused, so a caller can correlate
// its own report with the server. Anything malformed is replaced by a fresh
// identity instead of being echoed back as if it were trusted.
func TraceFromHeaders(traceParent, header string) string {
	if id := traceFromTraceParent(traceParent); id != "" {
		return id
	}
	if ValidTraceID(header) {
		return header
	}
	return NewTraceID()
}

// ValidTraceID reports whether id may be used as a trace identity.
func ValidTraceID(id string) bool {
	return traceIDPattern.MatchString(strings.TrimSpace(id))
}

// traceFromTraceParent extracts the trace id of a W3C traceparent value.
func traceFromTraceParent(value string) string {
	match := traceparentPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(value)))
	if match == nil {
		return ""
	}
	// An all-zero trace id is invalid per the specification.
	if strings.Trim(match[2], "0") == "" {
		return ""
	}
	return match[2]
}
