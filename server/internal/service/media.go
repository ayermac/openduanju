package service

import (
	"context"
	"errors"
	"time"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// AdminMediaService owns persistent media batches over frozen revisions.
type AdminMediaService struct {
	v1.UnimplementedAdminMediaServiceServer
	uc *biz.MediaBatches
}

// NewAdminMediaService builds the media service.
func NewAdminMediaService(uc *biz.MediaBatches) *AdminMediaService {
	return &AdminMediaService{uc: uc}
}

// SubmitRevision freezes the current revision selection and queues a
// persistent batch. The processing runs in the media worker, so the call
// answers 202 with the batch instead of waiting for the media.
func (s *AdminMediaService) SubmitRevision(ctx context.Context, req *v1.SubmitMediaBatchRequest) (*v1.MediaBatch, error) {
	actor, err := RequireActorName(ctx)
	if err != nil {
		return nil, kratoserrors.Unauthorized("AUTH_REQUIRED", "需要登录")
	}
	batch, err := s.uc.SubmitRevision(ctx, biz.SubmitBatchRequest{
		RevisionID:                   req.GetId(),
		ExpectedVersion:              int(req.GetExpectedVersion()),
		ProfileVersion:               req.GetProcessingProfileKey(),
		InputKind:                    req.GetInputKind(),
		Actor:                        actor,
		AuthorizationGrantID:         req.GetAuthorizationGrantId(),
		ExpectedAuthorizationVersion: int(req.GetExpectedAuthorizationVersion()),
	})
	if err != nil {
		return nil, toMediaError(err)
	}
	SetAccepted(ctx)
	return toMediaBatchReply(batch), nil
}

// ListBatches returns a page of batches.
func (s *AdminMediaService) ListBatches(ctx context.Context, req *v1.ListMediaBatchesRequest) (*v1.MediaBatchSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	if !knownBatchStatus(req.GetStatus()) {
		return nil, kratoserrors.BadRequest("INVALID_FILTER", "未知的批次状态")
	}
	batches, total, err := s.uc.ListBatches(ctx, biz.BatchQuery{
		SeriesID:   req.GetSeriesId(),
		RevisionID: req.GetRevisionId(),
		Status:     req.GetStatus(),
		Page:       page,
		PageSize:   size,
	})
	if err != nil {
		return nil, toMediaError(err)
	}
	out := &v1.MediaBatchSet{
		Total:    int32(total),
		Page:     int32(page),
		PageSize: int32(size),
		Items:    make([]*v1.MediaBatch, 0, len(batches)),
	}
	for _, batch := range batches {
		out.Items = append(out.Items, toMediaBatchReply(&batch))
	}
	return out, nil
}

// GetBatch returns one batch with the first page of its items.
func (s *AdminMediaService) GetBatch(ctx context.Context, req *v1.GetMediaBatchRequest) (*v1.MediaBatchDetail, error) {
	batch, err := s.uc.GetBatch(ctx, req.GetId())
	if err != nil {
		return nil, toMediaError(err)
	}
	page, size := normalizePage(1, 20)
	items, total, err := s.uc.ListItems(ctx, biz.ItemQuery{BatchID: batch.ID, Page: page, PageSize: size})
	if err != nil {
		return nil, toMediaError(err)
	}
	out := &v1.MediaBatchDetail{Batch: toMediaBatchReply(batch), ItemTotal: int32(total)}
	for _, item := range items {
		out.Items = append(out.Items, toMediaItemReply(item))
	}
	return out, nil
}

// ListItems returns a page of the episode tasks of a batch.
func (s *AdminMediaService) ListItems(ctx context.Context, req *v1.ListMediaItemsRequest) (*v1.MediaItemSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	filter := mediaItemFilter(req.GetStatus(), req.GetStage())
	if !knownItemStatus(filter) {
		return nil, kratoserrors.BadRequest("INVALID_FILTER", "未知的任务状态")
	}
	items, total, err := s.uc.ListItems(ctx, biz.ItemQuery{
		BatchID:  req.GetId(),
		Status:   filter,
		Page:     page,
		PageSize: size,
	})
	if err != nil {
		return nil, toMediaError(err)
	}
	out := &v1.MediaItemSet{Total: int32(total), Page: int32(page), PageSize: int32(size)}
	for _, item := range items {
		out.Items = append(out.Items, toMediaItemReply(item))
	}
	return out, nil
}

// RetryFailures requeues the failed episode tasks of a batch.
func (s *AdminMediaService) RetryFailures(ctx context.Context, req *v1.RetryMediaBatchRequest) (*v1.MediaBatch, error) {
	batch, err := s.uc.RetryFailures(ctx, req.GetId(), int(req.GetExpectedVersion()), req.GetItemIds())
	if err != nil {
		return nil, toMediaError(err)
	}
	SetAccepted(ctx)
	return toMediaBatchReply(batch), nil
}

// CancelBatch requests the cancellation of a batch.
func (s *AdminMediaService) CancelBatch(ctx context.Context, req *v1.CancelMediaBatchRequest) (*v1.MediaBatch, error) {
	batch, err := s.uc.CancelBatch(ctx, req.GetId(), int(req.GetExpectedVersion()), req.GetReason())
	if err != nil {
		return nil, toMediaError(err)
	}
	SetAccepted(ctx)
	return toMediaBatchReply(batch), nil
}

// mediaItemFilter resolves the task filter: a task is always in one stage,
// so the documented stage filter and the status filter are the same field.
func mediaItemFilter(status string, stage string) string {
	if status != "" {
		return status
	}
	return stage
}

// knownBatchStatus reports whether the filter names a batch state.
func knownBatchStatus(status string) bool {
	switch biz.MediaBatchStatus(status) {
	case "", biz.MediaBatchQueued, biz.MediaBatchRunning, biz.MediaBatchWaiting,
		biz.MediaBatchPartiallyFailed, biz.MediaBatchFailed, biz.MediaBatchSucceeded,
		biz.MediaBatchCancelling, biz.MediaBatchCancelled, biz.MediaBatchPendingReview:
		return true
	}
	return false
}

// knownItemStatus reports whether the filter names a task stage.
func knownItemStatus(status string) bool {
	switch biz.MediaItemStatus(status) {
	case "", biz.MediaItemQueued, biz.MediaItemResolving, biz.MediaItemAcquiring,
		biz.MediaItemValidating, biz.MediaItemProcessing, biz.MediaItemUploading,
		biz.MediaItemVerifying, biz.MediaItemReady, biz.MediaItemWaitingRetry,
		biz.MediaItemFailed, biz.MediaItemCancelled:
		return true
	}
	return false
}

// mediaActor names the authenticated operator of the request. It is never
// read from the body: the session middleware is the only source, so a client
// cannot write someone else's name into created_by/confirmed_by.
func mediaActor(ctx context.Context) string {
	return ActorName(ctx)
}

func toMediaBatchReply(batch *biz.MediaBatch) *v1.MediaBatch {
	if batch == nil {
		return nil
	}
	return &v1.MediaBatch{
		Id:              batch.ID,
		SeriesId:        batch.SeriesID,
		RevisionId:      batch.RevisionID,
		SelectionHash:   batch.SelectionHash,
		ProfileVersion:  batch.ProfileVersion,
		InputKind:       batch.InputKind,
		Status:          string(batch.Status),
		CoverStatus:     batch.CoverStatus,
		ItemTotal:       int32(batch.ItemTotal),
		ItemReady:       int32(batch.ItemReady),
		ItemFailed:      int32(batch.ItemFailed),
		CancelRequested: batch.CancelRequested,
		Version:         int32(batch.Version),
		ErrorCode:       batch.ErrorCode,
		ErrorMessage:    batch.ErrorMessage,
		CreatedBy:       batch.CreatedBy,
		ReviewTaskId:    batch.ReviewTaskID,
		CreatedAt:       formatMediaTime(batch.CreatedAt),
		UpdatedAt:       formatMediaTime(batch.UpdatedAt),
	}
}

func toMediaItemReply(item biz.MediaItem) *v1.MediaItem {
	out := &v1.MediaItem{
		Id:           item.ID,
		BatchId:      item.BatchID,
		EpisodeId:    item.EpisodeID,
		ExternalId:   item.ExternalID,
		EpisodeNo:    int32(item.EpisodeNo),
		Status:       string(item.Status),
		Attempt:      int32(item.Attempt),
		Retryable:    item.Retryable,
		ErrorCode:    item.ErrorCode,
		ErrorMessage: item.ErrorMessage,
		Sha256:       item.SHA256,
		SizeBytes:    item.SizeBytes,
		Codec:        item.Codec,
		DurationMs:   item.DurationMS,
		Width:        int32(item.Width),
		Height:       int32(item.Height),
		AssetId:      item.AssetID,
		CreatedAt:    formatMediaTime(item.CreatedAt),
		UpdatedAt:    formatMediaTime(item.UpdatedAt),
	}
	if item.NextEligibleAt != nil {
		out.NextEligibleAt = formatMediaTime(*item.NextEligibleAt)
	}
	return out
}

func formatMediaTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// toMediaError maps a batch failure to the documented API error codes.
func toMediaError(err error) error {
	switch {
	case errors.Is(err, biz.ErrBatchNotFound):
		return kratoserrors.NotFound("BATCH_NOT_FOUND", "素材批次不存在")
	case errors.Is(err, biz.ErrRevisionNotFound):
		return kratoserrors.NotFound("REVISION_NOT_FOUND", "修订不存在")
	case errors.Is(err, biz.ErrNoSelectedEpisodes):
		return kratoserrors.BadRequest("NO_SELECTED_EPISODES", "修订没有选中的分集")
	case errors.Is(err, biz.ErrProfileRequired):
		return kratoserrors.BadRequest("REQUIRED_FIELDS_MISSING", "缺少处理配置版本")
	case errors.Is(err, biz.ErrVersionRequired):
		return kratoserrors.BadRequest("REQUIRED_FIELDS_MISSING", "缺少修订版本")
	case errors.Is(err, biz.ErrRevisionVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "修订版本已变化")
	case errors.Is(err, biz.ErrBatchVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "批次版本已变化")
	case errors.Is(err, biz.ErrRevisionNotEditable):
		return kratoserrors.Conflict("INPUT_NOT_READY", "修订当前状态不可提交素材处理")
	case errors.Is(err, biz.ErrRevisionNotOwned):
		return kratoserrors.Conflict("INPUT_NOT_READY", "修订已撤回或被替换，批次不再推进该修订")
	case errors.Is(err, biz.ErrBatchNotCancellable):
		return kratoserrors.Conflict("BATCH_NOT_CANCELLABLE", "批次当前状态不可取消")
	case errors.Is(err, biz.ErrBatchNotRetryable):
		return kratoserrors.Conflict("NO_RETRYABLE_ITEMS", "没有可重试的失败任务")
	case errors.Is(err, biz.ErrBatchAlreadyActive):
		return kratoserrors.Conflict("BATCH_ALREADY_ACTIVE", "批次已有进行中的处理")
	case errors.Is(err, biz.ErrItemLeaseLost):
		return kratoserrors.Conflict("VERSION_CONFLICT", "任务已被其他执行者接管")
	case errors.Is(err, biz.ErrGrantNotFound):
		return kratoserrors.BadRequest("AUTHORIZATION_REQUIRED", "必须选择有效的授权依据")
	case errors.Is(err, biz.ErrGrantNotApplicable):
		return kratoserrors.BadRequest("AUTHORIZATION_NOT_APPLICABLE", "授权依据不属于该作品")
	case errors.Is(err, biz.ErrGrantVersion):
		return kratoserrors.Conflict("AUTHORIZATION_CHANGED", "授权依据版本已变化，请重新选择")
	case errors.Is(err, biz.ErrGrantNotEffective), errors.Is(err, biz.ErrGrantPermission):
		return kratoserrors.Conflict("AUTHORIZATION_REQUIRED", "授权依据未生效、已过期、已撤销或缺少所需用途")
	case errors.Is(err, biz.ErrBatchAuthorizationInvalid):
		return kratoserrors.Conflict("AUTHORIZATION_INVALID", "冻结的授权依据已失效")
	}
	return kratoserrors.InternalServer("MEDIA_INTERNAL", "素材批次操作失败")
}
