package biz_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
)

// grantFixture builds an active grant that covers series 7.
func grantFixture(now time.Time) *biz.AuthorizationGrant {
	from := now.Add(-time.Hour)
	until := now.Add(24 * time.Hour)
	return &biz.AuthorizationGrant{
		ID:            3,
		SeriesID:      7,
		Reference:     "AUTH-2026-001",
		AllowProcess:  true,
		AllowDisplay:  true,
		AllowPlayback: true,
		ValidFrom:     from.UTC(),
		ValidUntil:    &until,
		Status:        biz.GrantActive,
		Version:       2,
	}
}

func TestGrantValidityWindow(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	grant := grantFixture(now)

	if !grant.ValidAt(now) {
		t.Fatal("an active grant inside its window is not valid")
	}
	if grant.ValidAt(now.Add(-2 * time.Hour)) {
		t.Fatal("a grant was valid before valid_from")
	}
	if grant.ValidAt(now.Add(48 * time.Hour)) {
		t.Fatal("a grant was valid after valid_until")
	}
	// A grant without an end date is valid until revoked; it must not be
	// presented as a perpetual license, but the gate still treats it as open.
	open := grantFixture(now)
	open.ValidUntil = nil
	if !open.ValidAt(now) {
		t.Fatal("a grant without an end date is not valid")
	}
	// Revocation wins over any window.
	revoked := grantFixture(now)
	revoked.Status = biz.GrantRevoked
	if revoked.ValidAt(now) {
		t.Fatal("a revoked grant was valid")
	}
	// Not yet started.
	notYet := grantFixture(now)
	notYet.ValidFrom = future.UTC()
	if notYet.ValidAt(now) {
		t.Fatal("a grant that starts in the future was valid")
	}
	// An expired window is invalid even while the status is active.
	expired := grantFixture(now)
	ended := past.UTC()
	expired.ValidUntil = &ended
	if expired.ValidAt(now) {
		t.Fatal("an expired grant was valid")
	}
}

func TestGrantPermissionsAreSeparate(t *testing.T) {
	now := time.Now()
	grant := grantFixture(now)
	grant.AllowProcess = true
	grant.AllowDisplay = false
	grant.AllowPlayback = true

	if !grant.Allows(biz.PermissionProcess, now) {
		t.Fatal("process should be allowed")
	}
	if grant.Allows(biz.PermissionDisplay, now) {
		t.Fatal("display should not be allowed")
	}
	if err := biz.EnsureForDisplay(grant, 7, now); !errors.Is(err, biz.ErrGrantPermission) {
		t.Fatalf("display gate: %v", err)
	}
	// Review needs both display and playback.
	if err := biz.EnsureForReview(grant, 7, now); !errors.Is(err, biz.ErrGrantPermission) {
		t.Fatalf("review gate with display off: %v", err)
	}
	grant.AllowDisplay = true
	grant.AllowPlayback = false
	if err := biz.EnsureForReview(grant, 7, now); !errors.Is(err, biz.ErrGrantPermission) {
		t.Fatalf("review gate with playback off: %v", err)
	}
	grant.AllowPlayback = true
	if err := biz.EnsureForReview(grant, 7, now); err != nil {
		t.Fatalf("review gate: %v", err)
	}
	// Processing does not imply playback.
	processOnly := grantFixture(now)
	processOnly.AllowDisplay = false
	processOnly.AllowPlayback = false
	if err := biz.EnsureForPlayback(processOnly, 7, now); !errors.Is(err, biz.ErrGrantPermission) {
		t.Fatalf("playback gate on a process-only grant: %v", err)
	}
	if err := biz.EnsureForPublish(processOnly, 7, now); !errors.Is(err, biz.ErrGrantPermission) {
		t.Fatalf("publish gate on a process-only grant: %v", err)
	}
}

func TestGrantGatesRejectScopeVersionAndState(t *testing.T) {
	now := time.Now()
	grant := grantFixture(now)

	if err := biz.EnsureForSubmit(grant, 8, 2, now); !errors.Is(err, biz.ErrGrantNotApplicable) {
		t.Fatalf("a grant of another series was accepted: %v", err)
	}
	if err := biz.EnsureForSubmit(grant, 7, 1, now); !errors.Is(err, biz.ErrGrantVersion) {
		t.Fatalf("a version drift was accepted: %v", err)
	}
	if err := biz.EnsureForSubmit(grant, 7, 2, now); err != nil {
		t.Fatalf("the expected version was rejected: %v", err)
	}
	past := now.Add(-48 * time.Hour)
	expired := grantFixture(now)
	expired.ValidUntil = &past
	if err := biz.EnsureForSubmit(expired, 7, 2, now); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("an expired grant was accepted: %v", err)
	}
	revoked := grantFixture(now)
	revoked.Status = biz.GrantRevoked
	if err := biz.EnsureForSubmit(revoked, 7, 2, now); !errors.Is(err, biz.ErrGrantNotEffective) {
		t.Fatalf("a revoked grant was accepted: %v", err)
	}
	if err := biz.EnsureForSubmit(nil, 7, 0, now); !errors.Is(err, biz.ErrGrantNotFound) {
		t.Fatalf("a missing grant was accepted: %v", err)
	}
}

func TestGrantFingerprintCoversTheFrozenFacts(t *testing.T) {
	now := time.Now()
	grant := grantFixture(now)
	base := grant.Fingerprint()
	if base == "" || len(base) != 64 {
		t.Fatalf("fingerprint: %q", base)
	}
	if grant.Fingerprint() != base {
		t.Fatal("the fingerprint is not stable")
	}
	changed := grantFixture(now)
	changed.AllowPlayback = false
	if changed.Fingerprint() == base {
		t.Fatal("the fingerprint ignores a permission change")
	}
	changed = grantFixture(now)
	changed.Reference = "AUTH-2026-002"
	if changed.Fingerprint() == base {
		t.Fatal("the fingerprint ignores the reference")
	}
	changed = grantFixture(now)
	later := now.Add(72 * time.Hour)
	changed.ValidUntil = &later
	if changed.Fingerprint() == base {
		t.Fatal("the fingerprint ignores the validity window")
	}
}

// fakeGrantRepo records the create and revoke calls of the use case.
type fakeGrantRepo struct {
	created biz.CreateGrantRequest
	revoked bool
	grant   *biz.AuthorizationGrant
}

func (f *fakeGrantRepo) CreateGrant(_ context.Context, request biz.CreateGrantRequest) (*biz.AuthorizationGrant, error) {
	f.created = request
	grant := grantFixture(time.Now())
	grant.SeriesID = request.SeriesID
	grant.Reference = request.Reference
	grant.ValidFrom = request.ValidFrom
	grant.ValidUntil = request.ValidUntil
	f.grant = grant
	return grant, nil
}

func (f *fakeGrantRepo) GetGrant(context.Context, int64) (*biz.AuthorizationGrant, error) {
	return f.grant, nil
}

func (f *fakeGrantRepo) ListGrants(context.Context, biz.GrantQuery) ([]biz.AuthorizationGrant, int, error) {
	return nil, 0, nil
}

func (f *fakeGrantRepo) RevokeGrant(_ context.Context, _ int64, _ int, _ string, reason string) (*biz.AuthorizationGrant, error) {
	f.revoked = true
	grant := grantFixture(time.Now())
	grant.Status = biz.GrantRevoked
	grant.RevokeReason = reason
	return grant, nil
}

func TestCreateGrantRequiresExplicitFields(t *testing.T) {
	repo := &fakeGrantRepo{}
	uc := biz.NewAuthorizations(repo, nil)
	ctx := context.Background()
	from := time.Now().UTC()
	until := from.Add(time.Hour)

	if _, err := uc.Create(ctx, biz.CreateGrantRequest{SeriesID: 7, ValidFrom: from, Actor: "tester"}); !errors.Is(err, biz.ErrGrantInvalid) {
		t.Fatalf("missing reference: %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateGrantRequest{SeriesID: 7, Reference: "A", Actor: "tester"}); !errors.Is(err, biz.ErrGrantInvalid) {
		t.Fatalf("missing valid_from: %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateGrantRequest{SeriesID: 7, Reference: "A", ValidFrom: from, ValidUntil: &from, Actor: "tester"}); !errors.Is(err, biz.ErrGrantInvalid) {
		t.Fatalf("valid_until equal to valid_from: %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateGrantRequest{SeriesID: 7, Reference: "A", ValidFrom: from}); !errors.Is(err, biz.ErrGrantInvalid) {
		t.Fatalf("missing actor: %v", err)
	}
	if _, err := uc.Create(ctx, biz.CreateGrantRequest{
		SeriesID: 7, Reference: " AUTH-1 ", ValidFrom: from, ValidUntil: &until, Actor: "tester",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if repo.created.Reference != "AUTH-1" {
		t.Fatalf("reference was not trimmed: %q", repo.created.Reference)
	}
	// Permission bits are not defaulted open by the use case.
	if repo.created.AllowProcess || repo.created.AllowDisplay || repo.created.AllowPlayback {
		t.Fatal("the use case granted a permission that was not requested")
	}
}

func TestRevokeGrantRequiresVersionAndReason(t *testing.T) {
	repo := &fakeGrantRepo{}
	uc := biz.NewAuthorizations(repo, nil)
	ctx := context.Background()
	if _, err := uc.Revoke(ctx, 3, 0, "tester", "wrong scope"); !errors.Is(err, biz.ErrGrantVersion) {
		t.Fatalf("missing version: %v", err)
	}
	if _, err := uc.Revoke(ctx, 3, 1, "tester", "  "); !errors.Is(err, biz.ErrGrantInvalid) {
		t.Fatalf("missing reason: %v", err)
	}
	if _, err := uc.Revoke(ctx, 3, 1, "", "wrong scope"); !errors.Is(err, biz.ErrGrantInvalid) {
		t.Fatalf("missing actor: %v", err)
	}
	revoked, err := uc.Revoke(ctx, 3, 1, "tester", "wrong scope")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !repo.revoked || revoked.RevokeReason != "wrong scope" {
		t.Fatalf("revoke was not recorded: %+v", revoked)
	}
}
