package service

import (
	"context"
	"errors"
	"net/http"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// AdminUploadService owns the staging upload sessions of manual input.
type AdminUploadService struct {
	v1.UnimplementedAdminUploadServiceServer
	uc *biz.Uploads
}

// NewAdminUploadService builds the upload service. A deployment without an
// object storage keeps the API up and answers each call with a configuration
// error instead of failing at startup.
func NewAdminUploadService(uc *biz.Uploads) *AdminUploadService {
	return &AdminUploadService{uc: uc}
}

// CreateUpload opens one staging session and signs exactly one object key.
func (s *AdminUploadService) CreateUpload(ctx context.Context, req *v1.CreateUploadRequest) (*v1.UploadSession, error) {
	if s.uc == nil {
		return nil, kratoserrors.ServiceUnavailable("MEDIA_NOT_CONFIGURED", "对象存储未配置，无法上传")
	}
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	view, err := s.uc.Create(ctx, biz.CreateUploadRequest{
		RevisionID:              req.GetRevisionId(),
		ExpectedRevisionVersion: int(req.GetExpectedRevisionVersion()),
		EpisodeID:               req.GetEpisodeId(),
		Kind:                    req.GetKind(),
		Filename:                req.GetFilename(),
		SizeBytes:               req.GetSizeBytes(),
		ContentType:             req.GetContentType(),
		ExpectedSHA256:          req.GetExpectedSha256(),
		Actor:                   actor,
	})
	if err != nil {
		return nil, toUploadError(err)
	}
	SetCreated(ctx)
	return toUploadSessionReply(view), nil
}

// RenewUpload signs the same staging key again for a chunked upload.
func (s *AdminUploadService) RenewUpload(ctx context.Context, req *v1.RenewUploadRequest) (*v1.UploadSession, error) {
	if s.uc == nil {
		return nil, kratoserrors.ServiceUnavailable("MEDIA_NOT_CONFIGURED", "对象存储未配置，无法上传")
	}
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	view, err := s.uc.Renew(ctx, req.GetId(), int(req.GetExpectedVersion()), actor)
	if err != nil {
		return nil, toUploadError(err)
	}
	return toUploadSessionReply(view), nil
}

// CompleteUpload confirms the stored object. A completed video session is a
// trusted input, not ready material; a completed cover session is a ready
// cover of the revision.
func (s *AdminUploadService) CompleteUpload(ctx context.Context, req *v1.CompleteUploadRequest) (*v1.UploadSession, error) {
	if s.uc == nil {
		return nil, kratoserrors.ServiceUnavailable("MEDIA_NOT_CONFIGURED", "对象存储未配置，无法上传")
	}
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	session, err := s.uc.Complete(ctx, req.GetId(), int(req.GetExpectedVersion()), actor)
	if err != nil {
		return nil, toUploadError(err)
	}
	return toUploadSessionReply(&biz.UploadView{Session: session}), nil
}

// CancelUpload releases a pending session.
func (s *AdminUploadService) CancelUpload(ctx context.Context, req *v1.CancelUploadRequest) (*v1.UploadSession, error) {
	if s.uc == nil {
		return nil, kratoserrors.ServiceUnavailable("MEDIA_NOT_CONFIGURED", "对象存储未配置，无法上传")
	}
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	session, err := s.uc.Cancel(ctx, req.GetId(), int(req.GetExpectedVersion()), actor)
	if err != nil {
		return nil, toUploadError(err)
	}
	return toUploadSessionReply(&biz.UploadView{Session: session}), nil
}

// ListUploads returns a page of sessions of one revision.
func (s *AdminUploadService) ListUploads(ctx context.Context, req *v1.ListUploadsRequest) (*v1.UploadSessionSet, error) {
	if s.uc == nil {
		return nil, kratoserrors.ServiceUnavailable("MEDIA_NOT_CONFIGURED", "对象存储未配置，无法上传")
	}
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	sessions, total, err := s.uc.List(ctx, biz.UploadQuery{
		RevisionID: req.GetRevisionId(),
		Kind:       req.GetKind(),
		Status:     req.GetStatus(),
		Page:       page,
		PageSize:   size,
	})
	if err != nil {
		return nil, toUploadError(err)
	}
	out := &v1.UploadSessionSet{Total: int32(total), Page: int32(page), PageSize: int32(size)}
	for index := range sessions {
		out.Items = append(out.Items, toUploadSessionReply(&biz.UploadView{Session: &sessions[index]}))
	}
	return out, nil
}

// GetUpload returns one session.
func (s *AdminUploadService) GetUpload(ctx context.Context, req *v1.GetUploadRequest) (*v1.UploadSession, error) {
	if s.uc == nil {
		return nil, kratoserrors.ServiceUnavailable("MEDIA_NOT_CONFIGURED", "对象存储未配置，无法上传")
	}
	session, err := s.uc.Get(ctx, req.GetId())
	if err != nil {
		return nil, toUploadError(err)
	}
	return toUploadSessionReply(&biz.UploadView{Session: session}), nil
}

// toUploadSessionReply maps a session view to the API message. The temporary
// authorization is only attached when the caller just received one.
func toUploadSessionReply(view *biz.UploadView) *v1.UploadSession {
	if view == nil || view.Session == nil {
		return nil
	}
	session := view.Session
	out := &v1.UploadSession{
		Id:              session.ID,
		RevisionId:      session.RevisionID,
		EpisodeId:       session.EpisodeID,
		Kind:            session.Kind,
		Status:          session.Status,
		Version:         int32(session.Version),
		Filename:        session.Filename,
		ContentType:     session.ContentType,
		SizeBytes:       session.SizeBytes,
		ExpectedSha256:  session.ExpectedSHA256,
		Sha256:          session.SHA256,
		Width:           int32(session.Width),
		Height:          int32(session.Height),
		AssetId:         session.AssetID,
		CreatedBy:       session.CreatedBy,
		RevisionVersion: int32(session.RevisionVersion),
		ExpiresAt:       formatMediaTime(session.ExpiresAt),
		CreatedAt:       formatMediaTime(session.CreatedAt),
		UpdatedAt:       formatMediaTime(session.UpdatedAt),
	}
	if session.CompletedAt != nil {
		out.CompletedAt = formatMediaTime(*session.CompletedAt)
	}
	if view.Authorization != nil {
		out.Authorization = &v1.UploadAuthorization{
			Url:       view.Authorization.URL,
			Method:    view.Authorization.Method,
			Headers:   view.Authorization.Headers,
			ExpiresAt: formatMediaTime(view.Authorization.ExpiresAt),
		}
	}
	return out
}

// toUploadError maps an upload failure to the documented API error.
func toUploadError(err error) error {
	switch {
	case errors.Is(err, biz.ErrUploadNotFound):
		return kratoserrors.NotFound("UPLOAD_NOT_FOUND", "上传会话不存在")
	case errors.Is(err, biz.ErrRevisionNotFound):
		return kratoserrors.NotFound("REVISION_NOT_FOUND", "修订不存在")
	case errors.Is(err, biz.ErrUploadExpired):
		return kratoserrors.Conflict("UPLOAD_EXPIRED", "上传授权已过期，请续签")
	case errors.Is(err, biz.ErrUploadNotPending):
		return kratoserrors.Conflict("UPLOAD_NOT_PENDING", "上传会话当前状态不允许该操作")
	case errors.Is(err, biz.ErrUploadAlreadyBound):
		return kratoserrors.Conflict("UPLOAD_ALREADY_BOUND", "上传已绑定输入，不能取消")
	case errors.Is(err, biz.ErrUploadRevisionChanged):
		return kratoserrors.Conflict("REVISION_CHANGED", "修订已变化，旧上传不能绑定")
	case errors.Is(err, biz.ErrUploadEpisodeInvalid):
		return kratoserrors.BadRequest("EPISODE_NOT_IN_SERIES", "分集不属于该修订")
	case errors.Is(err, biz.ErrUploadKindInvalid):
		return kratoserrors.BadRequest("INVALID_UPLOAD_KIND", "上传用途无效")
	case errors.Is(err, biz.ErrUploadSizeInvalid):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "上传大小或文件名无效")
	case errors.Is(err, biz.ErrUploadTooLarge):
		return kratoserrors.New(http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "上传超过大小上限")
	case errors.Is(err, biz.ErrUploadObjectMissing):
		return kratoserrors.Conflict("OBJECT_NOT_FOUND", "暂存对象不存在，请重新上传")
	case errors.Is(err, biz.ErrUploadObjectSize):
		return kratoserrors.Conflict("OBJECT_SIZE_MISMATCH", "暂存对象大小与声明不一致")
	case errors.Is(err, biz.ErrUploadObjectDigest):
		return kratoserrors.Conflict("OBJECT_DIGEST_MISMATCH", "暂存对象摘要与声明不一致")
	case errors.Is(err, biz.ErrUploadObjectMismatch):
		return kratoserrors.Conflict("OBJECT_NOT_CONFIRMED", "对象确认失败，素材未就绪")
	case errors.Is(err, biz.ErrCoverImageInvalid):
		return kratoserrors.BadRequest("INVALID_COVER_IMAGE", "封面图片无效")
	case errors.Is(err, biz.ErrCoverImageUnsupported):
		return kratoserrors.BadRequest("INVALID_COVER_IMAGE", "封面必须是JPEG或PNG图片")
	case errors.Is(err, biz.ErrUploadActorNotOwner):
		return kratoserrors.Forbidden("FORBIDDEN", "只能操作自己创建的上传会话")
	case errors.Is(err, biz.ErrUploadVersionRequired):
		return kratoserrors.Conflict("VERSION_CONFLICT", "上传会话版本已变化")
	case errors.Is(err, biz.ErrRevisionNotEditable):
		return kratoserrors.Conflict("REVISION_LOCKED", "修订已冻结，不能再上传")
	case errors.Is(err, biz.ErrRevisionVersion):
		return kratoserrors.Conflict("REVISION_CHANGED", "修订版本已变化")
	case errors.Is(err, biz.ErrVersionRequired):
		return kratoserrors.BadRequest("REQUIRED_FIELDS_MISSING", "缺少修订版本")
	}
	return kratoserrors.InternalServer("UPLOAD_INTERNAL", "上传操作失败")
}
