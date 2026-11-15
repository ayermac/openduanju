package server

import (
	"context"
	"crypto/subtle"
	stderrors "errors"
	"log/slog"
	"net/url"
	"strings"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/service"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// SessionAuth authenticates every admin call with the session cookie (HTTP)
// or the x-admin-session metadata (gRPC). It replaces the old shared bearer
// token: there is no path that bypasses a real account.
//
// Mutating calls also require the session CSRF token and, over HTTP, an
// origin that matches the request host. The same check runs for gRPC, so the
// two transports cannot disagree about who may do what.
func SessionAuth(accounts *biz.StaffAccounts) middleware.Middleware {
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok || !isAdminOperation(tr.Operation()) {
				return next(ctx, req)
			}
			if isPublicOperation(tr.Operation()) {
				return next(ctx, req)
			}
			token := sessionToken(tr)
			if token == "" {
				// The rejection is recorded: a missing session has to be
				// distinguishable from one the server no longer accepts.
				logSessionRejection(ctx, tr, "AUTH_REQUIRED")
				return nil, errors.Unauthorized("AUTH_REQUIRED", "需要登录")
			}
			view, err := accounts.Authenticate(ctx, token)
			if err != nil {
				logSessionRejection(ctx, tr, "SESSION_EXPIRED")
				return nil, toSessionError(err)
			}
			// The operation decides whether the call changes something. A
			// client may not downgrade a write by claiming a read.
			mutating := isMutatingOperation(tr.Operation())
			if mutating {
				if !csrfMatches(tr, view.CSRFToken) {
					return nil, errors.Forbidden("CSRF_INVALID", "缺少或错误的CSRF令牌")
				}
				if !originAllowed(tr) {
					return nil, errors.Forbidden("ORIGIN_NOT_ALLOWED", "请求来源不被允许")
				}
			}
			capability, required := capabilityFor(tr.Operation(), mutating)
			if required && !biz.RoleAllows(view.Staff.Role, capability) {
				return nil, errors.Forbidden("FORBIDDEN", "当前角色无权执行该操作")
			}
			actor := &service.Actor{
				Staff:     view.Staff,
				SessionID: view.SessionID,
				CSRFToken: view.CSRFToken,
				ExpiresAt: view.ExpiresAt,
			}
			// The audit actor comes from the authenticated session, never
			// from the request body, so a row cannot name somebody else.
			ctx = biz.WithAuditActor(ctx, biz.Actor{ID: view.Staff.ID, Username: view.Staff.Username})
			return next(service.WithActor(ctx, actor), req)
		}
	}
}

// readOperationPrefixes names the operations that only read. Every other
// operation changes something and needs the CSRF token.
var readOperationPrefixes = []string{"List", "Get", "Read", "Search", "Preview", "Check", "Query", "Suggest", "Describe"}

// isMutatingOperation reports whether the operation changes state. The
// decision comes from the operation the server defines, never from a header
// the client can set.
func isMutatingOperation(operation string) bool {
	name := operation
	if index := strings.LastIndex(name, "/"); index != -1 {
		name = name[index+1:]
	}
	for _, prefix := range readOperationPrefixes {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// isAdminOperation reports whether the operation belongs to the admin surface.
func isAdminOperation(operation string) bool {
	return strings.HasPrefix(operation, "/api/admin/v1") ||
		strings.Contains(operation, "/admin.v1.")
}

// isPublicOperation lists the calls that do not need a session yet.
func isPublicOperation(operation string) bool {
	return strings.HasSuffix(operation, "/auth/login") || strings.Contains(operation, "AdminAuthService/Login")
}

// sessionToken reads the session secret from the request.
func sessionToken(tr transport.Transporter) string {
	if cookie := tr.RequestHeader().Get("Cookie"); cookie != "" {
		for _, part := range strings.Split(cookie, ";") {
			name, value, found := strings.Cut(strings.TrimSpace(part), "=")
			if found && name == service.SessionCookieName {
				return value
			}
		}
	}
	// gRPC clients send the same secret as metadata instead of a cookie.
	return tr.RequestHeader().Get("X-Admin-Session")
}

// csrfMatches compares the header with the session token in constant time.
func csrfMatches(tr transport.Transporter, expected string) bool {
	got := tr.RequestHeader().Get("X-CSRF-Token")
	if got == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// originAllowed requires a same-host origin for a mutating browser request.
// A gRPC call has no Origin header and is not a browser request.
func originAllowed(tr transport.Transporter) bool {
	origin := tr.RequestHeader().Get("Origin")
	if origin == "" {
		// A non-browser client (gRPC, script) is covered by the CSRF secret.
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := tr.RequestHeader().Get("X-Admin-Host")
	if host == "" {
		host = tr.RequestHeader().Get("Host")
	}
	if host == "" {
		host = tr.RequestHeader().Get(":authority")
	}
	return strings.EqualFold(parsed.Host, host)
}

// capabilityFor maps one admin operation to the capability it needs. The
// mapping is the single source of truth for both transports.
func capabilityFor(operation string, mutating bool) (biz.Capability, bool) {
	path := strings.TrimPrefix(operation, "/api/admin/v1")
	// The Kratos operation names the RPC when the request is not generated
	// from the HTTP path (gRPC), so the service name decides the surface.
	switch {
	case strings.Contains(operation, "PublishRevision") || strings.Contains(operation, "UnpublishSeries"):
		// Publishing and unpublishing have their own capability. They are
		// checked before the broad service rules, so a reviewer cannot
		// publish and an operator is not refused.
		return biz.CapPublish, true
	case strings.Contains(path, "/publications"):
		if mutating {
			return biz.CapPublish, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminContentService/") || strings.HasPrefix(path, "/series"):
		if mutating {
			return biz.CapContentWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminCandidateService/") || strings.HasPrefix(path, "/candidates"):
		if mutating {
			return biz.CapContentWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminAuditService/") || strings.HasPrefix(path, "/audit"):
		// The audit list holds identities and actions, never a payload; every
		// signed-in role may read it.
		return biz.CapRead, true
	case strings.Contains(operation, "AdminSourceService/") || strings.HasPrefix(path, "/sources") ||
		strings.HasPrefix(path, "/source-channels"):
		// The source configuration drives collection: only the roles that may
		// write ingestion may change it, and a reviewer keeps read access.
		if mutating {
			return biz.CapIngestionWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminIngestionService/") || strings.HasPrefix(path, "/ingestion"):
		if mutating {
			return biz.CapIngestionWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminMediaService/") || strings.Contains(operation, "AdminUploadService/") ||
		strings.HasPrefix(path, "/media-batches") || strings.HasPrefix(path, "/uploads") || strings.HasPrefix(path, "/media-assets"):
		if mutating {
			return biz.CapMediaWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminReviewService/") || strings.HasPrefix(path, "/reviews"):
		if mutating {
			return biz.CapReview, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminAuthorizationService/") || strings.Contains(path, "/authorization-grants"):
		if mutating {
			return biz.CapAuthorizationWrite, true
		}
		return biz.CapRead, true
	}
	switch {
	case strings.HasPrefix(path, "/auth/") || strings.Contains(operation, "AdminAuthService/"):
		return "", false
	case strings.HasPrefix(path, "/staff") || strings.Contains(operation, "AdminStaffService/"):
		return biz.CapStaffManage, true
	case strings.Contains(path, "/authorization-grants"):
		if mutating {
			return biz.CapAuthorizationWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(path, "/publications"):
		// Publishing and unpublishing are their own capability: a reviewer
		// may review, but must not put anything live.
		if mutating {
			return biz.CapPublish, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/reviews"):
		// Reading the worklist is open to every back-office role; claiming
		// and deciding need the review capability.
		if mutating {
			return biz.CapReview, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/runtime"):
		return biz.CapRead, true
	case strings.HasPrefix(path, "/uploads"):
		if mutating {
			return biz.CapMediaWrite, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/media-batches"):
		if mutating {
			return biz.CapMediaWrite, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/media-assets"):
		if mutating {
			return biz.CapMediaWrite, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/sources"), strings.HasPrefix(path, "/source-channels"):
		if mutating {
			return biz.CapIngestionWrite, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/ingestion"):
		if mutating {
			return biz.CapIngestionWrite, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/candidates"):
		if mutating {
			return biz.CapContentWrite, true
		}
		return biz.CapRead, true
	case strings.HasPrefix(path, "/series"), strings.HasPrefix(path, "/revisions"):
		if mutating {
			return biz.CapContentWrite, true
		}
		return biz.CapRead, true
	}
	// gRPC operations carry the service name, not the HTTP path.
	switch {
	case strings.Contains(operation, "AdminStaffService/"):
		return biz.CapStaffManage, true
	case strings.Contains(operation, "AdminAuthorizationService/"):
		if mutating {
			return biz.CapAuthorizationWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminReviewService/ListReviews"),
		strings.Contains(operation, "AdminReviewService/GetReview"):
		return biz.CapRead, true
	case strings.Contains(operation, "AdminReviewService/PublishRevision"),
		strings.Contains(operation, "AdminReviewService/UnpublishSeries"):
		return biz.CapPublish, true
	case strings.Contains(operation, "AdminReviewService/"):
		return biz.CapReview, true
	case strings.Contains(operation, "AdminUploadService/"), strings.Contains(operation, "AdminMediaService/"):
		if mutating {
			return biz.CapMediaWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminIngestionService/"):
		if mutating {
			return biz.CapIngestionWrite, true
		}
		return biz.CapRead, true
	case strings.Contains(operation, "AdminContentService/"), strings.Contains(operation, "AdminCandidateService/"):
		if mutating {
			return biz.CapContentWrite, true
		}
		return biz.CapRead, true
	}
	if mutating {
		return biz.CapContentWrite, true
	}
	return biz.CapRead, true
}

// toSessionError maps an authentication failure to the API error.
func toSessionError(err error) error {
	if stderrors.Is(err, biz.ErrAccountDisabled) {
		return errors.Forbidden("ACCOUNT_DISABLED", "账号已停用")
	}
	return errors.Unauthorized("SESSION_EXPIRED", "会话已失效，请重新登录")
}

// logSessionRejection records why a request was refused. It names the
// operation, the reason and the trace identity, and never the token itself.
func logSessionRejection(ctx context.Context, tr transport.Transporter, reason string) {
	slog.ErrorContext(ctx, "session refused",
		"reason", reason,
		"operation", tr.Operation(),
		"kind", tr.Kind(),
	)
}
