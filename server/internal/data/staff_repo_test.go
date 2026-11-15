package data_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/staffsession"
)

func newAccounts(t *testing.T) (*ent.Client, *data.StaffRepo, *biz.StaffAccounts) {
	t.Helper()
	client := openMediaClient(t)
	repo := data.NewStaffRepoWithDB(client)
	return client, repo, biz.NewStaffAccounts(repo, nil)
}

func TestStaffCreateAndLogin(t *testing.T) {
	_, repo, accounts := newAccounts(t)
	ctx := context.Background()

	staff, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "alice", DisplayName: "Alice", Role: biz.RoleAdmin, Password: "correct horse battery", Actor: "bootstrap",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if !staff.Enabled || staff.Role != biz.RoleAdmin || staff.Version != 1 {
		t.Fatalf("staff: %+v", staff)
	}
	// The stored hash is not the password and verifies against it.
	stored, err := repo.GetStaffByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get staff: %v", err)
	}
	if stored.PasswordHash == "correct horse battery" || !biz.VerifyPassword(stored.PasswordHash, "correct horse battery") {
		t.Fatal("the password hash is wrong")
	}
	if biz.VerifyPassword(stored.PasswordHash, "wrong password") {
		t.Fatal("a wrong password verified")
	}
	// A duplicate username is a conflict.
	if _, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "alice", Role: biz.RoleOperator, Password: "another long password",
	}); !errors.Is(err, biz.ErrUsernameExists) {
		t.Fatalf("duplicate username: %v", err)
	}
	// A short password is rejected before any row is written.
	if _, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "bob", Role: biz.RoleOperator, Password: "short",
	}); !errors.Is(err, biz.ErrStaffInvalid) {
		t.Fatalf("short password: %v", err)
	}

	view, err := accounts.Login(ctx, "alice", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if view.Token == "" || view.CSRFToken == "" || view.SessionID == 0 {
		t.Fatalf("session: %+v", view)
	}
	// Only the token hash is stored.
	row, err := repo.GetSessionByTokenHash(ctx, biz.HashSessionToken(view.Token))
	if err != nil {
		t.Fatalf("find session: %v", err)
	}
	if row.Staff == nil || row.Staff.Username != "alice" {
		t.Fatalf("session staff: %+v", row.Staff)
	}
	// The token still authenticates and resolves the same staff.
	authenticated, err := accounts.Authenticate(ctx, view.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if authenticated.Staff.ID != staff.ID || authenticated.CSRFToken != view.CSRFToken {
		t.Fatalf("authenticated: %+v", authenticated)
	}
}

func TestStaffLoginRejectsBadCredentialsAndDisabledAccounts(t *testing.T) {
	_, _, accounts := newAccounts(t)
	ctx := context.Background()
	staff, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "carol", Role: biz.RoleOperator, Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := accounts.Login(ctx, "carol", "wrong password"); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := accounts.Login(ctx, "nobody", "correct horse battery"); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	// A disabled account cannot log in.
	disabled := false
	if _, err := accounts.UpdateStaff(ctx, biz.UpdateStaffRequest{
		ID: staff.ID, ExpectedVersion: staff.Version, Enabled: &disabled,
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := accounts.Login(ctx, "carol", "correct horse battery"); !errors.Is(err, biz.ErrAccountDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
}

func TestStaffDisableRevokesLiveSessions(t *testing.T) {
	_, _, accounts := newAccounts(t)
	ctx := context.Background()
	staff, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "dave", Role: biz.RoleOperator, Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := accounts.Login(ctx, "dave", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := accounts.Authenticate(ctx, view.Token); err != nil {
		t.Fatalf("authenticate before disable: %v", err)
	}
	disabled := false
	if _, err := accounts.UpdateStaff(ctx, biz.UpdateStaffRequest{
		ID: staff.ID, ExpectedVersion: staff.Version, Enabled: &disabled,
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := accounts.Authenticate(ctx, view.Token); !errors.Is(err, biz.ErrSessionInvalid) && !errors.Is(err, biz.ErrAccountDisabled) {
		t.Fatalf("session after disable: %v", err)
	}
}

func TestStaffResetPasswordRevokesSessions(t *testing.T) {
	_, repo, accounts := newAccounts(t)
	ctx := context.Background()
	staff, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "erin", Role: biz.RoleAdmin, Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := accounts.Login(ctx, "erin", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	updated, err := accounts.ResetPassword(ctx, staff.ID, staff.Version, "a brand new passphrase")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if updated.PasswordVersion != staff.PasswordVersion+1 || updated.Version != staff.Version+1 {
		t.Fatalf("versions after reset: %+v", updated)
	}
	if _, err := accounts.Authenticate(ctx, view.Token); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("session after reset: %v", err)
	}
	// The old password no longer works; the new one does.
	if _, err := accounts.Login(ctx, "erin", "correct horse battery"); !errors.Is(err, biz.ErrInvalidCredentials) {
		t.Fatalf("old password: %v", err)
	}
	if _, err := accounts.Login(ctx, "erin", "a brand new passphrase"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	// A stale version cannot reset again.
	if _, err := accounts.ResetPassword(ctx, staff.ID, staff.Version, "another passphrase 123"); !errors.Is(err, biz.ErrStaffVersion) {
		t.Fatalf("stale reset: %v", err)
	}
	live, err := repo.GetStaffByUsername(ctx, "erin")
	if err != nil {
		t.Fatalf("get staff: %v", err)
	}
	if !biz.VerifyPassword(live.PasswordHash, "a brand new passphrase") {
		t.Fatal("the stored hash does not match the new password")
	}
}

func TestStaffLastAdminIsProtected(t *testing.T) {
	_, _, accounts := newAccounts(t)
	ctx := context.Background()
	admin, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "root", Role: biz.RoleAdmin, Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	// Disabling the only administrator is refused.
	disabled := false
	if _, err := accounts.UpdateStaff(ctx, biz.UpdateStaffRequest{
		ID: admin.ID, ExpectedVersion: admin.Version, Enabled: &disabled,
	}); !errors.Is(err, biz.ErrLastAdmin) {
		t.Fatalf("disable last admin: %v", err)
	}
	// Demoting the only administrator is refused too.
	operator := biz.RoleOperator
	if _, err := accounts.UpdateStaff(ctx, biz.UpdateStaffRequest{
		ID: admin.ID, ExpectedVersion: admin.Version, Role: &operator,
	}); !errors.Is(err, biz.ErrLastAdmin) {
		t.Fatalf("demote last admin: %v", err)
	}
	// With a second administrator the change is allowed.
	second, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "root2", Role: biz.RoleAdmin, Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("create second admin: %v", err)
	}
	if _, err := accounts.UpdateStaff(ctx, biz.UpdateStaffRequest{
		ID: admin.ID, ExpectedVersion: admin.Version, Enabled: &disabled,
	}); err != nil {
		t.Fatalf("disable with a second admin: %v", err)
	}
	// A stale version on the surviving admin is a conflict.
	enabled := true
	if _, err := accounts.UpdateStaff(ctx, biz.UpdateStaffRequest{
		ID: second.ID, ExpectedVersion: second.Version + 5, Enabled: &enabled,
	}); !errors.Is(err, biz.ErrStaffVersion) {
		t.Fatalf("stale update: %v", err)
	}
}

func TestStaffSessionExpiryIsEnforced(t *testing.T) {
	client, _, accounts := newAccounts(t)
	ctx := context.Background()
	if _, err := accounts.CreateStaff(ctx, biz.CreateStaffRequest{
		Username: "frank", Role: biz.RoleReviewer, Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := accounts.Login(ctx, "frank", "correct horse battery")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// Expire the row directly; a stale cookie must not authenticate.
	if _, err := client.StaffSession.Update().
		Where(staffsession.TokenHashEQ(biz.HashSessionToken(view.Token))).
		SetExpiresAt(time.Now().UTC().Add(-time.Minute)).
		Save(ctx); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := accounts.Authenticate(ctx, view.Token); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestStaffBootstrapRefusesASecondRun(t *testing.T) {
	_, _, accounts := newAccounts(t)
	ctx := context.Background()
	first, err := accounts.EnsureBootstrap(ctx, "founder", "Founder", "correct horse battery")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if first.Role != biz.RoleAdmin || !first.Enabled {
		t.Fatalf("bootstrap staff: %+v", first)
	}
	if _, err := accounts.EnsureBootstrap(ctx, "intruder", "", "another long password"); !errors.Is(err, biz.ErrAlreadyInitialised) {
		t.Fatalf("second bootstrap: %v", err)
	}
}
