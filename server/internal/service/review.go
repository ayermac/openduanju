package service

import (
	"context"
	"errors"
	"time"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// AdminReviewService is the review worklist and the publication step.
type AdminReviewService struct {
	v1.UnimplementedAdminReviewServiceServer
	uc      *biz.Reviews
	storage biz.ObjectStorage
}

// NewAdminReviewService builds the review service.
func NewAdminReviewService(uc *biz.Reviews, storage biz.ObjectStorage) *AdminReviewService {
	return &AdminReviewService{uc: uc, storage: storage}
}

// ListReviews returns a page of review tasks.
func (s *AdminReviewService) ListReviews(ctx context.Context, req *v1.ListReviewsRequest) (*v1.ReviewCaseSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	cases, total, err := s.uc.List(ctx, biz.ReviewQuery{
		Status: req.GetStatus(), Page: page, PageSize: size, Actor: ActorName(ctx),
	})
	if err != nil {
		return nil, toReviewError(err)
	}
	out := &v1.ReviewCaseSet{Total: int32(total), Page: int32(page), PageSize: int32(size)}
	for index := range cases {
		out.Items = append(out.Items, toReviewCaseReply(&cases[index]))
	}
	return out, nil
}

// GetReview returns the review screen, including short-lived preview URLs for
// the private material.
func (s *AdminReviewService) GetReview(ctx context.Context, req *v1.GetReviewRequest) (*v1.ReviewDetail, error) {
	detail, err := s.uc.Get(ctx, req.GetId())
	if err != nil {
		return nil, toReviewError(err)
	}
	out := &v1.ReviewDetail{
		Case:          toReviewCaseReply(&detail.Case),
		Authorization: toGrantReply(detail.Authorization),
	}
	for index := range detail.Episodes {
		episode := detail.Episodes[index]
		out.Episodes = append(out.Episodes, &v1.ReviewEpisode{
			EpisodeId:  episode.EpisodeID,
			Number:     int32(episode.Number),
			ExternalId: episode.ExternalID,
			Asset:      s.toAssetReply(ctx, episode.Asset),
		})
	}
	out.Cover = s.toAssetReply(ctx, detail.Cover)
	if detail.Decision != nil {
		out.Decision = &v1.ReviewDecision{
			Decision:             detail.Decision.Decision,
			Reason:               detail.Decision.Reason,
			DecidedBy:            detail.Decision.DecidedBy,
			DecidedAt:            formatMediaTime(detail.Decision.DecidedAt),
			TaskVersion:          int32(detail.Decision.TaskVersion),
			AuthorizationGrantId: detail.Decision.AuthorizationGrantID,
			AuthorizationVersion: int32(detail.Decision.AuthorizationVersion),
		}
	}
	return out, nil
}

// ClaimReview assigns the task to the calling reviewer.
func (s *AdminReviewService) ClaimReview(ctx context.Context, req *v1.ClaimReviewRequest) (*v1.ReviewCase, error) {
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	review, err := s.uc.Claim(ctx, req.GetId(), int(req.GetExpectedVersion()), actor)
	if err != nil {
		return nil, toReviewError(err)
	}
	return toReviewCaseReply(review), nil
}

// DecideReview records the final outcome of a claimed task.
func (s *AdminReviewService) DecideReview(ctx context.Context, req *v1.DecideReviewRequest) (*v1.ReviewCase, error) {
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	review, err := s.uc.Decide(ctx, biz.DecideReviewRequest{
		TaskID:          req.GetId(),
		ExpectedVersion: int(req.GetExpectedVersion()),
		Decision:        req.GetDecision(),
		Reason:          req.GetReason(),
		Actor:           actor,
		Checklist: biz.ReviewChecklist{
			TitleCoverIntro:     req.GetChecklist().GetTitleCoverIntro(),
			EpisodeMapping:      req.GetChecklist().GetEpisodeMapping(),
			AudioVideo:          req.GetChecklist().GetAudioVideo(),
			AuthorizationPublic: req.GetChecklist().GetAuthorizationPublic(),
			ReviewedEpisodes:    int(req.GetChecklist().GetReviewedEpisodes()),
		},
	})
	if err != nil {
		return nil, toReviewError(err)
	}
	return toReviewCaseReply(review), nil
}

// PublishRevision publishes an approved revision.
func (s *AdminReviewService) PublishRevision(ctx context.Context, req *v1.PublishRevisionRequest) (*v1.Publication, error) {
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	publication, err := s.uc.Publish(ctx, req.GetId(), req.GetRevisionId(), actor)
	if err != nil {
		return nil, toReviewError(err)
	}
	SetCreated(ctx)
	return toPublicationReply(publication), nil
}

// UnpublishSeries takes the current publication down.
func (s *AdminReviewService) UnpublishSeries(ctx context.Context, req *v1.UnpublishSeriesRequest) (*v1.Publication, error) {
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	publication, err := s.uc.Unpublish(ctx, req.GetId(), int(req.GetExpectedVersion()), actor, req.GetReason())
	if err != nil {
		return nil, toReviewError(err)
	}
	return toPublicationReply(publication), nil
}

// GetPublication returns the current publication of a series.
func (s *AdminReviewService) GetPublication(ctx context.Context, req *v1.GetPublicationRequest) (*v1.Publication, error) {
	publication, err := s.uc.Publication(ctx, req.GetId())
	if err != nil {
		return nil, toReviewError(err)
	}
	return toPublicationReply(publication), nil
}

// toAssetReply adds a short-lived preview URL to one private material.
func (s *AdminReviewService) toAssetReply(ctx context.Context, asset *biz.ReviewAsset) *v1.ReviewAsset {
	if asset == nil {
		return nil
	}
	out := &v1.ReviewAsset{
		AssetId:     asset.AssetID,
		ContentType: asset.ContentType,
		SizeBytes:   asset.SizeBytes,
		DurationMs:  asset.DurationMS,
		Width:       int32(asset.Width),
		Height:      int32(asset.Height),
	}
	if s.storage != nil {
		if signed, err := s.storage.PreviewURL(ctx, asset.Object, 10*time.Minute); err == nil {
			out.PreviewUrl = signed.URL
			out.PreviewExpiresAt = formatMediaTime(signed.ExpiresAt)
		}
	}
	return out
}

// toReviewCaseReply maps the worklist row.
func toReviewCaseReply(review *biz.ReviewCase) *v1.ReviewCase {
	if review == nil {
		return nil
	}
	return &v1.ReviewCase{
		TaskId:      review.TaskID,
		RevisionId:  review.RevisionID,
		SeriesId:    review.SeriesID,
		BatchId:     review.BatchID,
		Status:      review.Status,
		Assignee:    review.Assignee,
		SubmittedBy: review.SubmittedBy,
		Version:     int32(review.Version),
		CreatedAt:   formatMediaTime(review.CreatedAt),
		SeriesTitle: review.SeriesTitle,
		ItemTotal:   int32(review.ItemTotal),
		ItemReady:   int32(review.ItemReady),
		CoverStatus: review.CoverStatus,
	}
}

// toPublicationReply maps the publication.
func toPublicationReply(publication *biz.Publication) *v1.Publication {
	if publication == nil {
		return nil
	}
	out := &v1.Publication{
		Id:                   publication.ID,
		SeriesId:             publication.SeriesID,
		RevisionId:           publication.RevisionID,
		Status:               publication.Status,
		PublishedBy:          publication.PublishedBy,
		PublishedAt:          formatMediaTime(publication.PublishedAt),
		UnpublishedBy:        publication.UnpublishedBy,
		AuthorizationGrantId: publication.AuthorizationGrantID,
		AuthorizationVersion: int32(publication.AuthorizationVersion),
		Version:              int32(publication.Version),
	}
	if publication.UnpublishedAt != nil {
		out.UnpublishedAt = formatMediaTime(*publication.UnpublishedAt)
	}
	return out
}

// toReviewError maps review failures to the API errors.
func toReviewError(err error) error {
	switch {
	case errors.Is(err, biz.ErrReviewNotFound), errors.Is(err, biz.ErrPublicationMissing):
		return kratoserrors.NotFound("REVIEW_NOT_FOUND", "审核任务或发布记录不存在")
	case errors.Is(err, biz.ErrReviewVersion), errors.Is(err, biz.ErrPublicationVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "版本已变化，请刷新后重试")
	case errors.Is(err, biz.ErrReviewNotOpen):
		return kratoserrors.Conflict("REVIEW_NOT_OPEN", "任务已被认领或已结束")
	case errors.Is(err, biz.ErrReviewNotClaimed):
		return kratoserrors.Conflict("REVIEW_NOT_CLAIMED", "任务尚未被认领")
	case errors.Is(err, biz.ErrReviewNotAssignee):
		return kratoserrors.Forbidden("NOT_ASSIGNEE", "只有认领人可以裁决")
	case errors.Is(err, biz.ErrReviewSelfDecision):
		return kratoserrors.Forbidden("SELF_REVIEW_FORBIDDEN", "不能审核自己提交的内容")
	case errors.Is(err, biz.ErrReviewFinal):
		return kratoserrors.Conflict("REVIEW_FINAL", "审核决定已作出，不能覆盖")
	case errors.Is(err, biz.ErrReviewReason):
		return kratoserrors.BadRequest("REJECT_REASON_REQUIRED", "驳回必须填写理由")
	case errors.Is(err, biz.ErrReviewChecklist):
		return kratoserrors.BadRequest("CHECKLIST_INCOMPLETE", "通过前必须完成全部检查项并至少抽查1集")
	case errors.Is(err, biz.ErrReviewPrecondition):
		return kratoserrors.Conflict("REVIEW_PRECONDITION", "修订、素材或授权已不满足通过条件")
	case errors.Is(err, biz.ErrReviewNotApproved):
		return kratoserrors.Conflict("REVISION_NOT_APPROVED", "只有已通过的修订可以发布")
	case errors.Is(err, biz.ErrForbidden):
		return kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	return kratoserrors.InternalServer("REVIEW_INTERNAL", "审核操作失败")
}
