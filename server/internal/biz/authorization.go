package biz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Authorization grant states.
const (
	GrantActive  = "active"
	GrantRevoked = "revoked"
)

// Authorization permissions are separate rights. A grant that allows
// processing does not imply that the result may be shown or played.
type Permission string

// Permission bits.
const (
	PermissionProcess  Permission = "process"
	PermissionDisplay  Permission = "display"
	PermissionPlayback Permission = "playback"
)

// AuthorizationGrant is one series-level authorization record. It is
// immutable: a corrected permission, scope or validity window is a new grant,
// and an existing one can only be revoked.
type AuthorizationGrant struct {
	ID            int64
	SeriesID      int64
	Reference     string
	AllowProcess  bool
	AllowDisplay  bool
	AllowPlayback bool
	// ValidFrom and ValidUntil are UTC. A nil ValidUntil means the system has
	// no end date; it is not a promise of a perpetual license.
	ValidFrom    time.Time
	ValidUntil   *time.Time
	Status       string
	Version      int
	ConfirmedBy  string
	ConfirmedAt  time.Time
	RevokedBy    string
	RevokedAt    *time.Time
	RevokeReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ValidAt reports whether the grant is in force at now: active, started and
// not ended. Every gate calls it with the current time; nothing may cache a
// previous answer.
func (g *AuthorizationGrant) ValidAt(now time.Time) bool {
	if g == nil || g.Status != GrantActive {
		return false
	}
	now = now.UTC()
	if now.Before(g.ValidFrom.UTC()) {
		return false
	}
	if g.ValidUntil != nil && !now.Before(g.ValidUntil.UTC()) {
		return false
	}
	return true
}

// Allows reports whether one permission is granted and the grant is in force.
func (g *AuthorizationGrant) Allows(permission Permission, now time.Time) bool {
	if !g.ValidAt(now) {
		return false
	}
	switch permission {
	case PermissionProcess:
		return g.AllowProcess
	case PermissionDisplay:
		return g.AllowDisplay
	case PermissionPlayback:
		return g.AllowPlayback
	}
	return false
}

// Fingerprint is the immutable snapshot a revision freezes. It covers the
// permission bits, the reference and the validity window, so a changed row
// would no longer match the frozen revision.
func (g *AuthorizationGrant) Fingerprint() string {
	if g == nil {
		return ""
	}
	until := "none"
	if g.ValidUntil != nil {
		until = g.ValidUntil.UTC().Format(time.RFC3339Nano)
	}
	parts := []string{
		fmt.Sprintf("series=%d", g.SeriesID),
		"reference=" + g.Reference,
		fmt.Sprintf("process=%t", g.AllowProcess),
		fmt.Sprintf("display=%t", g.AllowDisplay),
		fmt.Sprintf("playback=%t", g.AllowPlayback),
		"from=" + g.ValidFrom.UTC().Format(time.RFC3339Nano),
		"until=" + until,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%x", sum[:])
}

// GrantQuery filters authorization grants.
type GrantQuery struct {
	SeriesID int64
	Status   string
	Page     int
	PageSize int
}

// CreateGrantRequest asks for a new grant. Permission bits default to false:
// an omitted bit never grants a right.
type CreateGrantRequest struct {
	SeriesID      int64
	Reference     string
	AllowProcess  bool
	AllowDisplay  bool
	AllowPlayback bool
	ValidFrom     time.Time
	ValidUntil    *time.Time
	Actor         string
}

// Grant failures.
var (
	ErrGrantNotFound      = errors.New("authorization grant not found")
	ErrGrantVersion       = errors.New("authorization grant version conflict")
	ErrGrantNotApplicable = errors.New("authorization grant does not cover the series")
	ErrGrantNotEffective  = errors.New("authorization grant is not in force")
	ErrGrantPermission    = errors.New("authorization grant does not allow this action")
	ErrGrantInvalid       = errors.New("authorization grant is invalid")
)

// AuthorizationRepo persists grants.
type AuthorizationRepo interface {
	CreateGrant(ctx context.Context, request CreateGrantRequest) (*AuthorizationGrant, error)
	GetGrant(ctx context.Context, id int64) (*AuthorizationGrant, error)
	ListGrants(ctx context.Context, query GrantQuery) ([]AuthorizationGrant, int, error)
	// RevokeGrant applies a version CAS. The same version revoking an already
	// revoked grant returns the stored row; a different version conflicts.
	RevokeGrant(ctx context.Context, id int64, expectedVersion int, actor string, reason string) (*AuthorizationGrant, error)
}

// Authorizations is the use case over series-level grants.
type Authorizations struct {
	repo  AuthorizationRepo
	audit *Auditor
}

// NewAuthorizations builds the authorization use case. The auditor may be nil
// in a unit test that checks business rules only.
func NewAuthorizations(repo AuthorizationRepo, auditor *Auditor) *Authorizations {
	return &Authorizations{repo: repo, audit: auditor}
}

// Create adds one grant. A corrected grant is a new row; nothing is updated
// in place here on purpose.
func (a *Authorizations) Create(ctx context.Context, req CreateGrantRequest) (grant *AuthorizationGrant, err error) {
	ctx, scope := a.audit.Begin(ctx, AuditActionGrantCreate, AuditResourceGrant)
	defer func() { scope.End(ctx, err) }()
	req.Reference = strings.TrimSpace(req.Reference)
	if req.SeriesID <= 0 {
		return nil, fmt.Errorf("%w: series is required", ErrGrantInvalid)
	}
	if req.Reference == "" {
		return nil, fmt.Errorf("%w: reference is required", ErrGrantInvalid)
	}
	if req.ValidFrom.IsZero() {
		return nil, fmt.Errorf("%w: valid_from is required", ErrGrantInvalid)
	}
	req.ValidFrom = req.ValidFrom.UTC()
	if req.ValidUntil != nil {
		until := req.ValidUntil.UTC()
		req.ValidUntil = &until
		if !until.After(req.ValidFrom) {
			return nil, fmt.Errorf("%w: valid_until must be after valid_from", ErrGrantInvalid)
		}
	}
	if strings.TrimSpace(req.Actor) == "" {
		return nil, fmt.Errorf("%w: confirmed_by is required", ErrGrantInvalid)
	}
	return a.repo.CreateGrant(ctx, req)
}

// Revoke marks one grant revoked with a version CAS.
func (a *Authorizations) Revoke(ctx context.Context, id int64, expectedVersion int, actor string, reason string) (grant *AuthorizationGrant, err error) {
	ctx, scope := a.audit.Begin(ctx, AuditActionGrantRevoke, AuditResourceGrant)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	if expectedVersion <= 0 {
		return nil, ErrGrantVersion
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: revoke reason is required", ErrGrantInvalid)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: revoked_by is required", ErrGrantInvalid)
	}
	return a.repo.RevokeGrant(ctx, id, expectedVersion, actor, reason)
}

// Get returns one grant.
func (a *Authorizations) Get(ctx context.Context, id int64) (*AuthorizationGrant, error) {
	return a.repo.GetGrant(ctx, id)
}

// List returns the grants matching the query.
func (a *Authorizations) List(ctx context.Context, query GrantQuery) ([]AuthorizationGrant, int, error) {
	return a.repo.ListGrants(ctx, query)
}

// EnsureForSubmit is the submission gate: the grant must cover the series,
// be the expected version, be in force and allow processing.
func EnsureForSubmit(grant *AuthorizationGrant, seriesID int64, expectedVersion int, now time.Time) error {
	return ensure(grant, seriesID, expectedVersion, now, PermissionProcess)
}

// EnsureForReview is the review gate: publishing rights are checked before a
// review task may be opened.
func EnsureForReview(grant *AuthorizationGrant, seriesID int64, now time.Time) error {
	if err := ensure(grant, seriesID, 0, now, PermissionDisplay); err != nil {
		return err
	}
	if !grant.Allows(PermissionPlayback, now) {
		return fmt.Errorf("%w: playback", ErrGrantPermission)
	}
	return nil
}

// EnsureForPublish is the publication gate. The publish API is not
// implemented yet; the rule lives here so the later slice cannot forget it.
func EnsureForPublish(grant *AuthorizationGrant, seriesID int64, now time.Time) error {
	return EnsureForReview(grant, seriesID, now)
}

// EnsureForDisplay guards public metadata and covers.
func EnsureForDisplay(grant *AuthorizationGrant, seriesID int64, now time.Time) error {
	return ensure(grant, seriesID, 0, now, PermissionDisplay)
}

// EnsureForPlayback guards a new playback session or a refreshed credential.
func EnsureForPlayback(grant *AuthorizationGrant, seriesID int64, now time.Time) error {
	return ensure(grant, seriesID, 0, now, PermissionPlayback)
}

// ensure applies the common checks: presence, series scope, expected version,
// current validity and one permission.
func ensure(grant *AuthorizationGrant, seriesID int64, expectedVersion int, now time.Time, permission Permission) error {
	if grant == nil {
		return ErrGrantNotFound
	}
	if seriesID > 0 && grant.SeriesID != seriesID {
		return fmt.Errorf("%w: grant series %d, expected %d", ErrGrantNotApplicable, grant.SeriesID, seriesID)
	}
	if expectedVersion > 0 && grant.Version != expectedVersion {
		return fmt.Errorf("%w: grant version %d, expected %d", ErrGrantVersion, grant.Version, expectedVersion)
	}
	if !grant.ValidAt(now) {
		return fmt.Errorf("%w: status %s", ErrGrantNotEffective, grant.Status)
	}
	if !grant.Allows(permission, now) {
		return fmt.Errorf("%w: %s", ErrGrantPermission, permission)
	}
	return nil
}
