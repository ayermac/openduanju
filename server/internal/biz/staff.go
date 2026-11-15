package biz

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Back-office roles. The boundaries are fixed: an operator cannot review, a
// reviewer cannot publish or change an authorization, and an administrator is
// still forbidden from reviewing their own submission.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleReviewer = "reviewer"
)

// Capability is one coarse right a role may hold. The transport maps an
// operation to a capability; the same mapping serves HTTP and gRPC.
type Capability string

// Capabilities.
const (
	CapRead               Capability = "read"
	CapStaffManage        Capability = "staff_manage"
	CapAuthorizationWrite Capability = "authorization_write"
	CapContentWrite       Capability = "content_write"
	CapIngestionWrite     Capability = "ingestion_write"
	CapMediaWrite         Capability = "media_write"
	CapReview             Capability = "review"
	CapPublish            Capability = "publish"
)

// roleCapabilities is the fixed permission matrix.
var roleCapabilities = map[string]map[Capability]bool{
	RoleAdmin: {
		CapRead: true, CapStaffManage: true, CapAuthorizationWrite: true,
		CapContentWrite: true, CapIngestionWrite: true, CapMediaWrite: true,
		CapReview: true, CapPublish: true,
	},
	RoleOperator: {
		CapRead: true, CapContentWrite: true, CapIngestionWrite: true,
		CapMediaWrite: true, CapPublish: true,
	},
	RoleReviewer: {
		CapRead: true, CapReview: true,
	},
}

// RoleAllows reports whether a role holds a capability. An unknown role holds
// nothing.
func RoleAllows(role string, capability Capability) bool {
	return roleCapabilities[role][capability]
}

// KnownRole reports whether the value names a role.
func KnownRole(role string) bool {
	_, ok := roleCapabilities[role]
	return ok
}

// Permissions lists the capabilities of a role for the session view.
func Permissions(role string) []string {
	order := []Capability{
		CapRead, CapStaffManage, CapAuthorizationWrite, CapContentWrite,
		CapIngestionWrite, CapMediaWrite, CapReview, CapPublish,
	}
	out := make([]string, 0, len(order))
	for _, capability := range order {
		if RoleAllows(role, capability) {
			out = append(out, string(capability))
		}
	}
	return out
}

// Staff is one back-office account.
type Staff struct {
	ID              int64
	Username        string
	DisplayName     string
	Role            string
	Enabled         bool
	Version         int
	PasswordVersion int
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// PasswordHash is only used by the login check; the API never returns it.
	PasswordHash string
}

// SessionView is an authenticated session with its staff row.
type SessionView struct {
	SessionID int64
	Staff     *Staff
	CSRFToken string
	ExpiresAt time.Time
	// Token is the cookie value; it is returned once, at login.
	Token string
}

// CreateStaffRequest asks for one account.
type CreateStaffRequest struct {
	Username    string
	DisplayName string
	Role        string
	Password    string
	Actor       string
}

// UpdateStaffRequest changes the mutable fields of an account.
type UpdateStaffRequest struct {
	ID              int64
	ExpectedVersion int
	DisplayName     *string
	Role            *string
	Enabled         *bool
	Actor           string
}

// StaffQuery filters accounts.
type StaffQuery struct {
	Page     int
	PageSize int
	Role     string
	Enabled  *bool
	Q        string
}

// SessionRecord is the persisted session the repository returns.
type SessionRecord struct {
	ID        int64
	StaffID   int64
	CSRFToken string
	ExpiresAt time.Time
	RevokedAt *time.Time
	Staff     *Staff
}

// StaffRepo persists accounts and sessions.
type StaffRepo interface {
	CreateStaff(ctx context.Context, request CreateStaffRequest, passwordHash string) (*Staff, error)
	GetStaff(ctx context.Context, id int64) (*Staff, error)
	GetStaffByUsername(ctx context.Context, username string) (*Staff, error)
	ListStaff(ctx context.Context, query StaffQuery) ([]Staff, int, error)
	UpdateStaff(ctx context.Context, request UpdateStaffRequest) (*Staff, error)
	ResetPassword(ctx context.Context, id int64, expectedVersion int, passwordHash string) (*Staff, error)
	CreateSession(ctx context.Context, staffID int64, tokenHash string, csrfToken string, expiresAt time.Time) (*SessionRecord, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*SessionRecord, error)
	RevokeSession(ctx context.Context, sessionID int64) error
	RevokeStaffSessions(ctx context.Context, staffID int64) (int, error)
	CountEnabledAdmins(ctx context.Context) (int, error)
	// CountRecentFailures counts failed logins for one username since a point
	// in time. It drives the sliding-window throttle.
	CountRecentFailures(ctx context.Context, username string, since time.Time) (int, error)
	RecordLoginAttempt(ctx context.Context, username string, succeeded bool, at time.Time) error
}

// Account failures.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountDisabled    = errors.New("account is disabled")
	ErrSessionInvalid     = errors.New("session is invalid or expired")
	ErrStaffNotFound      = errors.New("staff not found")
	ErrStaffVersion       = errors.New("staff version conflict")
	ErrUsernameExists     = errors.New("username already exists")
	ErrLastAdmin          = errors.New("the last enabled administrator cannot be removed")
	ErrStaffInvalid       = errors.New("staff fields are invalid")
	ErrForbidden          = errors.New("forbidden")
	ErrAlreadyInitialised = errors.New("an administrator already exists")
	ErrTooManyAttempts    = errors.New("too many failed login attempts")
)

const (
	passwordIterations = 210_000
	sessionTTL         = 12 * time.Hour
	// loginWindow and loginMaxFailures define the sliding throttle. The
	// window is what recovers an account: there is no permanent lock flag,
	// so an administrator cannot be locked out for good.
	loginWindow      = 15 * time.Minute
	loginMaxFailures = 5
)

// HashPassword derives a PBKDF2-SHA256 hash with a random salt. The encoded
// form carries the parameters, so a later change does not invalidate rows.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares a password against the encoded hash in constant
// time. A malformed hash never authenticates.
func VerifyPassword(encoded string, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	var iterations int
	if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidatePassword rejects short or blank secrets. There is no default
// password anywhere in the product.
func ValidatePassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("%w: password is required", ErrStaffInvalid)
	}
	if len(password) < 12 {
		return fmt.Errorf("%w: password must be at least 12 characters", ErrStaffInvalid)
	}
	return nil
}

// NewSessionToken returns the cookie value and the hash stored in the
// database. The raw token is never persisted.
func NewSessionToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, HashSessionToken(token), nil
}

// HashSessionToken hashes a cookie value.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCSRFToken returns a per-session CSRF token.
func NewCSRFToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// StaffAccounts is the use case over accounts and sessions.
type StaffAccounts struct {
	repo  StaffRepo
	audit *Auditor
}

// NewStaffAccounts builds the use case. The auditor may be nil in a unit test
// that checks business rules only.
func NewStaffAccounts(repo StaffRepo, auditor *Auditor) *StaffAccounts {
	return &StaffAccounts{repo: repo, audit: auditor}
}

// Login verifies a password and opens one session. A disabled account and a
// wrong password answer the same error, so the API does not enumerate users.
func (a *StaffAccounts) Login(ctx context.Context, username string, password string) (*SessionView, error) {
	username = strings.TrimSpace(username)
	now := time.Now().UTC()
	// The throttle counts every username, including unknown ones, so the
	// response cannot be used to enumerate accounts.
	failures, err := a.repo.CountRecentFailures(ctx, username, now.Add(-loginWindow))
	if err != nil {
		return nil, err
	}
	if failures >= loginMaxFailures {
		return nil, fmt.Errorf("%w: try again in %s", ErrTooManyAttempts, loginWindow)
	}
	staff, err := a.repo.GetStaffByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrStaffNotFound) {
			_ = a.repo.RecordLoginAttempt(ctx, username, false, now)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !staff.Enabled {
		_ = a.repo.RecordLoginAttempt(ctx, username, false, now)
		return nil, ErrAccountDisabled
	}
	if !VerifyPassword(staff.PasswordHash, password) {
		_ = a.repo.RecordLoginAttempt(ctx, username, false, now)
		return nil, ErrInvalidCredentials
	}
	if err := a.repo.RecordLoginAttempt(ctx, username, true, now); err != nil {
		return nil, err
	}
	token, hash, err := NewSessionToken()
	if err != nil {
		return nil, err
	}
	csrf := NewCSRFToken()
	expiresAt := time.Now().UTC().Add(sessionTTL)
	record, err := a.repo.CreateSession(ctx, staff.ID, hash, csrf, expiresAt)
	if err != nil {
		return nil, err
	}
	return &SessionView{SessionID: record.ID, Staff: staff, CSRFToken: csrf, ExpiresAt: expiresAt, Token: token}, nil
}

// Authenticate resolves a cookie value into a live session. An expired,
// revoked or disabled account never authenticates.
func (a *StaffAccounts) Authenticate(ctx context.Context, token string) (*SessionView, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrSessionInvalid
	}
	record, err := a.repo.GetSessionByTokenHash(ctx, HashSessionToken(token))
	if err != nil {
		if errors.Is(err, ErrSessionInvalid) || errors.Is(err, ErrStaffNotFound) {
			return nil, ErrSessionInvalid
		}
		return nil, err
	}
	now := time.Now().UTC()
	if record.RevokedAt != nil || !now.Before(record.ExpiresAt) {
		return nil, ErrSessionInvalid
	}
	if record.Staff == nil || !record.Staff.Enabled {
		return nil, ErrAccountDisabled
	}
	return &SessionView{
		SessionID: record.ID,
		Staff:     record.Staff,
		CSRFToken: record.CSRFToken,
		ExpiresAt: record.ExpiresAt,
	}, nil
}

// Logout revokes the current session.
func (a *StaffAccounts) Logout(ctx context.Context, sessionID int64) error {
	return a.repo.RevokeSession(ctx, sessionID)
}

// CreateStaff adds one account and hashes its password. The account and the
// record of who created it are committed together; the password itself is
// never part of either.
func (a *StaffAccounts) CreateStaff(ctx context.Context, request CreateStaffRequest) (staff *Staff, err error) {
	ctx, scope := a.audit.Begin(ctx, AuditActionStaffCreate, AuditResourceStaff)
	defer func() { scope.End(ctx, err) }()
	request.Username = strings.TrimSpace(request.Username)
	if request.Username == "" {
		return nil, fmt.Errorf("%w: username is required", ErrStaffInvalid)
	}
	if !KnownRole(request.Role) {
		return nil, fmt.Errorf("%w: unknown role %q", ErrStaffInvalid, request.Role)
	}
	hash, err := HashPassword(request.Password)
	if err != nil {
		return nil, err
	}
	return a.repo.CreateStaff(ctx, request, hash)
}

// UpdateStaff changes display name, role or enabled state. Disabling an
// account revokes its sessions in the same transaction.
func (a *StaffAccounts) UpdateStaff(ctx context.Context, request UpdateStaffRequest) (staff *Staff, err error) {
	ctx, scope := a.audit.Begin(ctx, AuditActionStaffUpdate, AuditResourceStaff)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, request.ID, 0)
	if request.ExpectedVersion <= 0 {
		return nil, ErrStaffVersion
	}
	if request.Role != nil && !KnownRole(*request.Role) {
		return nil, fmt.Errorf("%w: unknown role", ErrStaffInvalid)
	}
	return a.repo.UpdateStaff(ctx, request)
}

// ResetPassword sets a new password and revokes the old sessions. The event
// records that the password was reset, never the password.
func (a *StaffAccounts) ResetPassword(ctx context.Context, id int64, expectedVersion int, password string) (staff *Staff, err error) {
	ctx, scope := a.audit.Begin(ctx, AuditActionStaffResetSecret, AuditResourceStaff)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	if expectedVersion <= 0 {
		return nil, ErrStaffVersion
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	return a.repo.ResetPassword(ctx, id, expectedVersion, hash)
}

// GetStaff returns one account.
func (a *StaffAccounts) GetStaff(ctx context.Context, id int64) (*Staff, error) {
	return a.repo.GetStaff(ctx, id)
}

// ListStaff returns a page of accounts.
func (a *StaffAccounts) ListStaff(ctx context.Context, query StaffQuery) ([]Staff, int, error) {
	return a.repo.ListStaff(ctx, query)
}

// EnsureBootstrap creates the first administrator. It refuses to run when an
// enabled administrator already exists, so the command cannot reset a live
// installation.
func (a *StaffAccounts) EnsureBootstrap(ctx context.Context, username string, displayName string, password string) (*Staff, error) {
	count, err := a.repo.CountEnabledAdmins(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrAlreadyInitialised
	}
	return a.CreateStaff(ctx, CreateStaffRequest{
		Username:    username,
		DisplayName: displayName,
		Role:        RoleAdmin,
		Password:    password,
		Actor:       "bootstrap",
	})
}
