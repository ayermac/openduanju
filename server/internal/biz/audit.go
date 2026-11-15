package biz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Audit results. A failed operation is recorded as a failure, never as a
// success.
const (
	AuditResultSuccess = "success"
	AuditResultFailure = "failure"
)

// Audit resource types. They name the kind of row the action changed, so the
// list can be filtered by object without knowing the URL shape.
const (
	AuditResourceSeries      = "series"
	AuditResourceRevision    = "revision"
	AuditResourceBatch       = "media_batch"
	AuditResourceReview      = "review"
	AuditResourcePublication = "publication"
	AuditResourceSource      = "source"
	AuditResourceChannel     = "source_channel"
	AuditResourcePlan        = "ingestion_plan"
	AuditResourceStaff       = "staff"
	AuditResourceGrant       = "authorization_grant"
)

// Actions of the operations the console performs. The name is stable: it is
// stored, filtered on and read by an operator, so it never carries an
// identifier or a free-form message.
const (
	AuditActionSeriesCreate     = "content.series.create"
	AuditActionRevisionCreate   = "content.revision.create"
	AuditActionRevisionUpdate   = "content.revision.update"
	AuditActionRevisionSelect   = "content.revision.select"
	AuditActionBatchSubmit      = "media.batch.submit"
	AuditActionBatchRetry       = "media.batch.retry"
	AuditActionBatchCancel      = "media.batch.cancel"
	AuditActionReviewClaim      = "review.claim"
	AuditActionReviewDecide     = "review.decide"
	AuditActionPublish          = "publication.publish"
	AuditActionUnpublish        = "publication.unpublish"
	AuditActionSourceUpdate     = "source.update"
	AuditActionChannelUpdate    = "source_channel.update"
	AuditActionChannelStatus    = "source_channel.status"
	AuditActionPlanCreate       = "ingestion.plan.create"
	AuditActionPlanUpdate       = "ingestion.plan.update"
	AuditActionGrantCreate      = "authorization.create"
	AuditActionGrantRevoke      = "authorization.revoke"
	AuditActionStaffCreate      = "staff.create"
	AuditActionStaffUpdate      = "staff.update"
	AuditActionStaffResetSecret = "staff.reset_password"
)

// ErrAuditWriteFailed reports that the audit row of a successful change could
// not be written. The change is rolled back with it: a write that happened
// without a traceable record is exactly the gap this slice closes.
var ErrAuditWriteFailed = errors.New("audit event could not be written")

// AuditEvent is one recorded write: who did what to which resource, with
// which version, what came of it, and the trace it belongs to. It never holds
// a credential, a signed address or a payload body.
type AuditEvent struct {
	ID              int64
	TraceID         string
	ActorID         int64
	ActorName       string
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceVersion int
	Result          string
	Reason          string
	CreatedAt       time.Time
}

// AuditQuery filters the audit list.
type AuditQuery struct {
	ResourceType string
	ResourceID   string
	Action       string
	ActorID      int64
	ActorName    string
	TraceID      string
	Page         int
	PageSize     int
}

// AuditRepo stores and reads audit events.
type AuditRepo interface {
	Record(ctx context.Context, event AuditEvent) error
	List(ctx context.Context, query AuditQuery) ([]AuditEvent, int, error)
}

// Actor identifies who performed an operation.
type Actor struct {
	ID       int64
	Username string
}

// Auditor records the write operations of the console.
//
// A successful change is recorded by the repository inside the transaction
// that performs it (see AuditIntent): either both are committed or neither is.
// A failed operation is recorded here, after the business transaction rolled
// back, so the reason survives without pretending the change happened.
type Auditor struct {
	repo AuditRepo
	log  *slog.Logger
}

// NewAuditor builds the audit recorder.
func NewAuditor(repo AuditRepo, logger *slog.Logger) (*Auditor, error) {
	if repo == nil {
		return nil, errors.New("audit repo is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Auditor{repo: repo, log: logger}, nil
}

// auditState is the outcome of the pending event of one operation.
type auditState int

const (
	// auditPending means the operation has not written its event yet.
	auditPending auditState = iota
	// auditCommitted means the repository wrote the event in the business
	// transaction.
	auditCommitted
	// auditUnchanged means the operation changed nothing - an idempotent
	// replay - so there is no change to record.
	auditUnchanged
)

// AuditIntent is the event an operation has to leave behind. The use case
// declares the action and the resource type before it starts; the repository
// fills in the identity and the version of the row it wrote and commits the
// event in the same transaction.
type AuditIntent struct {
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceVersion int
	// Note is the reason an operator stated for an explicit action, such as
	// taking a published work down. It is written on the success row. Failure
	// rows keep a stable code instead, so a note never replaces a code.
	Note  string
	state auditState
}

// SetResource names the row the operation changed. It is called by the
// repository that owns the transaction, the only place that knows the
// resulting identity and version.
// SetNote records the operator's stated reason for this event.
func (i *AuditIntent) SetNote(note string) {
	if i == nil {
		return
	}
	i.Note = note
}

func (i *AuditIntent) SetResource(id string, version int) {
	if i == nil {
		return
	}
	if id == "" {
		return
	}
	i.ResourceID = id
	i.ResourceVersion = version
}

// Pending reports whether the event still has to be written.
func (i *AuditIntent) Pending() bool { return i != nil && i.state == auditPending }

// MarkCommitted records that the event was written with the change.
func (i *AuditIntent) MarkCommitted() {
	if i == nil {
		return
	}
	i.state = auditCommitted
}

// MarkUnchanged records that the operation did not change anything. An
// idempotent replay returns the existing result; recording it as a new change
// would report an event that never happened.
func (i *AuditIntent) MarkUnchanged() {
	if i == nil {
		return
	}
	i.state = auditUnchanged
}

// Committed reports whether the event was written with the change.
func (i *AuditIntent) Committed() bool { return i != nil && i.state == auditCommitted }

// auditIntentKey carries the pending event in the context.
type auditIntentKey struct{}

// AuditIntentFrom returns the pending event of the current operation, or nil.
func AuditIntentFrom(ctx context.Context) *AuditIntent {
	if ctx == nil {
		return nil
	}
	intent, _ := ctx.Value(auditIntentKey{}).(*AuditIntent)
	return intent
}

// auditActorKey carries the authenticated actor in the context.
type auditActorKey struct{}

// WithAuditActor returns a context that carries the actor of the request.
// The transport sets it after authentication, so a use case can record who
// did something without trusting anything the caller sent.
func WithAuditActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, auditActorKey{}, actor)
}

// AuditActorFrom returns the authenticated actor of ctx.
func AuditActorFrom(ctx context.Context) (Actor, bool) {
	if ctx == nil {
		return Actor{}, false
	}
	actor, ok := ctx.Value(auditActorKey{}).(Actor)
	return actor, ok
}

// AuditScope is the pending record of one use case call.
type AuditScope struct {
	auditor      *Auditor
	intent       *AuditIntent
	action       string
	resourceType string
}

// Begin declares the event of the operation that ctx is about to run. The
// returned context carries the intent into the repositories.
func (a *Auditor) Begin(ctx context.Context, action, resourceType string) (context.Context, *AuditScope) {
	intent := &AuditIntent{Action: action, ResourceType: resourceType}
	return context.WithValue(ctx, auditIntentKey{}, intent), &AuditScope{
		auditor:      a,
		intent:       intent,
		action:       action,
		resourceType: resourceType,
	}
}

// End closes the operation. A failure is recorded once, after the business
// transaction is gone, and never replaces the error it explains. A success
// that was not committed with the change is reported in the log: the change
// exists, so the missing record is a defect, not a reason to fail the caller.
func (s *AuditScope) End(ctx context.Context, err error) {
	if s == nil {
		return
	}
	if err == nil {
		if s.intent.Pending() && s.auditor != nil {
			s.auditor.log.ErrorContext(ctx, "audit event was not committed with the change",
				"action", s.action, "resource_type", s.resourceType)
		}
		return
	}
	if s.auditor == nil {
		return
	}
	if errors.Is(err, ErrAuditWriteFailed) {
		// The change itself was refused because its record could not be
		// written; the repository already logged the cause. Recording a
		// second failure event would need the same broken table.
		s.auditor.log.ErrorContext(ctx, "change rolled back because its audit event was not written",
			"action", s.action, "resource_type", s.resourceType)
		return
	}
	s.auditor.RecordFailure(ctx, s.action, s.resourceType, s.intent.ResourceID, err)
}

// Audit runs op with the event of action declared for resourceType. The
// success record is written by the repository in the business transaction; a
// failure is recorded afterwards.
func Audit[T any](ctx context.Context, auditor *Auditor, action, resourceType string, op func(context.Context) (T, error)) (T, error) {
	if auditor == nil {
		return op(ctx)
	}
	scoped, scope := auditor.Begin(ctx, action, resourceType)
	value, err := op(scoped)
	scope.End(scoped, err)
	return value, err
}

// RecordFailure writes one failed operation. It is used for the events that
// have to be kept although the change was refused.
func (a *Auditor) RecordFailure(ctx context.Context, action, resourceType, resourceID string, operationErr error) {
	if a == nil {
		return
	}
	actor, ok := AuditActorFrom(ctx)
	event := AuditEvent{
		TraceID:      TraceID(ctx),
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Result:       AuditResultFailure,
		Reason:       AuditReason(operationErr),
	}
	if ok {
		event.ActorID = actor.ID
		event.ActorName = actor.Username
	}
	if err := a.repo.Record(ctx, event); err != nil {
		// The original error is the one the caller sees. A lost failure
		// record is a defect of the audit trail, so it is logged loudly.
		a.log.ErrorContext(ctx, "failed audit event could not be recorded",
			"action", action, "resource_type", resourceType, "resource_id", resourceID,
			"reason", event.Reason, "err", err)
	}
}

// List returns the recorded events.
func (a *Auditor) List(ctx context.Context, query AuditQuery) ([]AuditEvent, int, error) {
	return a.repo.List(ctx, query)
}

// auditReasons maps a business error to the stable reason an audit row keeps.
// The row never stores an error body: it names the kind of refusal so a report
// can be followed without copying SQL, upstream responses or secrets.
var auditReasons = []struct {
	err  error
	code string
}{
	{ErrRevisionVersion, "VERSION_CONFLICT"},
	{ErrBatchVersion, "VERSION_CONFLICT"},
	{ErrReviewVersion, "VERSION_CONFLICT"},
	{ErrPublicationVersion, "VERSION_CONFLICT"},
	{ErrPlanVersion, "VERSION_CONFLICT"},
	{ErrSourceVersion, "VERSION_CONFLICT"},
	{ErrChannelVersion, "VERSION_CONFLICT"},
	{ErrGrantVersion, "VERSION_CONFLICT"},
	{ErrStaffVersion, "VERSION_CONFLICT"},
	{ErrRevisionConflict, "REVISION_CONFLICT"},
	{ErrDraftExists, "DRAFT_EXISTS"},
	{ErrRevisionNotEditable, "REVISION_NOT_EDITABLE"},
	{ErrNoSelectedEpisodes, "NO_SELECTED_EPISODES"},
	{ErrBatchInputNotReady, "INPUT_NOT_READY"},
	{ErrGrantNotFound, "AUTHORIZATION_REQUIRED"},
	{ErrGrantNotEffective, "AUTHORIZATION_INVALID"},
	{ErrGrantPermission, "AUTHORIZATION_INVALID"},
	{ErrGrantNotApplicable, "AUTHORIZATION_INVALID"},
	{ErrBatchAuthorizationInvalid, "AUTHORIZATION_INVALID"},
	{ErrBatchNotFound, "NOT_FOUND"},
	{ErrReviewNotFound, "NOT_FOUND"},
	{ErrPublicationMissing, "NOT_FOUND"},
	{ErrSeriesNotFound, "NOT_FOUND"},
	{ErrRevisionNotFound, "NOT_FOUND"},
	{ErrPlanNotFound, "NOT_FOUND"},
	{ErrSourceNotFound, "NOT_FOUND"},
	{ErrChannelNotFound, "NOT_FOUND"},
	{ErrStaffNotFound, "NOT_FOUND"},
	{ErrReviewNotOpen, "REVIEW_NOT_OPEN"},
	{ErrReviewNotClaimed, "REVIEW_NOT_CLAIMED"},
	{ErrReviewNotAssignee, "NOT_ASSIGNEE"},
	{ErrReviewSelfDecision, "SELF_REVIEW"},
	{ErrReviewFinal, "REVIEW_FINAL"},
	{ErrReviewReason, "REASON_REQUIRED"},
	{ErrReviewChecklist, "CHECKLIST_INCOMPLETE"},
	{ErrReviewPrecondition, "PRECONDITION_FAILED"},
	{ErrReviewNotApproved, "NOT_APPROVED"},
	{ErrBatchNotCancellable, "NOT_CANCELLABLE"},
	{ErrBatchNotRetryable, "NO_RETRYABLE_ITEMS"},
	{ErrBatchAlreadyActive, "BATCH_ALREADY_ACTIVE"},
	{ErrProfileRequired, "PROFILE_REQUIRED"},
	{ErrVersionRequired, "VERSION_REQUIRED"},
	{ErrIdempotencyConflict, "IDEMPOTENCY_CONFLICT"},
	{ErrManualTitleRequired, "TITLE_REQUIRED"},
	{ErrManualDescription, "DESCRIPTION_INVALID"},
	{ErrManualEpisodes, "EPISODES_REQUIRED"},
	{ErrManualEpisodeNo, "EPISODE_NUMBER_INVALID"},
	{ErrDuplicateEpisodeNo, "DUPLICATE_EPISODE_NUMBER"},
	{ErrPlanCodeExists, "PLAN_CODE_EXISTS"},
	{ErrPlanPageRange, "PAGE_RANGE_INVALID"},
	{ErrPlanBudget, "BUDGET_INVALID"},
	{ErrPlanSchedule, "SCHEDULE_INVALID"},
	{ErrSourceName, "NAME_INVALID"},
	{ErrSourceStatus, "STATUS_INVALID"},
	{ErrChannelName, "NAME_INVALID"},
	{ErrChannelEntry, "ENTRY_INVALID"},
	{ErrChannelURL, "URL_INVALID"},
	{ErrChannelRate, "RATE_INVALID"},
	{ErrChannelTimeout, "TIMEOUT_INVALID"},
	{ErrChannelAdapter, "ADAPTER_INVALID"},
	{ErrSourcePaused, "SOURCE_PAUSED"},
	{ErrUsernameExists, "USERNAME_EXISTS"},
	{ErrLastAdmin, "LAST_ADMIN"},
	{ErrStaffInvalid, "STAFF_INVALID"},
	{ErrForbidden, "FORBIDDEN"},
	{ErrSelectionMismatch, "SELECTION_MISMATCH"},
	{ErrAuditWriteFailed, "AUDIT_WRITE_FAILED"},
}

// AuditReason returns the stable reason of a failure. It never returns the
// message of the error, so a row cannot become a copy of an upstream response.
func AuditReason(err error) string {
	if err == nil {
		return ""
	}
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) && sourceErr.Code != "" {
		return string(sourceErr.Code)
	}
	for _, entry := range auditReasons {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return "OPERATION_FAILED"
}

// AuditHandle is a convenience for a use case that has to name the row it
// changed before a repository writes the event: the intent is filled here when
// the use case, not the repository, knows the identity.
// AuditNote records the operator's stated reason on the pending event. It is
// only for actions that ask the operator why, and it never overrides a failure
// code.
func AuditNote(ctx context.Context, note string) {
	if intent := AuditIntentFrom(ctx); intent != nil {
		intent.SetNote(note)
	}
}

func AuditHandle(ctx context.Context, id int64, version int) {
	if intent := AuditIntentFrom(ctx); intent != nil {
		intent.SetResource(fmt.Sprintf("%d", id), version)
	}
}

// AuditUnchanged marks the operation as an idempotent replay. The caller saw
// the change of an earlier call; this call changed nothing.
func AuditUnchanged(ctx context.Context) {
	if intent := AuditIntentFrom(ctx); intent != nil {
		intent.MarkUnchanged()
	}
}
