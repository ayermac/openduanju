package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Review task states.
const (
	ReviewOpen     = "open"
	ReviewClaimed  = "claimed"
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
)

// Decisions.
const (
	DecisionApprove = "approved"
	DecisionReject  = "rejected"
)

// Publication states.
const (
	PublicationPublished   = "published"
	PublicationUnpublished = "unpublished"
)

// ReviewCase is one row of the review worklist.
type ReviewCase struct {
	TaskID      int64
	RevisionID  int64
	SeriesID    int64
	BatchID     int64
	Status      string
	Assignee    string
	SubmittedBy string
	Version     int
	CreatedAt   time.Time
	SeriesTitle string
	ItemTotal   int
	ItemReady   int
	CoverStatus string
}

// ReviewAsset is a material object the reviewer may preview.
type ReviewAsset struct {
	AssetID     int64
	Object      ObjectRef
	ContentType string
	SizeBytes   int64
	SHA256      string
	DurationMS  int64
	Width       int
	Height      int
	SHA         string
}

// ReviewEpisode is one selected episode with its ready material.
type ReviewEpisode struct {
	EpisodeID  int64
	Number     int
	ExternalID string
	Asset      *ReviewAsset
}

// ReviewDecision is the final outcome of a task. It is written once.
type ReviewDecision struct {
	ID                   int64
	TaskID               int64
	Decision             string
	Reason               string
	DecidedBy            string
	DecidedAt            time.Time
	TaskVersion          int
	AuthorizationGrantID int64
	AuthorizationVersion int
	AuthorizationHash    string
}

// ReviewDetail is the review screen payload.
type ReviewDetail struct {
	Case          ReviewCase
	Episodes      []ReviewEpisode
	Cover         *ReviewAsset
	Authorization *AuthorizationGrant
	Decision      *ReviewDecision
}

// ReviewQuery filters the worklist.
type ReviewQuery struct {
	Status   string
	Page     int
	PageSize int
	Actor    string
}

// ReviewChecklist is what a reviewer confirms before an approval. The items
// follow the product's minimum list; ReviewedEpisodes records how many
// episodes the reviewer actually loaded, so an empty preview cannot be
// reported as a technical pass.
type ReviewChecklist struct {
	TitleCoverIntro     bool
	EpisodeMapping      bool
	AudioVideo          bool
	AuthorizationPublic bool
	ReviewedEpisodes    int
}

// Validate refuses an incomplete checklist. A rejection does not need one.
func (c ReviewChecklist) Validate(total int) error {
	switch {
	case !c.TitleCoverIntro:
		return fmt.Errorf("%w: 标题/封面/简介未确认", ErrReviewChecklist)
	case !c.EpisodeMapping:
		return fmt.Errorf("%w: 分集对应未确认", ErrReviewChecklist)
	case !c.AudioVideo:
		return fmt.Errorf("%w: 声音画面未确认", ErrReviewChecklist)
	case !c.AuthorizationPublic:
		return fmt.Errorf("%w: 授权与全年龄可公开未确认", ErrReviewChecklist)
	case c.ReviewedEpisodes < 1:
		return fmt.Errorf("%w: 至少抽查1集", ErrReviewChecklist)
	case total > 0 && c.ReviewedEpisodes > total:
		return fmt.Errorf("%w: 抽查集数超过选中集数", ErrReviewChecklist)
	}
	return nil
}

// DecideReviewRequest is one decision.
type DecideReviewRequest struct {
	TaskID          int64
	ExpectedVersion int
	Decision        string
	Reason          string
	Actor           string
	Checklist       ReviewChecklist
}

// Publication is the published revision of one series.
type Publication struct {
	ID                   int64
	SeriesID             int64
	RevisionID           int64
	Status               string
	PublishedBy          string
	PublishedAt          time.Time
	UnpublishedBy        string
	UnpublishedAt        *time.Time
	AuthorizationGrantID int64
	AuthorizationVersion int
	Version              int
}

// Review failures.
var (
	ErrReviewNotFound     = errors.New("review task not found")
	ErrReviewVersion      = errors.New("review version conflict")
	ErrReviewNotOpen      = errors.New("review task is not open for claiming")
	ErrReviewNotClaimed   = errors.New("review task is not claimed")
	ErrReviewNotAssignee  = errors.New("only the assignee may decide")
	ErrReviewSelfDecision = errors.New("a submission cannot be reviewed by its own author")
	ErrReviewFinal        = errors.New("a review decision is final")
	ErrReviewReason       = errors.New("a rejection requires a reason")
	ErrReviewChecklist    = errors.New("the review checklist is incomplete")
	ErrReviewPrecondition = errors.New("the material or authorization is not ready for approval")
	ErrReviewNotApproved  = errors.New("the revision is not approved")
	ErrPublicationMissing = errors.New("publication not found")
	ErrPublicationVersion = errors.New("publication version conflict")
	// ErrUnpublishReason refuses an unpublish without a stated reason.
	ErrUnpublishReason = errors.New("unpublish reason is required")
)

// ReviewRepo persists review tasks, decisions and publications. The rules
// that must hold inside a transaction (assignment, finality, material and
// authorization re-checks) live in the repository so a stale read cannot let
// them pass.
type ReviewRepo interface {
	ListReviews(ctx context.Context, query ReviewQuery) ([]ReviewCase, int, error)
	GetReview(ctx context.Context, taskID int64) (*ReviewDetail, error)
	ClaimReview(ctx context.Context, taskID int64, expectedVersion int, actor string) (*ReviewCase, error)
	DecideReview(ctx context.Context, request DecideReviewRequest) (*ReviewCase, error)
	PublishRevision(ctx context.Context, seriesID int64, revisionID int64, actor string) (*Publication, error)
	UnpublishSeries(ctx context.Context, seriesID int64, expectedVersion int, actor string) (*Publication, error)
	GetPublication(ctx context.Context, seriesID int64) (*Publication, error)
}

// Reviews is the use case over review and publication.
type Reviews struct {
	repo  ReviewRepo
	audit *Auditor
}

// NewReviews builds the review use case. The auditor may be nil in a unit test
// that checks business rules only.
func NewReviews(repo ReviewRepo, auditor *Auditor) *Reviews {
	return &Reviews{repo: repo, audit: auditor}
}

// List returns a page of review tasks.
func (r *Reviews) List(ctx context.Context, query ReviewQuery) ([]ReviewCase, int, error) {
	if query.Status != "" {
		switch query.Status {
		case ReviewOpen, ReviewClaimed, ReviewApproved, ReviewRejected:
		default:
			return nil, 0, fmt.Errorf("%w: unknown status %q", ErrReviewNotFound, query.Status)
		}
	}
	return r.repo.ListReviews(ctx, query)
}

// Get returns one review with its material, authorization and decision.
func (r *Reviews) Get(ctx context.Context, taskID int64) (*ReviewDetail, error) {
	return r.repo.GetReview(ctx, taskID)
}

// Claim assigns a task to the reviewer. A task can only be claimed once, and
// a reviewer cannot claim a submission they prepared.
func (r *Reviews) Claim(ctx context.Context, taskID int64, expectedVersion int, actor string) (claimed *ReviewCase, err error) {
	ctx, scope := r.audit.Begin(ctx, AuditActionReviewClaim, AuditResourceReview)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, taskID, 0)
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrForbidden
	}
	if expectedVersion <= 0 {
		return nil, ErrReviewVersion
	}
	return r.repo.ClaimReview(ctx, taskID, expectedVersion, actor)
}

// Decide records the final outcome. A rejection needs a reason; an approval
// re-checks the revision, the material and the frozen authorization.
func (r *Reviews) Decide(ctx context.Context, request DecideReviewRequest) (decided *ReviewCase, err error) {
	ctx, scope := r.audit.Begin(ctx, AuditActionReviewDecide, AuditResourceReview)
	defer func() { scope.End(ctx, err) }()
	AuditHandle(ctx, request.TaskID, 0)
	request.Actor = strings.TrimSpace(request.Actor)
	if request.Actor == "" {
		return nil, ErrForbidden
	}
	if request.ExpectedVersion <= 0 {
		return nil, ErrReviewVersion
	}
	request.Reason = strings.TrimSpace(request.Reason)
	switch request.Decision {
	case DecisionApprove:
		request.Reason = ""
		if err := request.Checklist.Validate(0); err != nil {
			return nil, err
		}
	case DecisionReject:
		if request.Reason == "" {
			return nil, ErrReviewReason
		}
		if len(request.Reason) > 1000 {
			return nil, fmt.Errorf("%w: reason is too long", ErrReviewReason)
		}
	default:
		return nil, fmt.Errorf("%w: unknown decision %q", ErrReviewReason, request.Decision)
	}
	return r.repo.DecideReview(ctx, request)
}

// Publish makes an approved revision the published one. It never publishes
// on its own: approval and publication are separate steps.
func (r *Reviews) Publish(ctx context.Context, seriesID int64, revisionID int64, actor string) (publication *Publication, err error) {
	ctx, scope := r.audit.Begin(ctx, AuditActionPublish, AuditResourcePublication)
	defer func() { scope.End(ctx, err) }()
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrForbidden
	}
	return r.repo.PublishRevision(ctx, seriesID, revisionID, actor)
}

// Unpublish takes the current publication down. The published revision is
// recorded, not deleted.
func (r *Reviews) Unpublish(ctx context.Context, seriesID int64, expectedVersion int, actor, reason string) (publication *Publication, err error) {
	ctx, scope := r.audit.Begin(ctx, AuditActionUnpublish, AuditResourcePublication)
	defer func() { scope.End(ctx, err) }()
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, ErrForbidden
	}
	if expectedVersion <= 0 {
		return nil, ErrPublicationVersion
	}
	// Taking a published work down is deliberate: the operator states why, and
	// the reason travels with the audit row.
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 200 {
		return nil, ErrUnpublishReason
	}
	AuditNote(ctx, reason)
	return r.repo.UnpublishSeries(ctx, seriesID, expectedVersion, actor)
}

// Publication returns the current publication of a series.
func (r *Reviews) Publication(ctx context.Context, seriesID int64) (*Publication, error) {
	return r.repo.GetPublication(ctx, seriesID)
}
