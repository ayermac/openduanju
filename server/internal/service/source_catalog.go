package service

import (
	"context"
	"errors"
	"time"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminIngestionService exposes plans and persistent runs.
type AdminIngestionService struct {
	v1.UnimplementedAdminIngestionServiceServer
	uc *biz.SourceCatalog
}

// NewAdminIngestionService builds the ingestion service.
func NewAdminIngestionService(uc *biz.SourceCatalog) *AdminIngestionService {
	return &AdminIngestionService{uc: uc}
}

// ListPlans returns configured plans.
func (s *AdminIngestionService) ListPlans(ctx context.Context, _ *emptypb.Empty) (*v1.PlanSet, error) {
	plans, err := s.uc.ListPlans(ctx)
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.PlanSet{Items: make([]*v1.Plan, 0, len(plans))}
	for _, plan := range plans {
		out.Items = append(out.Items, toPlanReply(plan))
	}
	return out, nil
}

// CreatePlan stores a reviewed plan.
func (s *AdminIngestionService) CreatePlan(ctx context.Context, req *v1.CreatePlanRequest) (*v1.Plan, error) {
	plan, err := s.uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: req.GetCode(), Name: req.GetName(), SourceCode: req.GetSourceCode(), Channel: req.GetChannel(),
		PageStart: int(req.GetPageStart()), PageEnd: int(req.GetPageEnd()),
		RequestBudget: int(req.GetRequestBudget()), ItemBudget: int(req.GetItemBudget()),
		FetchDetails: req.GetFetchDetails(), FetchEpisodes: req.GetFetchEpisodes(), Enabled: req.GetEnabled(),
		RatePerMinute: int(req.GetRatePerMinute()),
		Cron:          req.GetCron(), Timezone: req.GetTimezone(),
	})
	if err != nil {
		return nil, toKratosError(err)
	}
	return toPlanReply(*plan), nil
}

// UpdatePlan edits a plan with a version check.
func (s *AdminIngestionService) UpdatePlan(ctx context.Context, req *v1.UpdatePlanRequest) (*v1.Plan, error) {
	plan, err := s.uc.UpdatePlan(ctx, req.GetId(), int(req.GetExpectedVersion()), biz.IngestionPlan{
		Name: req.GetName(), PageStart: int(req.GetPageStart()), PageEnd: int(req.GetPageEnd()),
		RequestBudget: int(req.GetRequestBudget()), ItemBudget: int(req.GetItemBudget()),
		RatePerMinute: int(req.GetRatePerMinute()), FetchDetails: req.GetFetchDetails(),
		FetchEpisodes: req.GetFetchEpisodes(), Enabled: req.GetEnabled(),
		Cron: req.GetCron(), Timezone: req.GetTimezone(),
	})
	if err != nil {
		return nil, toKratosError(err)
	}
	return toPlanReply(*plan), nil
}

// TriggerPlan queues a run and returns immediately.
func (s *AdminIngestionService) TriggerPlan(ctx context.Context, req *v1.TriggerPlanRequest) (*v1.Run, error) {
	run, err := s.uc.TriggerPlan(ctx, req.GetId(), int(req.GetExpectedVersion()))
	if err != nil {
		return nil, toKratosError(err)
	}
	return toRunReply(*run), nil
}

// ListRuns returns run history.
func (s *AdminIngestionService) ListRuns(ctx context.Context, req *v1.ListRunsRequest) (*v1.RunSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	runs, total, err := s.uc.ListRuns(ctx, biz.RunQuery{PlanID: req.GetPlanId(), Status: req.GetStatus(), Page: page, PageSize: size})
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.RunSet{Total: int32(total), Page: int32(page), PageSize: int32(size), Items: make([]*v1.Run, 0, len(runs))}
	for _, run := range runs {
		out.Items = append(out.Items, toRunReply(run))
	}
	return out, nil
}

// GetRun returns one run.
func (s *AdminIngestionService) GetRun(ctx context.Context, req *v1.GetRunRequest) (*v1.Run, error) {
	run, err := s.uc.GetRun(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	return toRunReply(*run), nil
}

// RetryRun requeues a run and optionally extends its budget.
func (s *AdminIngestionService) RetryRun(ctx context.Context, req *v1.RetryRunRequest) (*v1.Run, error) {
	run, err := s.uc.RetryRun(ctx, req.GetId(), int(req.GetExpectedVersion()), int(req.GetAdditionalRequests()), int(req.GetAdditionalItems()), req.GetReason())
	if err != nil {
		return nil, toKratosError(err)
	}
	return toRunReply(*run), nil
}

// CancelRun requests cooperative cancellation.
func (s *AdminIngestionService) CancelRun(ctx context.Context, req *v1.CancelRunRequest) (*v1.Run, error) {
	run, err := s.uc.CancelRun(ctx, req.GetId(), int(req.GetExpectedVersion()))
	if err != nil {
		return nil, toKratosError(err)
	}
	return toRunReply(*run), nil
}

// AdminCandidateService reads local snapshots.
type AdminCandidateService struct {
	v1.UnimplementedAdminCandidateServiceServer
	uc *biz.SourceCatalog
}

// NewAdminCandidateService builds the candidate service.
func NewAdminCandidateService(uc *biz.SourceCatalog) *AdminCandidateService {
	return &AdminCandidateService{uc: uc}
}

// ListSourceEntries lists the web entry points a plan may use. The console
// reads them instead of guessing channel names.
func (s *AdminCandidateService) ListSourceEntries(ctx context.Context, _ *v1.ListSourceEntriesRequest) (*v1.SourceEntrySet, error) {
	entries := s.uc.SourceEntries()
	out := &v1.SourceEntrySet{Items: make([]*v1.SourceEntry, 0, len(entries))}
	for _, entry := range entries {
		out.Items = append(out.Items, &v1.SourceEntry{
			SourceCode: entry.SourceCode, Kind: entry.Kind, Channel: entry.Channel, Label: entry.Label,
			AdapterKey: entry.AdapterKey, Description: entry.Description,
		})
	}
	return out, nil
}

// ListRunCandidates returns the rows one collection run produced, with their
// stage. It reads local snapshots only and never creates content.
func (s *AdminCandidateService) ListRunCandidates(ctx context.Context, req *v1.ListRunCandidatesRequest) (*v1.CandidateSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	runID := req.GetId()
	result, err := s.uc.ListCandidates(ctx, biz.CandidateQuery{RunID: &runID, Page: page, PageSize: size})
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.CandidateSet{Total: int32(result.Total), Page: int32(result.Page), PageSize: int32(result.PageSize), Items: make([]*v1.Candidate, 0, len(result.Items))}
	for _, item := range result.Items {
		out.Items = append(out.Items, toCandidateReply(item))
	}
	return out, nil
}

// ListRunItems returns the rows one run produced, with the result of each
// attempt. The relation is stored with the run, so a later run cannot hide
// the old rows.
func (s *AdminCandidateService) ListRunItems(ctx context.Context, req *v1.ListRunItemsRequest) (*v1.RunItemSet, error) {
	// A run that does not exist is not the same as a run without rows.
	if _, err := s.uc.GetRun(ctx, req.GetId()); err != nil {
		if errors.Is(err, biz.ErrRunNotFound) {
			return nil, kratoserrors.NotFound("RUN_NOT_FOUND", "运行不存在")
		}
		return nil, toKratosError(err)
	}
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	result, err := s.uc.ListRunItems(ctx, req.GetId(), page, size)
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.RunItemSet{Total: int32(result.Total), Page: int32(page), PageSize: int32(size), Items: make([]*v1.RunItem, 0, len(result.Items))}
	for _, item := range result.Items {
		out.Items = append(out.Items, &v1.RunItem{
			Id: item.ID, RunId: item.RunID, CandidateId: item.CandidateID,
			CandidateExternalId: item.CandidateExternalID, Stage: item.Stage, Result: item.Result,
			ErrorCode: item.ErrorCode, ErrorMessage: item.ErrorMessage, Attempt: int32(item.Attempt),
			CreatedAt: formatMediaTime(item.CreatedAt),
		})
	}
	return out, nil
}

// ListCandidates returns one local page.
func (s *AdminCandidateService) ListCandidates(ctx context.Context, req *v1.ListCandidatesRequest) (*v1.CandidateSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	query := biz.CandidateQuery{
		SourceCode: req.GetSourceCode(), Keyword: req.GetQ(), Completeness: req.GetCompleteness(),
		Page: page, PageSize: size,
	}
	if req.Imported != nil {
		value := req.GetImported()
		query.Imported = &value
	}
	result, err := s.uc.ListCandidates(ctx, query)
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.CandidateSet{Total: int32(result.Total), Page: int32(result.Page), PageSize: int32(result.PageSize), Items: make([]*v1.Candidate, 0, len(result.Items))}
	for _, item := range result.Items {
		out.Items = append(out.Items, toCandidateReply(item))
	}
	return out, nil
}

// GetCandidate returns one local candidate with its episode snapshot.
func (s *AdminCandidateService) GetCandidate(ctx context.Context, req *v1.GetCandidateRequest) (*v1.CandidateDetail, error) {
	candidate, err := s.uc.GetCandidate(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	episodes, err := s.uc.ListCandidateEpisodes(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.CandidateDetail{Candidate: toCandidateReply(*candidate)}
	for _, episode := range episodes {
		out.Episodes = append(out.Episodes, &v1.SourceEpisode{ExternalId: episode.ExternalID, Number: int32(episode.Number), Title: episode.Title})
	}
	return out, nil
}

// ListCandidateEpisodes returns the snapshot episodes.
func (s *AdminCandidateService) ListCandidateEpisodes(ctx context.Context, req *v1.ListCandidateEpisodesRequest) (*v1.SourceEpisodeSet, error) {
	episodes, err := s.uc.ListCandidateEpisodes(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.SourceEpisodeSet{Total: int32(len(episodes)), Items: make([]*v1.SourceEpisode, 0, len(episodes))}
	for _, episode := range episodes {
		out.Items = append(out.Items, &v1.SourceEpisode{ExternalId: episode.ExternalID, Number: int32(episode.Number), Title: episode.Title})
	}
	return out, nil
}

// AdminContentService creates local series and edits draft selection.
type AdminContentService struct {
	v1.UnimplementedAdminContentServiceServer
	uc *biz.SourceCatalog
}

// NewAdminContentService builds the content service.
func NewAdminContentService(uc *biz.SourceCatalog) *AdminContentService {
	return &AdminContentService{uc: uc}
}

// CreateSeries copies a local snapshot; it never calls the source.
func (s *AdminContentService) CreateSeries(ctx context.Context, req *v1.CreateSeriesRequest) (*v1.SeriesDetail, error) {
	switch req.GetMode() {
	case "source":
		view, err := s.uc.CreateSeries(ctx, req.GetCandidateId())
		if err != nil {
			return nil, toKratosError(err)
		}
		return toSeriesReply(view), nil
	case "manual":
		// A manual work declares itself: no candidate, no provider identity.
		// The draft it opens is the same draft the import path opens, so the
		// upload, processing, review and publication steps do not branch.
		episodes := make([]biz.ManualEpisodeInput, 0, len(req.GetEpisodes()))
		for _, episode := range req.GetEpisodes() {
			episodes = append(episodes, biz.ManualEpisodeInput{Number: int(episode.GetOriginalNumber())})
		}
		actor := ""
		if current := ActorFrom(ctx); current != nil {
			actor = current.Staff.Username
		}
		view, err := s.uc.CreateManualSeries(ctx, biz.ManualSeriesInput{
			Title:       req.GetTitle(),
			Description: req.GetDescription(),
			Episodes:    episodes,
			RequestID:   req.GetRequestId(),
			Actor:       actor,
		})
		if err != nil {
			return nil, toKratosError(err)
		}
		return toSeriesReply(view), nil
	default:
		return nil, kratoserrors.BadRequest("UNSUPPORTED_MODE", "mode 只支持 source 或 manual")
	}
}

// GetSeries returns a local series and draft revision.
// ListSeries returns one page of the content list.
func (s *AdminContentService) ListSeries(ctx context.Context, req *v1.ListSeriesRequest) (*v1.SeriesSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	items, total, err := s.uc.ListSeries(ctx, biz.ContentSeriesQuery{
		Keyword: req.GetQ(), Status: req.GetStatus(), Page: page, PageSize: size,
	})
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.SeriesSet{Total: int32(total), Page: int32(page), PageSize: int32(size), Items: make([]*v1.SeriesItem, 0, len(items))}
	for _, item := range items {
		out.Items = append(out.Items, &v1.SeriesItem{
			SeriesId: item.SeriesID, Title: item.Title, Description: item.Description,
			RevisionId: item.RevisionID, RevisionNo: int32(item.RevisionNo),
			RevisionState: item.RevisionState, Status: item.Status, Version: int32(item.Version),
			UpdatedAt: formatMediaTime(item.UpdatedAt), EpisodeCount: int32(item.EpisodeCount),
			Published: item.Published,
		})
	}
	return out, nil
}

// CreateRevision creates a new working draft of one series. A series that
// already has a working draft answers a conflict with the existing draft, so
// nothing is replaced silently.
func (s *AdminContentService) CreateRevision(ctx context.Context, req *v1.CreateRevisionRequest) (*v1.SeriesDetail, error) {
	detail, err := s.uc.CreateRevision(ctx, req.GetId(), req.GetBaseRevisionId(), req.GetReplaceDraftRevisionId(), int(req.GetReplaceDraftVersion()))
	if err != nil {
		if errors.Is(err, biz.ErrDraftExists) {
			return nil, kratoserrors.Conflict("DRAFT_EXISTS", "该作品已有工作草稿，请在草稿上继续修改，或显式替换该草稿")
		}
		return nil, toKratosError(err)
	}
	return toSeriesReply(detail), nil
}

// UpdateRevision edits the working draft of one series.
func (s *AdminContentService) UpdateRevision(ctx context.Context, req *v1.UpdateRevisionRequest) (*v1.SeriesDetail, error) {
	detail, err := s.uc.UpdateRevision(ctx, req.GetId(), req.GetRevisionId(), int(req.GetExpectedVersion()), req.Title != nil, req.GetTitle(), req.Description != nil, req.GetDescription())
	if err != nil {
		if errors.Is(err, biz.ErrRevisionConflict) {
			return nil, kratoserrors.Conflict("REVISION_CONFLICT", "修订已变化或不可编辑，请刷新后重试")
		}
		return nil, toKratosError(err)
	}
	return toSeriesReply(detail), nil
}

func (s *AdminContentService) GetSeries(ctx context.Context, req *v1.GetSeriesRequest) (*v1.SeriesDetail, error) {
	view, err := s.uc.GetSeries(ctx, req.GetId())
	if err != nil {
		return nil, toKratosError(err)
	}
	return toSeriesReply(view), nil
}

// ListRevisionEpisodes returns a page of the draft episode snapshot.
func (s *AdminContentService) ListRevisionEpisodes(ctx context.Context, req *v1.ListRevisionEpisodesRequest) (*v1.RevisionEpisodeSet, error) {
	page, size := normalizePage(int(req.GetPage()), int(req.GetPageSize()))
	episodes, total, err := s.uc.ListRevisionEpisodes(ctx, req.GetId(), page, size)
	if err != nil {
		return nil, toKratosError(err)
	}
	out := &v1.RevisionEpisodeSet{Total: int32(total), Items: make([]*v1.RevisionEpisode, 0, len(episodes))}
	for _, episode := range episodes {
		out.Items = append(out.Items, &v1.RevisionEpisode{Id: episode.ID, ExternalId: episode.ExternalID, Number: int32(episode.Number), Selected: episode.Selected})
	}
	return out, nil
}

// SetSelection replaces the draft selection.
func (s *AdminContentService) SetSelection(ctx context.Context, req *v1.SetSelectionRequest) (*v1.RevisionSelection, error) {
	revision, err := s.uc.SetSelection(ctx, req.GetId(), int(req.GetExpectedVersion()), req.GetSelectionMode(), req.GetEpisodeIds())
	if err != nil {
		return nil, toKratosError(err)
	}
	return &v1.RevisionSelection{Revision: toRevisionReply(*revision), SelectedCount: int32(revision.SelectedCount)}, nil
}

func normalizePage(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func toPlanReply(plan biz.IngestionPlan) *v1.Plan {
	out := &v1.Plan{
		Id: plan.ID, Code: plan.Code, Name: plan.Name, SourceCode: plan.SourceCode, Channel: plan.Channel,
		PageStart: int32(plan.PageStart), PageEnd: int32(plan.PageEnd),
		RequestBudget: int32(plan.RequestBudget), ItemBudget: int32(plan.ItemBudget),
		FetchDetails: plan.FetchDetails, FetchEpisodes: plan.FetchEpisodes, Enabled: plan.Enabled, Version: int32(plan.Version),
		RatePerMinute: int32(plan.RatePerMinute),
		Cron:          plan.Cron, Timezone: plan.Timezone,
	}
	if plan.NextRunAt != nil {
		out.NextRunAt = plan.NextRunAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toRunReply(run biz.IngestionRun) *v1.Run {
	out := &v1.Run{
		Id: run.ID, PlanId: run.PlanID, Kind: run.Kind, CandidateId: run.CandidateID,
		SourceCode: run.SourceCode, Channel: run.Channel, PageStart: int32(run.PageStart), PageEnd: int32(run.PageEnd),
		Status: run.Status, RequestBudget: int32(run.RequestBudget), ItemBudget: int32(run.ItemBudget),
		RequestsUsed: int32(run.RequestsUsed), PageCursor: int32(run.PageCursor), PagesFetched: int32(run.PagesFetched),
		ItemsSeen: int32(run.ItemsSeen), ItemsUpserted: int32(run.ItemsUpserted),
		DetailFetched: int32(run.DetailFetched), EpisodesFetched: int32(run.EpisodesFetched),
		CancelRequested: run.CancelRequested, ErrorCode: run.ErrorCode, ErrorReason: run.ErrorReason, Version: int32(run.Version),
		RatePerMinute: int32(run.RatePerMinute),
		StartedAt:     run.StartedAt.UTC().Format(time.RFC3339),
	}
	if run.FinishedAt != nil {
		out.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339)
	}
	if run.NextRequestAt != nil {
		out.NextRequestAt = run.NextRequestAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toCandidateReply(item biz.Candidate) *v1.Candidate {
	out := &v1.Candidate{
		Id: item.ID, SourceCode: item.SourceCode, Channel: item.Channel, ExternalId: item.ExternalID,
		Title: item.Title, CoverUrl: item.CoverURL, Description: item.Description, Tags: item.Tags,
		EpisodeCount: int32(item.EpisodeCount), OpenEpisodeCount: int32(item.OpenEpisodeCount), Rank: int32(item.Rank),
		Metrics: item.Metrics, ContentHash: item.ContentHash, Completeness: item.Completeness, SeriesId: item.SeriesID,
		FirstSeenAt: item.FirstSeenAt.UTC().Format(time.RFC3339), LastSeenAt: item.LastSeenAt.UTC().Format(time.RFC3339),
	}
	if item.DetailFetchedAt != nil {
		out.DetailFetchedAt = item.DetailFetchedAt.UTC().Format(time.RFC3339)
	}
	if item.EpisodesFetchedAt != nil {
		out.EpisodesFetchedAt = item.EpisodesFetchedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toRevisionReply(revision biz.ContentRevision) *v1.Revision {
	return &v1.Revision{
		Id: revision.ID, SeriesId: revision.SeriesID, RevisionNo: int32(revision.RevisionNo),
		State: revision.State, Version: int32(revision.Version), Title: revision.Title,
		Description: revision.Description, CoverUrl: revision.CoverURL, Tags: revision.Tags,
		SelectedCount: int32(revision.SelectedCount),
	}
}

func toSeriesReply(view *biz.SeriesView) *v1.SeriesDetail {
	out := &v1.SeriesDetail{
		Id: view.Series.ID, SourceCode: view.Series.SourceCode, ExternalId: view.Series.ExternalID,
		CandidateId: view.Series.CandidateID, Status: view.Series.Status, Version: int32(view.Series.Version),
		CurrentDraftRevisionId: view.Series.CurrentDraftRevisionID, Draft: toRevisionReply(*view.Revision),
		Episodes: make([]*v1.RevisionEpisode, 0, len(view.Episodes)),
	}
	if view.PublishedRevision != nil {
		out.PublishedRevision = toRevisionReply(*view.PublishedRevision)
		out.PublishedRevisionId = view.PublishedRevision.ID
	}
	for _, episode := range view.Episodes {
		out.Episodes = append(out.Episodes, &v1.RevisionEpisode{Id: episode.ID, ExternalId: episode.ExternalID, Number: int32(episode.Number), Selected: episode.Selected})
	}
	return out
}

func toKratosError(err error) error {
	if mapped := sourceError(err); mapped != nil {
		return mapped
	}
	switch {
	case errors.Is(err, biz.ErrPlanCodeExists):
		return kratoserrors.Conflict("PLAN_CODE_EXISTS", "该计划代号已存在，请换一个")
	case errors.Is(err, biz.ErrPlanNotFound):
		return kratoserrors.NotFound("PLAN_NOT_FOUND", "采集计划不存在")
	case errors.Is(err, biz.ErrRunNotFound):
		return kratoserrors.NotFound("RUN_NOT_FOUND", "采集运行不存在")
	case errors.Is(err, biz.ErrRunNotCancellable):
		return kratoserrors.Conflict("RUN_NOT_CANCELLABLE", "运行不可取消")
	case errors.Is(err, biz.ErrRunAlreadyActive):
		return kratoserrors.Conflict("RUN_ALREADY_ACTIVE", "该计划已有进行中的运行")
	case errors.Is(err, biz.ErrCandidateNotFound):
		return kratoserrors.NotFound("CANDIDATE_NOT_FOUND", "候选不存在")
	case errors.Is(err, biz.ErrCandidateIncomplete):
		return kratoserrors.Conflict("CANDIDATE_INCOMPLETE", "候选快照未补齐，已创建补全任务，请在运行列表查看")
	case errors.Is(err, biz.ErrCandidateImported):
		return kratoserrors.Conflict("CANDIDATE_ALREADY_IMPORTED", "候选已导入本地剧集")
	case errors.Is(err, biz.ErrSeriesNotFound):
		return kratoserrors.NotFound("SERIES_NOT_FOUND", "本地剧集不存在")
	case errors.Is(err, biz.ErrManualTitleRequired):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "作品标题必填，且不超过120字")
	case errors.Is(err, biz.ErrManualDescription):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "简介不能超过5000字")
	case errors.Is(err, biz.ErrManualEpisodes):
		return kratoserrors.BadRequest("VALIDATION_FAILED", "至少需要一集，且不超过500集")
	case errors.Is(err, biz.ErrManualEpisodeNo):
		return kratoserrors.BadRequest("INVALID_EPISODE_NUMBER", "分集序号需为1~9999")
	case errors.Is(err, biz.ErrDuplicateEpisodeNo):
		return kratoserrors.Conflict("DUPLICATE_EPISODE_NUMBER", "分集序号重复")
	case errors.Is(err, biz.ErrIdempotencyConflict):
		return kratoserrors.Conflict("IDEMPOTENCY_CONFLICT", "同一请求号已用于另一次提交")
	case errors.Is(err, biz.ErrRevisionNotFound):
		return kratoserrors.NotFound("REVISION_NOT_FOUND", "修订不存在")
	case errors.Is(err, biz.ErrRevisionVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "修订版本已变化")
	case errors.Is(err, biz.ErrSelectionMismatch):
		return kratoserrors.BadRequest("EPISODE_NOT_IN_SERIES", "选集包含未知或重复分集")
	case errors.Is(err, biz.ErrPlanPageRange):
		return kratoserrors.BadRequest("INVALID_PAGE_RANGE", "页码范围无效")
	case errors.Is(err, biz.ErrPlanBudget):
		return kratoserrors.BadRequest("INVALID_BUDGET", "预算无效")
	case errors.Is(err, biz.ErrPlanVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "计划版本已变化")
	case errors.Is(err, biz.ErrRunVersion):
		return kratoserrors.Conflict("VERSION_CONFLICT", "运行版本已变化")
	case errors.Is(err, biz.ErrRunNotRetryable):
		return kratoserrors.Conflict("RUN_NOT_RETRYABLE", "运行不可重试")
	case errors.Is(err, biz.ErrBudgetExtension):
		return kratoserrors.Conflict("BUDGET_EXTENSION_REQUIRED", "预算耗尽，需追加请求预算")
	case errors.Is(err, biz.ErrPlanSchedule):
		return kratoserrors.BadRequest("INVALID_CRON", "定时表达式或时区无效")
	}
	code, ok := biz.SourceErrorCodeOf(err)
	if !ok {
		return kratoserrors.InternalServer("SOURCE_INTERNAL", "来源操作失败")
	}
	switch code {
	case biz.SourceCodeRateLimited:
		return kratoserrors.TooManyRequests(string(code), "来源限频，请稍后重试")
	case biz.SourceCodeNotFound:
		return kratoserrors.NotFound(string(code), "来源页面不存在")
	case biz.SourceCodeRejected, biz.SourceCodeParseFailed:
		return kratoserrors.ServiceUnavailable(string(code), "来源拒绝或页面结构异常")
	case biz.SourceCodeUnavailable:
		return kratoserrors.ServiceUnavailable(string(code), "来源暂不可用")
	case biz.SourceCodeConfigInvalid:
		return kratoserrors.InternalServer(string(code), "来源配置无效")
	default:
		return kratoserrors.InternalServer(string(code), "来源操作失败")
	}
}
