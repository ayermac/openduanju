package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Stable source-catalog errors. Err* values are matched with errors.Is.
var (
	ErrPlanNotFound        = errors.New("ingestion plan not found")
	ErrRunNotFound         = errors.New("ingestion run not found")
	ErrRunNotCancellable   = errors.New("ingestion run is not cancellable")
	ErrRunAlreadyActive    = errors.New("an ingestion run is already active")
	ErrCandidateNotFound   = errors.New("candidate not found")
	ErrCandidateIncomplete = errors.New("candidate snapshot is incomplete")
	ErrCandidateImported   = errors.New("candidate already has a local series")
	ErrSeriesNotFound      = errors.New("series not found")
	ErrRevisionNotFound    = errors.New("revision not found")
	ErrRevisionVersion     = errors.New("revision version conflict")
	ErrSelectionMismatch   = errors.New("selection contains unknown or duplicate episode ids")
	ErrPlanPageRange       = errors.New("invalid page range")
	ErrPlanBudget          = errors.New("invalid budget")
	ErrRunLeaseLost        = errors.New("ingestion run lease was lost")
	ErrRunNotResumable     = errors.New("ingestion run cannot be resumed")
	ErrRunNotRetryable     = errors.New("ingestion run cannot be retried")
	ErrBudgetExtension     = errors.New("budget extension is required")
	ErrPlanVersion         = errors.New("ingestion plan version conflict")
	ErrRunVersion          = errors.New("ingestion run version conflict")
	ErrPlanSchedule        = errors.New("invalid ingestion plan schedule")
	ErrPlanCodeExists      = errors.New("an ingestion plan with this code already exists")
)

// Ingest status values persisted for a run.
const (
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusWaiting   = "waiting"
	RunStatusBudget    = "budget_exhausted"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

// Run kinds.
const (
	RunKindRanking    = "ranking"
	RunKindCompletion = "completion"
)

// Completeness values stored on a candidate snapshot.
const (
	CompletenessSummary  = "summary"
	CompletenessDetail   = "detail"
	CompletenessComplete = "complete"
)

// IngestionPlan is one reviewed collection configuration.
type IngestionPlan struct {
	ID            int64
	Code          string
	Name          string
	SourceCode    string
	Channel       string
	PageStart     int
	PageEnd       int
	RequestBudget int
	ItemBudget    int
	RatePerMinute int
	FetchDetails  bool
	FetchEpisodes bool
	Enabled       bool
	Version       int
	// Cron is a 5-field cron expression; empty keeps the plan manual.
	Cron string
	// Timezone is the IANA zone the cron expression is evaluated in.
	Timezone string
	// NextRunAt is the next scheduled fire time in UTC.
	NextRunAt *time.Time
}

// IngestionRun is a persistent task with checkpoint and lease metadata.
type IngestionRun struct {
	ID            int64
	PlanID        int64
	Kind          string
	CandidateID   int64
	SourceCode    string
	Channel       string
	PageStart     int
	PageEnd       int
	Status        string
	RequestBudget int
	ItemBudget    int
	RequestsUsed  int
	RatePerMinute int
	// ChannelBaseURL and ChannelTimeout are the channel configuration the run
	// snapshot froze; the worker uses them for the outbound calls.
	ChannelBaseURL  string
	ChannelTimeout  time.Duration
	RateWindowStart *time.Time
	RateWindowCount int
	PageCursor      int
	PagesFetched    int
	ItemsSeen       int
	ItemsUpserted   int
	DetailFetched   int
	EpisodesFetched int
	CancelRequested bool
	FetchDetails    bool
	FetchEpisodes   bool
	ErrorCode       string
	ErrorReason     string
	// TraceID is the identity of the request that queued the run. The worker
	// logs it with the run, the page and the item it is working on, so a task
	// resumed after a restart still points back at its trigger.
	TraceID       string
	Version       int
	StartedAt     time.Time
	FinishedAt    *time.Time
	NextRequestAt *time.Time
}

// IngestionRunItem is one row one collection run produced, with the result
// of that attempt. It is stored with the run, so a later run cannot erase it.
type IngestionRunItem struct {
	ID                  int64
	RunID               int64
	CandidateID         int64
	CandidateExternalID string
	// Stage is the step of the run: summary, detail or episodes.
	Stage string
	// Result is success or failed.
	Result       string
	ErrorCode    string
	ErrorMessage string
	Attempt      int
	CreatedAt    time.Time
}

// RunItemPage is one page of the rows of a run.
type RunItemPage struct {
	Items    []IngestionRunItem
	Total    int
	Page     int
	PageSize int
}

// The stages of a collection run.
const (
	RunItemStageSummary  = "summary"
	RunItemStageDetail   = "detail"
	RunItemStageEpisodes = "episodes"
)

// The results of one attempt.
const (
	RunItemResultSuccess = "success"
	RunItemResultFailed  = "failed"
)

// CandidatePage is one local page of candidate snapshots.
type CandidatePage struct {
	Items    []Candidate
	Total    int
	Page     int
	PageSize int
}

// Candidate is a local snapshot; it never carries a playback URL.
type Candidate struct {
	ID                int64
	SourceCode        string
	Channel           string
	ExternalID        string
	Title             string
	CoverURL          string
	Description       string
	Tags              []string
	EpisodeCount      int
	OpenEpisodeCount  int
	Rank              int
	Metrics           map[string]string
	ContentHash       string
	Completeness      string
	DetailFetchedAt   *time.Time
	EpisodesFetchedAt *time.Time
	SeriesID          int64
	LastRunID         int64
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
}

// CandidateEpisode is one real episode identity in the candidate snapshot.
type CandidateEpisode struct {
	ExternalID string
	Number     int
	Title      string
}

// ContentSeries is the local series.
// ErrDraftExists marks a series that already has a working draft. The
// caller has to name that draft to replace it; nothing is overwritten
// silently.
var ErrDraftExists = errors.New("the series already has a working draft")

// ErrRevisionConflict marks a write that lost the version check or that
// tried to change a revision which is no longer a draft.
var ErrRevisionConflict = errors.New("revision conflict")

// ContentSeriesItem is one row of the content list.
type ContentSeriesItem struct {
	SeriesID      int64
	Title         string
	Description   string
	RevisionID    int64
	RevisionNo    int
	RevisionState string
	Status        string
	Version       int
	EpisodeCount  int
	Published     bool
	UpdatedAt     time.Time
}

// ContentSeriesQuery filters the content list.
type ContentSeriesQuery struct {
	Keyword  string
	Status   string
	Page     int
	PageSize int
}

type ContentSeries struct {
	ID                     int64
	SourceCode             string
	ExternalID             string
	CandidateID            int64
	Title                  string
	Description            string
	CoverURL               string
	Status                 string
	CurrentDraftRevisionID int64
	PublishedRevisionID    int64
	Version                int
}

// ContentRevision is an editable snapshot of a series.
type ContentRevision struct {
	ID            int64
	SeriesID      int64
	RevisionNo    int
	State         string
	Version       int
	Title         string
	Description   string
	CoverURL      string
	Tags          []string
	SelectedCount int
}

// RevisionEpisode is one selectable episode.
type RevisionEpisode struct {
	// ID is the revision episode identity a manual upload binds to.
	ID         int64
	ExternalID string
	Number     int
	Selected   bool
}

// SeriesView bundles the series, its draft revision and episodes.
type SeriesView struct {
	Series   *ContentSeries
	Revision *ContentRevision
	// PublishedRevision is the revision on the shelf, if there is one.
	PublishedRevision *ContentRevision
	Episodes          []RevisionEpisode
}

// CandidateQuery filters local candidates.
type CandidateQuery struct {
	SourceCode   string
	Keyword      string
	Completeness string
	Imported     *bool
	// RunID selects the candidates one collection run produced.
	RunID    *int64
	Page     int
	PageSize int
}

// RequestDecision is the outcome of reserving one outbound request.
type RequestDecision string

const (
	RequestAllowed   RequestDecision = "allowed"
	RequestRateLimit RequestDecision = "rate_limited"
	RequestBudget    RequestDecision = "budget_exhausted"
	RequestCancelled RequestDecision = "cancelled"
)

// RunQuery filters local runs.
type RunQuery struct {
	PlanID   int64
	Status   string
	Page     int
	PageSize int
}

// SourceCatalogRepo persists plans, runs and local snapshots. Implementations
// must keep selection and candidate reads local; only an ingestion worker may
// call the web adapter.
type SourceCatalogRepo interface {
	ListPlans(ctx context.Context) ([]IngestionPlan, error)
	GetPlan(ctx context.Context, id int64) (*IngestionPlan, error)
	CreatePlan(ctx context.Context, plan IngestionPlan) (*IngestionPlan, error)
	UpdatePlan(ctx context.Context, id int64, expectedVersion int, plan IngestionPlan) (*IngestionPlan, error)
	DuePlans(ctx context.Context, now time.Time, limit int) ([]IngestionPlan, error)
	AdvancePlanSchedule(ctx context.Context, planID int64, expectedNextRunAt, nextRunAt time.Time) (bool, error)
	CreateRun(ctx context.Context, plan *IngestionPlan, kind string, candidateID int64) (*IngestionRun, error)
	ActiveRun(ctx context.Context, planID int64) (*IngestionRun, error)
	ClaimRun(ctx context.Context, worker string, lease time.Duration) (*IngestionRun, error)
	GetRun(ctx context.Context, id int64) (*IngestionRun, error)
	ListRuns(ctx context.Context, query RunQuery) ([]IngestionRun, int, error)
	RequestCancel(ctx context.Context, id int64, expectedVersion int) error
	ConsumeRequest(ctx context.Context, runID int64, worker string, version int, now time.Time, lease time.Duration) (RequestDecision, *time.Time, error)
	RenewLease(ctx context.Context, runID int64, worker string, version int, lease time.Duration) error
	AddProgress(ctx context.Context, runID int64, worker string, version int, seen, upserted, details, episodes int) error
	CompletePage(ctx context.Context, runID int64, worker string, version int, page, seen int, nextRequestAt time.Time) error
	AddBudget(ctx context.Context, runID int64, extra int) (*IngestionRun, error)
	RetryRun(ctx context.Context, runID int64, expectedVersion, addRequests, addItems int, reason string) (*IngestionRun, error)
	MarkBudgetExhausted(ctx context.Context, runID int64, worker string, version int, reason string) error
	FinishRun(ctx context.Context, runID int64, worker string, version int, status, code, reason string) error
	ListSeries(ctx context.Context, query ContentSeriesQuery) ([]ContentSeriesItem, int, error)
	// Source and channel configuration.
	ListSources(ctx context.Context) ([]SourceConfig, error)
	UpdateSource(ctx context.Context, id int64, expectedVersion int, name, status string) (*SourceConfig, error)
	ListChannels(ctx context.Context, sourceID int64) ([]ChannelConfig, error)
	GetChannel(ctx context.Context, id int64) (*ChannelConfig, error)
	GetChannelByCode(ctx context.Context, sourceCode, channelCode string) (*ChannelConfig, error)
	UpdateChannel(ctx context.Context, id int64, expectedVersion int, update ChannelUpdate) (*ChannelConfig, error)
	SetChannelStatus(ctx context.Context, id int64, expectedVersion int, status string) (*ChannelConfig, error)
	// UpdateRevision edits a draft revision with a version check.
	UpdateRevision(ctx context.Context, seriesID int64, revisionID int64, expectedVersion int, setTitle bool, title string, setDescription bool, description string) (*SeriesView, error)
	// CreateRevision creates a new working draft.
	CreateRevision(ctx context.Context, seriesID int64, baseRevisionID int64, replaceDraftRevisionID int64, replaceDraftVersion int) (*SeriesView, error)
	UpsertCandidateSummary(ctx context.Context, runID int64, worker string, version int, sourceCode, channel string, summary SeriesSummary) (*Candidate, error)
	// RecordRunItem stores the result of one attempt of one row.
	RecordRunItem(ctx context.Context, runID int64, worker string, version int, candidateID int64, externalID string, stage string, result string, errorCode string, errorMessage string) error
	// ListRunItems reads the rows one run produced.
	ListRunItems(ctx context.Context, runID int64, page int, pageSize int) (*RunItemPage, error)
	UpsertCandidateDetail(ctx context.Context, candidateID, runID int64, worker string, version int, detail *SeriesDetail) error
	ListCandidates(ctx context.Context, query CandidateQuery) (*CandidatePage, error)
	GetCandidate(ctx context.Context, id int64) (*Candidate, error)
	GetCandidateByExternal(ctx context.Context, sourceCode, externalID string) (*Candidate, error)
	ListCandidateEpisodes(ctx context.Context, candidateID int64) ([]CandidateEpisode, error)
	CreateSeriesFromCandidate(ctx context.Context, candidateID int64) (*SeriesView, error)
	// CreateManualSeries declares a work without a provider. The fingerprint
	// is what the request id was used for.
	CreateManualSeries(ctx context.Context, input ManualSeriesInput, fingerprint string) (*SeriesView, error)
	CreateCompletionRun(ctx context.Context, candidateID int64) (*IngestionRun, error)
	GetSeriesDetail(ctx context.Context, seriesID int64) (*SeriesView, error)
	ListRevisionEpisodes(ctx context.Context, revisionID int64, page, pageSize int) ([]RevisionEpisode, int, error)
	SetRevisionSelection(ctx context.Context, revisionID int64, expectedVersion int, mode string, externalIDs []string) (*ContentRevision, error)
}

// SourceCatalog is the SOURCE-001 usecase. It never calls the web adapter from
// read or selection methods.
type SourceCatalog struct {
	repo    SourceCatalogRepo
	adapter WebMetadataAdapter
	// adapters is the registry a channel configuration may choose from.
	adapters map[string]WebMetadataAdapter
	log      *slog.Logger
	audit    *Auditor
}

// NewSourceCatalog validates dependencies. The auditor may be nil in a unit
// test that checks business rules only; the server always provides one.
func NewSourceCatalog(repo SourceCatalogRepo, adapter WebMetadataAdapter, auditor *Auditor, logger *slog.Logger) (*SourceCatalog, error) {
	if repo == nil {
		return nil, errors.New("source catalog repo is required")
	}
	if adapter == nil {
		return nil, errors.New("web metadata adapter is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	adapters := map[string]WebMetadataAdapter{adapter.Key(): adapter}
	return &SourceCatalog{repo: repo, adapter: adapter, adapters: adapters, log: logger, audit: auditor}, nil
}

// ListPlans returns the configured plans.
func (uc *SourceCatalog) ListPlans(ctx context.Context) ([]IngestionPlan, error) {
	return uc.repo.ListPlans(ctx)
}

// SourceEntry is a web entry point the source adapter can really execute.
// validatePlanChannel refuses a plan that names a channel the server does not
// know. Whether the channel is paused is decided when collection is triggered,
// so a plan may outlive a pause.
func (uc *SourceCatalog) validatePlanChannel(ctx context.Context, sourceCode, channel string) error {
	if _, err := uc.repo.GetChannelByCode(ctx, sourceCode, channel); err != nil {
		if errors.Is(err, ErrChannelNotFound) {
			return NewSourceError(SourceCodeConfigInvalid, false,
				fmt.Errorf("source entry %s/%s is not configured", sourceCode, channel))
		}
		return err
	}
	return nil
}

// CreatePlan validates and stores one plan. The plan and the record of who
// created it are committed together.
func (uc *SourceCatalog) CreatePlan(ctx context.Context, plan IngestionPlan) (created *IngestionPlan, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionPlanCreate, AuditResourcePlan)
	defer func() { scope.End(ctx, err) }()
	plan.Code = strings.TrimSpace(plan.Code)
	plan.Name = strings.TrimSpace(plan.Name)
	plan.SourceCode = strings.TrimSpace(plan.SourceCode)
	plan.Channel = strings.TrimSpace(plan.Channel)
	if plan.Code == "" || plan.Name == "" || plan.SourceCode == "" || plan.Channel == "" {
		return nil, fmt.Errorf("%w: code, name, source and channel are required", ErrPlanPageRange)
	}
	if plan.PageStart < 1 || plan.PageEnd < plan.PageStart || plan.PageEnd-plan.PageStart+1 > 5 {
		return nil, ErrPlanPageRange
	}
	if plan.RequestBudget < 1 || plan.ItemBudget < 1 {
		return nil, ErrPlanBudget
	}
	if err := uc.validatePlanChannel(ctx, plan.SourceCode, plan.Channel); err != nil {
		return nil, err
	}
	// A zero rate means "inherit the channel"; the run snapshot records which
	// value was used.
	if plan.RatePerMinute < 0 || plan.RatePerMinute > 600 {
		return nil, ErrPlanBudget
	}
	expression, timezone, nextRunAt, err := normalizeSchedule(plan.Cron, plan.Timezone, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	plan.Cron, plan.Timezone, plan.NextRunAt = expression, timezone, nextRunAt
	return uc.repo.CreatePlan(ctx, plan)
}

// UpdatePlan validates and updates editable plan fields with a version check.
func (uc *SourceCatalog) UpdatePlan(ctx context.Context, id int64, expectedVersion int, plan IngestionPlan) (updated *IngestionPlan, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionPlanUpdate, AuditResourcePlan)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, id, 0)
	plan.Name = strings.TrimSpace(plan.Name)
	if plan.Name == "" || plan.PageStart < 1 || plan.PageEnd < plan.PageStart || plan.PageEnd-plan.PageStart+1 > 5 {
		return nil, ErrPlanPageRange
	}
	if plan.RequestBudget < 1 || plan.ItemBudget < 1 {
		return nil, ErrPlanBudget
	}
	// A zero rate means "inherit the channel".
	if plan.RatePerMinute < 0 || plan.RatePerMinute > 600 {
		return nil, ErrPlanBudget
	}
	if expectedVersion <= 0 {
		return nil, ErrPlanVersion
	}
	// The source and the channel of a plan are not editable: an edit changes
	// the pages, the budget or the schedule. A request that leaves them out
	// keeps the stored pair instead of failing the channel check on an empty
	// value.
	if plan.SourceCode == "" || plan.Channel == "" {
		stored, err := uc.repo.GetPlan(ctx, id)
		if err != nil {
			return nil, err
		}
		if plan.SourceCode == "" {
			plan.SourceCode = stored.SourceCode
		}
		if plan.Channel == "" {
			plan.Channel = stored.Channel
		}
	}
	if err := uc.validatePlanChannel(ctx, plan.SourceCode, plan.Channel); err != nil {
		return nil, err
	}
	expression, timezone, nextRunAt, err := normalizeSchedule(plan.Cron, plan.Timezone, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	plan.Cron, plan.Timezone, plan.NextRunAt = expression, timezone, nextRunAt
	return uc.repo.UpdatePlan(ctx, id, expectedVersion, plan)
}

// TriggerPlan queues a persisted run and returns it immediately.
func (uc *SourceCatalog) TriggerPlan(ctx context.Context, planID int64, expectedVersion int) (*IngestionRun, error) {
	plan, err := uc.repo.GetPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if expectedVersion > 0 && plan.Version != expectedVersion {
		return nil, ErrPlanVersion
	}
	if !plan.Enabled {
		return nil, fmt.Errorf("%w: plan is disabled", ErrRunAlreadyActive)
	}
	// A paused channel refuses every new collection, so the reason is the
	// pause and not the state of an earlier run.
	channel, err := uc.repo.GetChannelByCode(ctx, plan.SourceCode, plan.Channel)
	if err != nil {
		return nil, err
	}
	if channel.ChannelPaused() {
		return nil, ErrSourcePaused
	}
	if active, err := uc.repo.ActiveRun(ctx, planID); err != nil {
		return nil, err
	} else if active != nil {
		return nil, fmt.Errorf("%w: run %d", ErrRunAlreadyActive, active.ID)
	}
	return uc.repo.CreateRun(ctx, plan, RunKindRanking, 0)
}

// GetRun returns one persisted run.
func (uc *SourceCatalog) GetRun(ctx context.Context, id int64) (*IngestionRun, error) {
	return uc.repo.GetRun(ctx, id)
}

// ListRuns returns local run history.
func (uc *SourceCatalog) ListRuns(ctx context.Context, query RunQuery) ([]IngestionRun, int, error) {
	return uc.repo.ListRuns(ctx, query)
}

// CancelRun requests cancellation with a version check.
func (uc *SourceCatalog) CancelRun(ctx context.Context, id int64, expectedVersion int) (*IngestionRun, error) {
	if err := uc.repo.RequestCancel(ctx, id, expectedVersion); err != nil {
		return nil, err
	}
	return uc.repo.GetRun(ctx, id)
}

// RetryRun requeues a failed or paused run; consumed counters are preserved.
func (uc *SourceCatalog) RetryRun(ctx context.Context, runID int64, expectedVersion, addRequests, addItems int, reason string) (*IngestionRun, error) {
	if expectedVersion <= 0 || addRequests < 0 || addItems < 0 {
		return nil, ErrRunNotRetryable
	}
	run, err := uc.repo.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != RunStatusFailed && run.Status != RunStatusWaiting && run.Status != RunStatusBudget && run.Status != RunStatusCancelled {
		return nil, ErrRunNotRetryable
	}
	if run.Status == RunStatusBudget && addRequests == 0 && addItems == 0 {
		return nil, ErrBudgetExtension
	}
	if (addRequests > 0 || addItems > 0) && strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("%w: reason is required for a budget extension", ErrPlanBudget)
	}
	return uc.repo.RetryRun(ctx, runID, expectedVersion, addRequests, addItems, strings.TrimSpace(reason))
}

// ListCandidates reads only local snapshots.
// ListRunItems returns the rows one run produced, with the result of each
// attempt.
func (uc *SourceCatalog) ListRunItems(ctx context.Context, runID int64, page int, pageSize int) (*RunItemPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return uc.repo.ListRunItems(ctx, runID, page, pageSize)
}

// ListSeries returns one page of the content list with the working draft and
// the published revision of every series.
func (uc *SourceCatalog) ListSeries(ctx context.Context, query ContentSeriesQuery) ([]ContentSeriesItem, int, error) {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 || query.PageSize > 100 {
		query.PageSize = 20
	}
	return uc.repo.ListSeries(ctx, query)
}

// UpdateRevision edits the draft of one series. Only a draft may change and
// the write uses the version of the draft the caller read. A frozen or
// published revision is never touched.
// CreateRevision creates a new working draft for a series. It inherits the
// metadata, the selection and the frozen material of the base revision, and
// never inherits an approval, a decision or a publication.
func (uc *SourceCatalog) CreateRevision(ctx context.Context, seriesID int64, baseRevisionID int64, replaceDraftRevisionID int64, replaceDraftVersion int) (view *SeriesView, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionRevisionCreate, AuditResourceRevision)
	defer func() { scope.End(ctx, err) }()
	return uc.repo.CreateRevision(ctx, seriesID, baseRevisionID, replaceDraftRevisionID, replaceDraftVersion)
}

func (uc *SourceCatalog) UpdateRevision(ctx context.Context, seriesID int64, revisionID int64, expectedVersion int, setTitle bool, title string, setDescription bool, description string) (view *SeriesView, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionRevisionUpdate, AuditResourceRevision)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, revisionID, 0)
	if revisionID <= 0 {
		return nil, ErrRevisionConflict
	}
	return uc.repo.UpdateRevision(ctx, seriesID, revisionID, expectedVersion, setTitle, title, setDescription, description)
}

func (uc *SourceCatalog) ListCandidates(ctx context.Context, query CandidateQuery) (*CandidatePage, error) {
	return uc.repo.ListCandidates(ctx, query)
}

// GetCandidate reads one local snapshot.
func (uc *SourceCatalog) GetCandidate(ctx context.Context, id int64) (*Candidate, error) {
	return uc.repo.GetCandidate(ctx, id)
}

// ListCandidateEpisodes reads the collected episode snapshot.
func (uc *SourceCatalog) ListCandidateEpisodes(ctx context.Context, id int64) ([]CandidateEpisode, error) {
	return uc.repo.ListCandidateEpisodes(ctx, id)
}

// CreateSeries copies a complete local snapshot into a draft series. It never
// calls the web adapter; incomplete snapshots stay blocked.
func (uc *SourceCatalog) CreateSeries(ctx context.Context, candidateID int64) (view *SeriesView, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionSeriesCreate, AuditResourceSeries)
	defer func() { scope.End(ctx, err) }()
	candidate, err := uc.repo.GetCandidate(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	if candidate.SeriesID > 0 {
		return nil, ErrCandidateImported
	}
	if candidate.Completeness != CompletenessComplete {
		run, runErr := uc.repo.CreateCompletionRun(ctx, candidate.ID)
		if runErr != nil {
			return nil, fmt.Errorf("%w: completeness=%s", ErrCandidateIncomplete, candidate.Completeness)
		}
		return nil, fmt.Errorf("%w: completeness=%s, completion run %d", ErrCandidateIncomplete, candidate.Completeness, run.ID)
	}
	return uc.repo.CreateSeriesFromCandidate(ctx, candidateID)
}

// GetSeries returns a local series and its draft revision.
func (uc *SourceCatalog) GetSeries(ctx context.Context, id int64) (*SeriesView, error) {
	return uc.repo.GetSeriesDetail(ctx, id)
}

// ListRevisionEpisodes returns the episode snapshot of a draft revision.
func (uc *SourceCatalog) ListRevisionEpisodes(ctx context.Context, revisionID int64, page, pageSize int) ([]RevisionEpisode, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 200
	}
	return uc.repo.ListRevisionEpisodes(ctx, revisionID, page, pageSize)
}

// SetSelection replaces the selected episode set of a draft revision.
func (uc *SourceCatalog) SetSelection(ctx context.Context, revisionID int64, expectedVersion int, mode string, externalIDs []string) (revision *ContentRevision, err error) {
	ctx, scope := uc.audit.Begin(ctx, AuditActionRevisionSelect, AuditResourceRevision)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, revisionID, 0)
	if revisionID <= 0 {
		return nil, ErrRevisionNotFound
	}
	if expectedVersion <= 0 {
		return nil, ErrRevisionVersion
	}
	mode = strings.TrimSpace(mode)
	if mode != "all" && mode != "explicit" {
		return nil, fmt.Errorf("%w: selection mode must be all or explicit", ErrSelectionMismatch)
	}
	seen := make(map[string]struct{}, len(externalIDs))
	for _, id := range externalIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return nil, ErrSelectionMismatch
		}
		if _, exists := seen[trimmed]; exists {
			return nil, ErrSelectionMismatch
		}
		seen[trimmed] = struct{}{}
	}
	if mode == "all" && len(externalIDs) > 0 {
		return nil, fmt.Errorf("%w: all mode must not pass episode ids", ErrSelectionMismatch)
	}
	return uc.repo.SetRevisionSelection(ctx, revisionID, expectedVersion, mode, externalIDs)
}

// CandidateHash returns a stable hash of the normalized summary fields.
func CandidateHash(summary SeriesSummary) string {
	payload, _ := json.Marshal(struct {
		Title       string            `json:"title"`
		Cover       string            `json:"cover"`
		Description string            `json:"description"`
		Tags        []string          `json:"tags"`
		Episodes    int               `json:"episodes"`
		Open        int               `json:"open"`
		Rank        int               `json:"rank"`
		Metrics     map[string]string `json:"metrics"`
	}{summary.Title, summary.CoverURL, summary.Description, summary.Tags, summary.EpisodeCount, summary.OpenEpisodeCount, summary.Rank, summary.Metrics})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
