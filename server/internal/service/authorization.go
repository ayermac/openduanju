package service

import (
	"context"
	"errors"
	"time"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// AdminAuthorizationService owns series-level authorization grants.
type AdminAuthorizationService struct {
	v1.UnimplementedAdminAuthorizationServiceServer
	uc *biz.Authorizations
}

// NewAdminAuthorizationService builds the authorization service.
func NewAdminAuthorizationService(uc *biz.Authorizations) *AdminAuthorizationService {
	return &AdminAuthorizationService{uc: uc}
}

// CreateGrant adds one grant. The actor is recorded as the confirmant; the
// shared token is a placeholder, not a real staff identity.
func (s *AdminAuthorizationService) CreateGrant(ctx context.Context, req *v1.CreateGrantRequest) (*v1.AuthorizationGrant, error) {
	validFrom, err := parseGrantTime(req.GetValidFrom())
	if err != nil {
		return nil, kratoserrors.BadRequest("INVALID_AUTHORIZATION", "valid_from 必须是 RFC3339 时间")
	}
	var validUntil *time.Time
	if req.GetValidUntil() != "" {
		parsed, err := parseGrantTime(req.GetValidUntil())
		if err != nil {
			return nil, kratoserrors.BadRequest("INVALID_AUTHORIZATION", "valid_until 必须是 RFC3339 时间")
		}
		validUntil = &parsed
	}
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	grant, err := s.uc.Create(ctx, biz.CreateGrantRequest{
		SeriesID:      req.GetId(),
		Reference:     req.GetReference(),
		AllowProcess:  req.GetAllowProcess(),
		AllowDisplay:  req.GetAllowDisplay(),
		AllowPlayback: req.GetAllowPlayback(),
		ValidFrom:     validFrom,
		ValidUntil:    validUntil,
		Actor:         actor,
	})
	if err != nil {
		return nil, toGrantError(err)
	}
	SetCreated(ctx)
	return toGrantReply(grant), nil
}

// RevokeGrant marks one grant revoked with a version CAS.
func (s *AdminAuthorizationService) RevokeGrant(ctx context.Context, req *v1.RevokeGrantRequest) (*v1.AuthorizationGrant, error) {
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	grant, err := s.uc.Revoke(ctx, req.GetId(), int(req.GetExpectedVersion()), actor, req.GetReason())
	if err != nil {
		return nil, toGrantError(err)
	}
	return toGrantReply(grant), nil
}

// ListGrants returns a page of grants.
func (s *AdminAuthorizationService) ListGrants(ctx context.Context, req *v1.ListGrantsRequest) (*v1.AuthorizationGrantSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	if status := req.GetStatus(); status != "" && status != biz.GrantActive && status != biz.GrantRevoked {
		return nil, kratoserrors.BadRequest("INVALID_FILTER", "未知的授权状态")
	}
	grants, total, err := s.uc.List(ctx, biz.GrantQuery{
		SeriesID: req.GetSeriesId(),
		Status:   req.GetStatus(),
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		return nil, toGrantError(err)
	}
	out := &v1.AuthorizationGrantSet{Total: int32(total), Page: int32(page), PageSize: int32(size)}
	for index := range grants {
		out.Items = append(out.Items, toGrantReply(&grants[index]))
	}
	return out, nil
}

// GetGrant returns one grant.
func (s *AdminAuthorizationService) GetGrant(ctx context.Context, req *v1.GetGrantRequest) (*v1.AuthorizationGrant, error) {
	grant, err := s.uc.Get(ctx, req.GetId())
	if err != nil {
		return nil, toGrantError(err)
	}
	return toGrantReply(grant), nil
}

// parseGrantTime reads a UTC timestamp. A value without a zone is rejected
// instead of guessed.
func parseGrantTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

// toGrantReply maps the domain grant to the API message.
func toGrantReply(grant *biz.AuthorizationGrant) *v1.AuthorizationGrant {
	if grant == nil {
		return nil
	}
	out := &v1.AuthorizationGrant{
		Id:            grant.ID,
		SeriesId:      grant.SeriesID,
		Reference:     grant.Reference,
		AllowProcess:  grant.AllowProcess,
		AllowDisplay:  grant.AllowDisplay,
		AllowPlayback: grant.AllowPlayback,
		ValidFrom:     formatMediaTime(grant.ValidFrom),
		Status:        grant.Status,
		Version:       int32(grant.Version),
		ConfirmedBy:   grant.ConfirmedBy,
		ConfirmedAt:   formatMediaTime(grant.ConfirmedAt),
		RevokedBy:     grant.RevokedBy,
		RevokeReason:  grant.RevokeReason,
		CreatedAt:     formatMediaTime(grant.CreatedAt),
		UpdatedAt:     formatMediaTime(grant.UpdatedAt),
	}
	if grant.ValidUntil != nil {
		out.ValidUntil = formatMediaTime(*grant.ValidUntil)
	}
	if grant.RevokedAt != nil {
		out.RevokedAt = formatMediaTime(*grant.RevokedAt)
	}
	return out
}

// toGrantError maps grant failures to the documented API error codes.
func toGrantError(err error) error {
	switch {
	case errors.Is(err, biz.ErrGrantNotFound):
		return kratoserrors.NotFound("GRANT_NOT_FOUND", "授权依据不存在")
	case errors.Is(err, biz.ErrSeriesNotFound):
		return kratoserrors.NotFound("SERIES_NOT_FOUND", "系列不存在")
	case errors.Is(err, biz.ErrGrantVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "授权依据版本已变化")
	case errors.Is(err, biz.ErrGrantInvalid):
		return kratoserrors.BadRequest("INVALID_AUTHORIZATION", "授权依据字段无效")
	}
	return kratoserrors.InternalServer("AUTHORIZATION_INTERNAL", "授权操作失败")
}
