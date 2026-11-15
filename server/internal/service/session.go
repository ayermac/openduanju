package service

import (
	"context"
	"net/http"
	"time"

	"openduanju-server/internal/biz"
)

// Actor is the authenticated staff of the current request. It is injected by
// the transport middleware and is the only source of an operator identity:
// a request body can never name the actor.
type Actor struct {
	Staff     *biz.Staff
	SessionID int64
	CSRFToken string
	ExpiresAt time.Time
}

// actorKey is the private context key of the actor.
type actorKey struct{}

// WithActor returns a context that carries the authenticated actor.
func WithActor(ctx context.Context, actor *Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor of ctx, if any.
func ActorFrom(ctx context.Context) *Actor {
	actor, _ := ctx.Value(actorKey{}).(*Actor)
	return actor
}

// RequireActor returns the actor or a forbidden error. Mutating use cases call
// it instead of trusting a client-supplied name.
func RequireActor(ctx context.Context) (*Actor, error) {
	actor := ActorFrom(ctx)
	if actor == nil || actor.Staff == nil {
		return nil, biz.ErrForbidden
	}
	return actor, nil
}

// RequireActorName returns the authenticated username or an error. A write
// use case never records an empty operator.
func RequireActorName(ctx context.Context) (string, error) {
	name := ActorName(ctx)
	if name == "" {
		return "", biz.ErrForbidden
	}
	return name, nil
}

// ActorName is the username recorded in created_by/confirmed_by fields.
func ActorName(ctx context.Context) string {
	if actor := ActorFrom(ctx); actor != nil && actor.Staff != nil {
		return actor.Staff.Username
	}
	return ""
}

// SessionCookieName is the HttpOnly cookie of the admin session.
const SessionCookieName = "dj_admin_session"

// SetCookie records a Set-Cookie header for the response. The transport
// applies it before the body is written.
func SetCookie(ctx context.Context, cookie *http.Cookie) bool {
	holder := StatusHolderFrom(ctx)
	if holder == nil {
		return false
	}
	holder.Cookies = append(holder.Cookies, cookie.String())
	return true
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(ctx context.Context) bool {
	return SetCookie(ctx, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
