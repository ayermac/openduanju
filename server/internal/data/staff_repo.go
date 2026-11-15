package data

import (
	"context"
	"strings"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/loginattempt"
	"openduanju-server/internal/data/ent/staff"
	"openduanju-server/internal/data/ent/staffsession"
)

// StaffRepo persists accounts and their sessions.
type StaffRepo struct {
	client *ent.Client
}

// NewStaffRepo builds the repository.
func NewStaffRepo(d *Data) *StaffRepo {
	return &StaffRepo{client: d.Ent()}
}

// NewStaffRepoWithDB builds the repository over an explicit client.
func NewStaffRepoWithDB(client *ent.Client) *StaffRepo {
	return &StaffRepo{client: client}
}

// CreateStaff stores one account. A duplicate username is reported as a
// conflict instead of a database error.
func (r *StaffRepo) CreateStaff(ctx context.Context, request biz.CreateStaffRequest, passwordHash string) (*biz.Staff, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	exists, err := tx.Staff.Query().Where(staff.UsernameEQ(request.Username)).Exist(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if exists {
		_ = tx.Rollback()
		return nil, biz.ErrUsernameExists
	}
	created, err := tx.Staff.Create().
		SetUsername(request.Username).
		SetDisplayName(request.DisplayName).
		SetRole(request.Role).
		SetEnabled(true).
		SetPasswordHash(passwordHash).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			return nil, biz.ErrUsernameExists
		}
		return nil, err
	}
	auditResource(ctx, created.ID, int(created.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toStaff(created), nil
}

// GetStaff returns one account by id.
func (r *StaffRepo) GetStaff(ctx context.Context, id int64) (*biz.Staff, error) {
	row, err := r.client.Staff.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrStaffNotFound
		}
		return nil, err
	}
	return toStaff(row), nil
}

// GetStaffByUsername returns one account by its unique username.
func (r *StaffRepo) GetStaffByUsername(ctx context.Context, username string) (*biz.Staff, error) {
	row, err := r.client.Staff.Query().
		Where(staff.UsernameEQ(username)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrStaffNotFound
		}
		return nil, err
	}
	return toStaff(row), nil
}

// ListStaff returns a page of accounts.
func (r *StaffRepo) ListStaff(ctx context.Context, query biz.StaffQuery) ([]biz.Staff, int, error) {
	page, size := mediaPage(query.Page, query.PageSize)
	q := r.client.Staff.Query()
	if query.Role != "" {
		q = q.Where(staff.RoleEQ(query.Role))
	}
	if query.Enabled != nil {
		q = q.Where(staff.EnabledEQ(*query.Enabled))
	}
	if search := strings.TrimSpace(query.Q); search != "" {
		q = q.Where(staff.Or(
			staff.UsernameContains(search),
			staff.DisplayNameContains(search),
		))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Asc(staff.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.Staff, 0, len(rows))
	for _, row := range rows {
		out = append(out, *toStaff(row))
	}
	return out, total, nil
}

// UpdateStaff changes display name, role and enabled state under a version
// CAS. Removing the last enabled administrator is refused, and a revoked
// account loses every session in the same transaction.
func (r *StaffRepo) UpdateStaff(ctx context.Context, request biz.UpdateStaffRequest) (*biz.Staff, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.Staff.Query().Where(staff.IDEQ(request.ID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrStaffNotFound
		}
		return nil, err
	}
	if row.Version != request.ExpectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrStaffVersion
	}
	newRole := row.Role
	if request.Role != nil {
		newRole = *request.Role
	}
	newEnabled := row.Enabled
	if request.Enabled != nil {
		newEnabled = *request.Enabled
	}
	losingAdmin := row.Role == biz.RoleAdmin && row.Enabled && (newRole != biz.RoleAdmin || !newEnabled)
	if losingAdmin {
		others, err := tx.Staff.Query().
			Where(staff.RoleEQ(biz.RoleAdmin), staff.EnabledEQ(true), staff.IDNEQ(row.ID)).
			Count(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if others == 0 {
			_ = tx.Rollback()
			return nil, biz.ErrLastAdmin
		}
	}
	update := tx.Staff.UpdateOneID(row.ID).AddVersion(1)
	if request.DisplayName != nil {
		update = update.SetDisplayName(*request.DisplayName)
	}
	if request.Role != nil {
		update = update.SetRole(*request.Role)
	}
	if request.Enabled != nil {
		update = update.SetEnabled(*request.Enabled)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if !updated.Enabled {
		// A disabled account cannot keep a live session.
		if _, err := tx.StaffSession.Update().
			Where(staffsession.StaffIDEQ(row.ID), staffsession.RevokedAtIsNil()).
			SetRevokedAt(time.Now().UTC()).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toStaff(updated), nil
}

// ResetPassword replaces the password and revokes every session of the
// account, so a stolen cookie stops working.
func (r *StaffRepo) ResetPassword(ctx context.Context, id int64, expectedVersion int, passwordHash string) (*biz.Staff, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.Staff.Query().Where(staff.IDEQ(id)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrStaffNotFound
		}
		return nil, err
	}
	if row.Version != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrStaffVersion
	}
	updated, err := tx.Staff.UpdateOneID(id).
		SetPasswordHash(passwordHash).
		AddPasswordVersion(1).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.StaffSession.Update().
		Where(staffsession.StaffIDEQ(id), staffsession.RevokedAtIsNil()).
		SetRevokedAt(time.Now().UTC()).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toStaff(updated), nil
}

// CreateSession stores one session. Only the token hash is persisted.
func (r *StaffRepo) CreateSession(ctx context.Context, staffID int64, tokenHash string, csrfToken string, expiresAt time.Time) (*biz.SessionRecord, error) {
	created, err := r.client.StaffSession.Create().
		SetStaffID(staffID).
		SetTokenHash(tokenHash).
		SetCsrfToken(csrfToken).
		SetExpiresAt(expiresAt.UTC()).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return &biz.SessionRecord{ID: created.ID, StaffID: staffID, CSRFToken: csrfToken, ExpiresAt: created.ExpiresAt}, nil
}

// GetSessionByTokenHash returns the session and its staff row.
func (r *StaffRepo) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*biz.SessionRecord, error) {
	row, err := r.client.StaffSession.Query().
		Where(staffsession.TokenHashEQ(tokenHash)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrSessionInvalid
		}
		return nil, err
	}
	record := &biz.SessionRecord{
		ID:        row.ID,
		StaffID:   row.StaffID,
		CSRFToken: row.CsrfToken,
		ExpiresAt: row.ExpiresAt.UTC(),
	}
	if row.RevokedAt != nil {
		revoked := row.RevokedAt.UTC()
		record.RevokedAt = &revoked
	}
	staffRow, err := r.client.Staff.Get(ctx, row.StaffID)
	if err != nil {
		if ent.IsNotFound(err) {
			return record, nil
		}
		return nil, err
	}
	record.Staff = toStaff(staffRow)
	return record, nil
}

// RevokeSession revokes one session.
func (r *StaffRepo) RevokeSession(ctx context.Context, sessionID int64) error {
	_, err := r.client.StaffSession.UpdateOneID(sessionID).
		SetRevokedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	return nil
}

// RevokeStaffSessions revokes every live session of an account.
func (r *StaffRepo) RevokeStaffSessions(ctx context.Context, staffID int64) (int, error) {
	return r.client.StaffSession.Update().
		Where(staffsession.StaffIDEQ(staffID), staffsession.RevokedAtIsNil()).
		SetRevokedAt(time.Now().UTC()).
		Save(ctx)
}

// CountRecentFailures counts failed logins of one username since a point in
// time. Deleted usernames are counted too, which keeps the response uniform.
func (r *StaffRepo) CountRecentFailures(ctx context.Context, username string, since time.Time) (int, error) {
	return r.client.LoginAttempt.Query().
		Where(
			loginattempt.UsernameEQ(username),
			loginattempt.SucceededEQ(false),
			loginattempt.AttemptedAtGTE(since.UTC()),
		).
		Count(ctx)
}

// RecordLoginAttempt appends one attempt. Successful attempts are kept for
// the audit trail but never counted by the throttle.
func (r *StaffRepo) RecordLoginAttempt(ctx context.Context, username string, succeeded bool, at time.Time) error {
	return r.client.LoginAttempt.Create().
		SetUsername(username).
		SetSucceeded(succeeded).
		SetAttemptedAt(at.UTC()).
		Exec(ctx)
}

// CountEnabledAdmins reports how many enabled administrators exist.
func (r *StaffRepo) CountEnabledAdmins(ctx context.Context) (int, error) {
	return r.client.Staff.Query().
		Where(staff.RoleEQ(biz.RoleAdmin), staff.EnabledEQ(true)).
		Count(ctx)
}

// toStaff maps the entity to the domain type.
func toStaff(row *ent.Staff) *biz.Staff {
	if row == nil {
		return nil
	}
	out := &biz.Staff{
		ID:              row.ID,
		Username:        row.Username,
		DisplayName:     row.DisplayName,
		Role:            row.Role,
		Enabled:         row.Enabled,
		Version:         row.Version,
		PasswordVersion: row.PasswordVersion,
		CreatedAt:       row.CreatedAt.UTC(),
		UpdatedAt:       row.UpdatedAt.UTC(),
		PasswordHash:    row.PasswordHash,
	}
	if row.LastLoginAt != nil {
		last := row.LastLoginAt.UTC()
		out.LastLoginAt = &last
	}
	return out
}

// compile-time guard: the repository implements the use-case port.
var _ biz.StaffRepo = (*StaffRepo)(nil)
