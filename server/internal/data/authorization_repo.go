package data

import (
	"context"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/authorizationgrant"
	"openduanju-server/internal/data/ent/contentseries"
)

// AuthorizationRepo persists series-level authorization grants.
type AuthorizationRepo struct {
	client *ent.Client
}

// NewAuthorizationRepo builds the repository.
func NewAuthorizationRepo(d *Data) *AuthorizationRepo {
	return &AuthorizationRepo{client: d.Ent()}
}

// NewAuthorizationRepoWithDB builds the repository over an explicit client.
func NewAuthorizationRepoWithDB(client *ent.Client) *AuthorizationRepo {
	return &AuthorizationRepo{client: client}
}

// CreateGrant stores one immutable grant. Only a new row can change the
// permissions or the validity window of a series.
func (r *AuthorizationRepo) CreateGrant(ctx context.Context, request biz.CreateGrantRequest) (*biz.AuthorizationGrant, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	exists, err := tx.ContentSeries.Query().Where(contentseries.IDEQ(request.SeriesID)).Exist(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if !exists {
		_ = tx.Rollback()
		return nil, biz.ErrSeriesNotFound
	}
	create := tx.AuthorizationGrant.Create().
		SetSeriesID(request.SeriesID).
		SetReference(request.Reference).
		SetAllowProcess(request.AllowProcess).
		SetAllowDisplay(request.AllowDisplay).
		SetAllowPlayback(request.AllowPlayback).
		SetValidFrom(request.ValidFrom.UTC()).
		SetStatus(biz.GrantActive).
		SetVersion(1).
		SetConfirmedBy(request.Actor).
		SetConfirmedAt(time.Now().UTC())
	if request.ValidUntil != nil {
		create = create.SetValidUntil(request.ValidUntil.UTC())
	}
	created, err := create.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, created.ID, int(created.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toAuthorizationGrant(created), nil
}

// GetGrant returns one grant.
func (r *AuthorizationRepo) GetGrant(ctx context.Context, id int64) (*biz.AuthorizationGrant, error) {
	row, err := r.client.AuthorizationGrant.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	return toAuthorizationGrant(row), nil
}

// ListGrants returns a page of grants.
func (r *AuthorizationRepo) ListGrants(ctx context.Context, query biz.GrantQuery) ([]biz.AuthorizationGrant, int, error) {
	page, size := mediaPage(query.Page, query.PageSize)
	q := r.client.AuthorizationGrant.Query()
	if query.SeriesID > 0 {
		q = q.Where(authorizationgrant.SeriesIDEQ(query.SeriesID))
	}
	if query.Status != "" {
		q = q.Where(authorizationgrant.StatusEQ(query.Status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(authorizationgrant.FieldID)).
		Offset((page - 1) * size).
		Limit(size).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.AuthorizationGrant, 0, len(rows))
	for _, row := range rows {
		out = append(out, *toAuthorizationGrant(row))
	}
	return out, total, nil
}

// RevokeGrant applies the version CAS. A repeated revoke with the same
// version is idempotent; a different version is a conflict. Revocation never
// rewrites the confirmation facts.
func (r *AuthorizationRepo) RevokeGrant(ctx context.Context, id int64, expectedVersion int, actor string, reason string) (*biz.AuthorizationGrant, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.AuthorizationGrant.Query().
		Where(authorizationgrant.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	if row.Status == biz.GrantRevoked {
		if row.Version == expectedVersion {
			// Revoked by an earlier call: the repeat changes nothing.
			biz.AuditUnchanged(ctx)
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return toAuthorizationGrant(row), nil
		}
		_ = tx.Rollback()
		return nil, biz.ErrGrantVersion
	}
	if row.Version != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrGrantVersion
	}
	now := time.Now().UTC()
	updated, err := tx.AuthorizationGrant.UpdateOneID(id).
		SetStatus(biz.GrantRevoked).
		SetRevokedBy(actor).
		SetRevokedAt(now).
		SetRevokeReason(reason).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return toAuthorizationGrant(updated), nil
}

// toAuthorizationGrant maps the entity to the domain type.
func toAuthorizationGrant(row *ent.AuthorizationGrant) *biz.AuthorizationGrant {
	if row == nil {
		return nil
	}
	out := &biz.AuthorizationGrant{
		ID:            row.ID,
		SeriesID:      row.SeriesID,
		Reference:     row.Reference,
		AllowProcess:  row.AllowProcess,
		AllowDisplay:  row.AllowDisplay,
		AllowPlayback: row.AllowPlayback,
		ValidFrom:     row.ValidFrom.UTC(),
		Status:        row.Status,
		Version:       row.Version,
		ConfirmedBy:   row.ConfirmedBy,
		ConfirmedAt:   row.ConfirmedAt.UTC(),
		RevokedBy:     row.RevokedBy,
		RevokeReason:  row.RevokeReason,
		CreatedAt:     row.CreatedAt.UTC(),
		UpdatedAt:     row.UpdatedAt.UTC(),
	}
	if row.ValidUntil != nil {
		until := row.ValidUntil.UTC()
		out.ValidUntil = &until
	}
	if row.RevokedAt != nil {
		revoked := row.RevokedAt.UTC()
		out.RevokedAt = &revoked
	}
	return out
}
