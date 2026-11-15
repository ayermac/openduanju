package service

import (
	"context"
	"errors"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// AdminStaffService manages back-office accounts.
type AdminStaffService struct {
	v1.UnimplementedAdminStaffServiceServer
	uc *biz.StaffAccounts
}

// NewAdminStaffService builds the staff service.
func NewAdminStaffService(uc *biz.StaffAccounts) *AdminStaffService {
	return &AdminStaffService{uc: uc}
}

// ListStaff returns a page of accounts.
func (s *AdminStaffService) ListStaff(ctx context.Context, req *v1.ListStaffRequest) (*v1.StaffSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	if req.GetRole() != "" && !biz.KnownRole(req.GetRole()) {
		return nil, kratoserrors.BadRequest("INVALID_FILTER", "未知的角色")
	}
	var enabled *bool
	if req.Enabled != nil {
		value := req.GetEnabled()
		enabled = &value
	}
	staff, total, err := s.uc.ListStaff(ctx, biz.StaffQuery{
		Page: page, PageSize: size, Role: req.GetRole(), Enabled: enabled, Q: req.GetQ(),
	})
	if err != nil {
		return nil, toStaffError(err)
	}
	out := &v1.StaffSet{Total: int32(total), Page: int32(page), PageSize: int32(size)}
	for index := range staff {
		out.Items = append(out.Items, toStaffReply(&staff[index]))
	}
	return out, nil
}

// CreateStaff adds one account with an explicit role and password.
func (s *AdminStaffService) CreateStaff(ctx context.Context, req *v1.CreateStaffRequest) (*v1.Staff, error) {
	actor, err := RequireActor(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	staff, err := s.uc.CreateStaff(ctx, biz.CreateStaffRequest{
		Username:    req.GetUsername(),
		DisplayName: req.GetDisplayName(),
		Role:        req.GetRole(),
		Password:    req.GetPassword(),
		Actor:       actor.Staff.Username,
	})
	if err != nil {
		return nil, toStaffError(err)
	}
	SetCreated(ctx)
	return toStaffReply(staff), nil
}

// UpdateStaff changes display name, role or enabled state under a version CAS.
func (s *AdminStaffService) UpdateStaff(ctx context.Context, req *v1.UpdateStaffRequest) (*v1.Staff, error) {
	update := biz.UpdateStaffRequest{
		ID:              req.GetId(),
		ExpectedVersion: int(req.GetExpectedVersion()),
		Actor:           ActorName(ctx),
	}
	if req.DisplayName != nil {
		value := req.GetDisplayName()
		update.DisplayName = &value
	}
	if req.Role != nil {
		value := req.GetRole()
		update.Role = &value
	}
	if req.Enabled != nil {
		value := req.GetEnabled()
		update.Enabled = &value
	}
	if update.DisplayName == nil && update.Role == nil && update.Enabled == nil {
		return nil, kratoserrors.BadRequest("INVALID_FIELD_MASK", "没有要更新的字段")
	}
	staff, err := s.uc.UpdateStaff(ctx, update)
	if err != nil {
		return nil, toStaffError(err)
	}
	return toStaffReply(staff), nil
}

// ResetPassword sets a new password and revokes the account's sessions.
func (s *AdminStaffService) ResetPassword(ctx context.Context, req *v1.ResetStaffPasswordRequest) (*v1.Staff, error) {
	staff, err := s.uc.ResetPassword(ctx, req.GetId(), int(req.GetExpectedVersion()), req.GetNewPassword())
	if err != nil {
		return nil, toStaffError(err)
	}
	return toStaffReply(staff), nil
}

// toStaffError maps account failures to the documented API errors.
func toStaffError(err error) error {
	switch {
	case errors.Is(err, biz.ErrStaffNotFound):
		return kratoserrors.NotFound("STAFF_NOT_FOUND", "账号不存在")
	case errors.Is(err, biz.ErrUsernameExists):
		return kratoserrors.Conflict("USERNAME_EXISTS", "用户名已存在")
	case errors.Is(err, biz.ErrStaffVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "账号版本已变化")
	case errors.Is(err, biz.ErrLastAdmin):
		return kratoserrors.Conflict("LAST_ADMIN_REQUIRED", "必须保留至少一个启用的管理员")
	case errors.Is(err, biz.ErrStaffInvalid):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "账号字段无效：密码至少12位，角色必须有效")
	}
	return kratoserrors.InternalServer("STAFF_INTERNAL", "账号操作失败")
}
