package service

import (
	"context"
	"errors"
	"net/http"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminAuthService owns the browser session of a back-office account.
type AdminAuthService struct {
	v1.UnimplementedAdminAuthServiceServer
	uc *biz.StaffAccounts
	// secureCookie marks the session cookie Secure. Production enables it;
	// the local HTTP check leaves it off so the browser keeps the cookie.
	secureCookie bool
}

// NewAdminAuthService builds the auth service. The cookie is Secure unless
// the deployment says it runs behind plain HTTP (local acceptance only).
func NewAdminAuthService(uc *biz.StaffAccounts, c *conf.Admin) *AdminAuthService {
	return &AdminAuthService{uc: uc, secureCookie: c.GetSecureCookie()}
}

// Login verifies the password and opens a session. The token is only sent as
// an HttpOnly cookie; the body carries the CSRF token the UI must echo.
func (s *AdminAuthService) Login(ctx context.Context, req *v1.LoginRequest) (*v1.AdminSessionView, error) {
	view, err := s.uc.Login(ctx, req.GetUsername(), req.GetPassword())
	if err != nil {
		return nil, toAuthError(err)
	}
	SetCookie(ctx, &http.Cookie{
		Name:     SessionCookieName,
		Value:    view.Token,
		Path:     "/",
		Expires:  view.ExpiresAt,
		HttpOnly: true,
		Secure:   s.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	return toSessionView(view), nil
}

// GetMe returns the caller's session. It is also how the UI refreshes the
// CSRF token after a reload.
func (s *AdminAuthService) GetMe(ctx context.Context, _ *v1.GetMeRequest) (*v1.AdminSessionView, error) {
	actor, err := RequireActor(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	return toSessionView(&biz.SessionView{
		SessionID: actor.SessionID,
		Staff:     actor.Staff,
		CSRFToken: actor.CSRFToken,
		ExpiresAt: actor.ExpiresAt,
	}), nil
}

// Logout revokes the current session and expires the cookie.
func (s *AdminAuthService) Logout(ctx context.Context, _ *v1.LogoutRequest) (*emptypb.Empty, error) {
	actor, err := RequireActor(ctx)
	if err != nil {
		// A logout without a session is already the desired state.
		ClearSessionCookie(ctx)
		return &emptypb.Empty{}, nil
	}
	if err := s.uc.Logout(ctx, actor.SessionID); err != nil {
		return nil, kratoserrors.InternalServer("AUTH_INTERNAL", "退出失败")
	}
	ClearSessionCookie(ctx)
	return &emptypb.Empty{}, nil
}

// toSessionView maps the domain session to the API message.
func toSessionView(view *biz.SessionView) *v1.AdminSessionView {
	if view == nil || view.Staff == nil {
		return nil
	}
	return &v1.AdminSessionView{
		Staff:       toStaffReply(view.Staff),
		Permissions: biz.Permissions(view.Staff.Role),
		CsrfToken:   view.CSRFToken,
		ExpiresAt:   formatMediaTime(view.ExpiresAt),
	}
}

// toStaffReply maps an account without its password hash.
func toStaffReply(staff *biz.Staff) *v1.Staff {
	if staff == nil {
		return nil
	}
	out := &v1.Staff{
		Id:          staff.ID,
		Username:    staff.Username,
		DisplayName: staff.DisplayName,
		Role:        staff.Role,
		Enabled:     staff.Enabled,
		Version:     int32(staff.Version),
		CreatedAt:   formatMediaTime(staff.CreatedAt),
		UpdatedAt:   formatMediaTime(staff.UpdatedAt),
	}
	if staff.LastLoginAt != nil {
		out.LastLoginAt = formatMediaTime(*staff.LastLoginAt)
	}
	return out
}

// toAuthError maps authentication failures. A wrong password and an unknown
// user answer the same way.
func toAuthError(err error) error {
	switch {
	case errors.Is(err, biz.ErrInvalidCredentials):
		return kratoserrors.Unauthorized("INVALID_CREDENTIALS", "用户名或密码错误")
	case errors.Is(err, biz.ErrAccountDisabled):
		return kratoserrors.Forbidden("ACCOUNT_DISABLED", "账号已停用")
	case errors.Is(err, biz.ErrSessionInvalid):
		return kratoserrors.Unauthorized("SESSION_EXPIRED", "会话已失效，请重新登录")
	case errors.Is(err, biz.ErrTooManyAttempts):
		return kratoserrors.TooManyRequests("TOO_MANY_ATTEMPTS", "登录失败次数过多，请稍后重试")
	}
	return kratoserrors.InternalServer("AUTH_INTERNAL", "登录失败")
}
