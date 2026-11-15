package data

import (
	"context"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/candidateepisode"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/contentseries"
	"openduanju-server/internal/data/ent/ingestionplan"
	"openduanju-server/internal/data/ent/ingestionrun"
	"openduanju-server/internal/data/ent/ingestionrunitem"
	"openduanju-server/internal/data/ent/revisionepisode"
	"openduanju-server/internal/data/ent/source"
	"openduanju-server/internal/data/ent/sourcecandidate"
	"openduanju-server/internal/data/ent/sourcechannel"

	"entgo.io/ent/dialect/sql"
)

// SourceCatalogRepo stores plans, runs and snapshots through Ent entities while
// the schema itself stays owned by reviewed SQL migrations.
type SourceCatalogRepo struct {
	db *ent.Client
}

// NewSourceCatalogRepo builds the repository on the shared Ent client.
func NewSourceCatalogRepo(d *Data) biz.SourceCatalogRepo {
	return &SourceCatalogRepo{db: d.Ent()}
}

// NewSourceCatalogRepoWithDB builds the repository on an explicit client.
func NewSourceCatalogRepoWithDB(db *ent.Client) biz.SourceCatalogRepo {
	return &SourceCatalogRepo{db: db}
}

// ListPlans returns all plans ordered by id.
func (r *SourceCatalogRepo) ListPlans(ctx context.Context) ([]biz.IngestionPlan, error) {
	rows, err := r.db.IngestionPlan.Query().Order(ent.Asc("id")).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.IngestionPlan, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPlan(row))
	}
	return out, nil
}

// GetPlan returns one plan.
func (r *SourceCatalogRepo) GetPlan(ctx context.Context, id int64) (*biz.IngestionPlan, error) {
	row, err := r.db.IngestionPlan.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, biz.ErrPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	plan := toPlan(row)
	return &plan, nil
}

// CreatePlan stores one plan. The plan and the record of who created it are
// committed together: a plan that exists without a traceable author is exactly
// what the audit trail is for.
func (r *SourceCatalogRepo) CreatePlan(ctx context.Context, plan biz.IngestionPlan) (*biz.IngestionPlan, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	builder := tx.IngestionPlan.Create().
		SetCode(plan.Code).
		SetName(plan.Name).
		SetSourceCode(plan.SourceCode).
		SetChannel(plan.Channel).
		SetPageStart(int32(plan.PageStart)).
		SetPageEnd(int32(plan.PageEnd)).
		SetRequestBudget(int32(plan.RequestBudget)).
		SetItemBudget(int32(plan.ItemBudget)).
		SetRatePerMinute(int32(plan.RatePerMinute)).
		SetFetchDetails(plan.FetchDetails).
		SetFetchEpisodes(plan.FetchEpisodes).
		SetEnabled(plan.Enabled).
		SetCron(plan.Cron).
		SetTimezone(plan.Timezone)
	if plan.NextRunAt != nil {
		builder = builder.SetNextRunAt(*plan.NextRunAt)
	}
	row, err := builder.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsConstraintError(err) {
			// The code of a plan is unique; a repeated code is a conflict the
			// operator can act on, not an internal failure.
			return nil, biz.ErrPlanCodeExists
		}
		return nil, err
	}
	auditResource(ctx, row.ID, int(row.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	created := toPlan(row)
	return &created, nil
}

// DuePlans returns enabled plans whose cron slot is due.
func (r *SourceCatalogRepo) DuePlans(ctx context.Context, now time.Time, limit int) ([]biz.IngestionPlan, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.IngestionPlan.Query().
		Where(
			ingestionplan.EnabledEQ(true),
			ingestionplan.CronNEQ(""),
			ingestionplan.NextRunAtNotNil(),
			ingestionplan.NextRunAtLTE(now),
		).
		Order(ent.Asc("next_run_at"), ent.Asc("id")).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.IngestionPlan, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPlan(row))
	}
	return out, nil
}

// AdvancePlanSchedule claims one cron slot with a compare-and-set on the
// stored fire time, so two schedulers never act on the same slot.
func (r *SourceCatalogRepo) AdvancePlanSchedule(ctx context.Context, planID int64, expectedNextRunAt, nextRunAt time.Time) (bool, error) {
	affected, err := r.db.IngestionPlan.Update().
		Where(
			ingestionplan.IDEQ(planID),
			ingestionplan.NextRunAtEQ(expectedNextRunAt),
		).
		SetNextRunAt(nextRunAt).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// CreateRun persists a queued run holding a plan snapshot. The plan row stays
// locked for the whole transaction, so a scheduled run and a manual trigger
// cannot both create a run for the same plan.
func (r *SourceCatalogRepo) CreateRun(ctx context.Context, plan *biz.IngestionPlan, kind string, candidateID int64) (*biz.IngestionRun, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	locked, err := tx.IngestionPlan.Query().Where(ingestionplan.IDEQ(plan.ID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrPlanNotFound
		}
		return nil, err
	}
	// The channel is resolved here, not at trigger time only: the run records
	// what it will really use, and a paused channel cannot start new work.
	owner, err := tx.Source.Query().Where(source.CodeEQ(locked.SourceCode)).Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	channel, err := tx.SourceChannel.Query().
		Where(sourcechannel.SourceIDEQ(owner.ID), sourcechannel.CodeEQ(locked.Channel)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	if channel.Status == biz.SourceStatusPaused || owner.Status == biz.SourceStatusPaused {
		_ = tx.Rollback()
		return nil, biz.ErrSourcePaused
	}
	active, err := tx.IngestionRun.Query().
		Where(
			ingestionrun.PlanIDEQ(locked.ID),
			ingestionrun.StatusIn(biz.RunStatusQueued, biz.RunStatusRunning, biz.RunStatusWaiting, biz.RunStatusBudget),
		).
		ForUpdate().
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, err
	}
	if active != nil {
		_ = tx.Rollback()
		return nil, biz.ErrRunAlreadyActive
	}
	rate, rateSource := biz.EffectiveRate(int(locked.RatePerMinute), int(channel.RatePerMinute))
	snapshot := map[string]any{
		"plan_id":                 locked.ID,
		"code":                    locked.Code,
		"channel":                 locked.Channel,
		"page_start":              int(locked.PageStart),
		"page_end":                int(locked.PageEnd),
		"request_budget":          int(locked.RequestBudget),
		"item_budget":             int(locked.ItemBudget),
		"rate_per_minute":         rate,
		"rate_source":             rateSource,
		"channel_base_url":        channel.BaseURL,
		"channel_timeout_seconds": int(channel.TimeoutSeconds),
		"channel_rate_per_minute": int(channel.RatePerMinute),
		"fetch_details":           locked.FetchDetails,
		"fetch_episodes":          locked.FetchEpisodes,
	}
	builder := tx.IngestionRun.Create().
		SetPlanID(locked.ID).
		SetPlanSnapshot(snapshot).
		SetKind(kind).
		SetSourceCode(locked.SourceCode).
		SetChannel(locked.Channel).
		SetPageStart(locked.PageStart).
		SetPageEnd(locked.PageEnd).
		SetStatus(biz.RunStatusQueued).
		SetRequestBudget(locked.RequestBudget).
		SetItemBudget(locked.ItemBudget).
		// The effective rate: the plan override when it is set, otherwise the
		// channel value resolved above.
		SetRatePerMinute(int32(rate)).
		SetPageCursor(locked.PageStart - 1).
		SetVersion(1).
		// The run is executed by a worker, not by the request. Storing the
		// identity of the trigger keeps the task reachable after the request
		// is gone and after a worker restart.
		SetTraceID(biz.TraceID(ctx)).
		SetStartedAt(time.Now().UTC())
	if candidateID > 0 {
		builder.SetCandidateID(candidateID)
	}
	row, err := builder.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	run := toRun(row)
	return &run, nil
}

// ActiveRun returns a non-terminal run of the plan.
func (r *SourceCatalogRepo) ActiveRun(ctx context.Context, planID int64) (*biz.IngestionRun, error) {
	row, err := r.db.IngestionRun.Query().
		Where(
			ingestionrun.PlanIDEQ(planID),
			ingestionrun.StatusIn(biz.RunStatusQueued, biz.RunStatusRunning, biz.RunStatusWaiting, biz.RunStatusBudget),
		).
		Order(ent.Desc("id")).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run := toRun(row)
	return &run, nil
}

// ClaimRun leases the next queued or expired run for one worker.
func (r *SourceCatalogRepo) ClaimRun(ctx context.Context, worker string, lease time.Duration) (*biz.IngestionRun, error) {
	now := time.Now().UTC()
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.IngestionRun.Query().
		Where(
			ingestionrun.Or(
				ingestionrun.StatusEQ(biz.RunStatusQueued),
				ingestionrun.And(
					ingestionrun.StatusEQ(biz.RunStatusRunning),
					ingestionrun.Or(
						ingestionrun.LeaseUntilIsNil(),
						ingestionrun.LeaseUntilLT(now),
					),
				),
				ingestionrun.And(
					ingestionrun.StatusEQ(biz.RunStatusWaiting),
					ingestionrun.Or(
						ingestionrun.NextRequestAtIsNil(),
						ingestionrun.NextRequestAtLTE(now),
					),
				),
			),
		).
		Order(ent.Asc("id")).
		ForUpdate(sql.WithLockAction(sql.SkipLocked)).
		First(ctx)
	if ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, nil
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	updated, err := row.Update().
		SetStatus(biz.RunStatusRunning).
		SetLeaseOwner(worker).
		SetLeaseUntil(now.Add(lease)).
		ClearNextRequestAt().
		AddVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	run := toRun(updated)
	return &run, nil
}

// GetRun returns one run.
func (r *SourceCatalogRepo) GetRun(ctx context.Context, id int64) (*biz.IngestionRun, error) {
	row, err := r.db.IngestionRun.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, biz.ErrRunNotFound
	}
	if err != nil {
		return nil, err
	}
	run := toRun(row)
	return &run, nil
}

// ListRuns returns one page of run history.
func (r *SourceCatalogRepo) ListRuns(ctx context.Context, query biz.RunQuery) ([]biz.IngestionRun, int, error) {
	q := r.db.IngestionRun.Query()
	if query.PlanID > 0 {
		q = q.Where(ingestionrun.PlanIDEQ(query.PlanID))
	}
	if query.Status != "" {
		q = q.Where(ingestionrun.StatusEQ(query.Status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc("id")).Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.IngestionRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRun(row))
	}
	return out, total, nil
}

// RequestCancel marks a run for cooperative cancellation.
func (r *SourceCatalogRepo) RequestCancel(ctx context.Context, id int64, expectedVersion int) error {
	affected, err := r.db.IngestionRun.Update().
		Where(
			ingestionrun.IDEQ(id),
			ingestionrun.VersionEQ(int32(expectedVersion)),
			ingestionrun.StatusIn(biz.RunStatusQueued, biz.RunStatusRunning, biz.RunStatusWaiting, biz.RunStatusBudget),
		).
		SetCancelRequested(true).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return biz.ErrRunNotCancellable
	}
	return nil
}

// UpdatePlan applies editable fields with an optimistic version check.
func (r *SourceCatalogRepo) UpdatePlan(ctx context.Context, id int64, expectedVersion int, plan biz.IngestionPlan) (*biz.IngestionPlan, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	update := tx.IngestionPlan.Update().
		Where(ingestionplan.IDEQ(id), ingestionplan.VersionEQ(int32(expectedVersion))).
		SetName(plan.Name).
		SetPageStart(int32(plan.PageStart)).
		SetPageEnd(int32(plan.PageEnd)).
		SetRequestBudget(int32(plan.RequestBudget)).
		SetItemBudget(int32(plan.ItemBudget)).
		SetRatePerMinute(int32(plan.RatePerMinute)).
		SetFetchDetails(plan.FetchDetails).
		SetFetchEpisodes(plan.FetchEpisodes).
		SetEnabled(plan.Enabled).
		SetCron(plan.Cron).
		SetTimezone(plan.Timezone).
		AddVersion(1)
	if plan.NextRunAt != nil {
		update = update.SetNextRunAt(*plan.NextRunAt)
	} else {
		update = update.ClearNextRunAt()
	}
	affected, err := update.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if affected == 0 {
		_ = tx.Rollback()
		if ok, _ := r.db.IngestionPlan.Query().Where(ingestionplan.IDEQ(id)).Exist(ctx); !ok {
			return nil, biz.ErrPlanNotFound
		}
		return nil, biz.ErrPlanVersion
	}
	row, err := tx.IngestionPlan.Get(ctx, id)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, row.ID, int(row.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	updated := toPlan(row)
	return &updated, nil
}

// RetryRun requeues a paused/failed run, optionally extending budget. Consumed
// counters and the page cursor stay untouched.
func (r *SourceCatalogRepo) RetryRun(ctx context.Context, runID int64, expectedVersion, addRequests, addItems int, reason string) (*biz.IngestionRun, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.IngestionRun.Query().Where(ingestionrun.IDEQ(runID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRunNotFound
		}
		return nil, err
	}
	if row.Version != int32(expectedVersion) {
		_ = tx.Rollback()
		return nil, biz.ErrRunVersion
	}
	switch row.Status {
	case biz.RunStatusFailed, biz.RunStatusWaiting, biz.RunStatusBudget, biz.RunStatusCancelled:
	default:
		_ = tx.Rollback()
		return nil, biz.ErrRunNotRetryable
	}
	if row.Status == biz.RunStatusBudget && addRequests == 0 && addItems == 0 {
		_ = tx.Rollback()
		return nil, biz.ErrBudgetExtension
	}
	update := row.Update().
		SetStatus(biz.RunStatusQueued).
		SetErrorCode("").
		SetErrorMessage("").
		SetLeaseOwner("").
		ClearLeaseUntil().
		ClearNextRequestAt()
	if addRequests > 0 {
		update = update.AddRequestBudget(int32(addRequests))
	}
	if addItems > 0 {
		update = update.AddItemBudget(int32(addItems))
	}
	updated, err := update.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if addRequests > 0 || addItems > 0 {
		if _, err := tx.IngestionRunBudget.Create().
			SetRunID(runID).
			SetAdditionalRequests(int32(addRequests)).
			SetAdditionalItems(int32(addItems)).
			SetReason(reason).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	run := toRun(updated)
	return &run, nil
}

// ConsumeRequest atomically reserves one outbound request. It persists the
// consumed budget and rolling minute window before the HTTP call, so a page
// interrupted later still records what was actually attempted.
func (r *SourceCatalogRepo) ConsumeRequest(ctx context.Context, runID int64, worker string, version int, now time.Time, lease time.Duration) (biz.RequestDecision, *time.Time, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return "", nil, err
	}
	row, err := tx.IngestionRun.Query().Where(ingestionrun.IDEQ(runID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return "", nil, biz.ErrRunNotFound
		}
		return "", nil, err
	}
	if row.Version != int32(version) || row.LeaseOwner != worker || row.Status != biz.RunStatusRunning ||
		row.LeaseUntil == nil || !row.LeaseUntil.After(now) {
		_ = tx.Rollback()
		return "", nil, biz.ErrRunLeaseLost
	}
	if row.CancelRequested {
		_ = tx.Rollback()
		return biz.RequestCancelled, nil, nil
	}
	rate := row.RatePerMinute
	if rate <= 0 {
		rate = 30
	}
	windowStart := row.RateWindowStart
	windowCount := row.RateWindowCount
	if windowStart == nil || now.Sub(*windowStart) >= time.Minute {
		copied := now
		windowStart = &copied
		windowCount = 0
	}
	if windowCount >= rate {
		next := windowStart.Add(time.Minute)
		if _, err := row.Update().
			SetStatus(biz.RunStatusWaiting).
			SetNextRequestAt(next).
			SetLeaseOwner("").
			ClearLeaseUntil().
			SetRateWindowStart(*windowStart).
			SetRateWindowCount(int32(windowCount)).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return "", nil, err
		}
		if err := tx.Commit(); err != nil {
			return "", nil, err
		}
		return biz.RequestRateLimit, &next, nil
	}
	if row.RequestsUsed+1 > row.RequestBudget {
		if _, err := row.Update().
			SetStatus(biz.RunStatusBudget).
			SetErrorCode(string(biz.SourceCodeBudget)).
			SetErrorMessage("request budget exhausted").
			SetLeaseOwner("").
			ClearLeaseUntil().
			ClearNextRequestAt().
			SetRateWindowStart(*windowStart).
			SetRateWindowCount(int32(windowCount)).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return "", nil, err
		}
		if err := tx.Commit(); err != nil {
			return "", nil, err
		}
		return biz.RequestBudget, nil, nil
	}
	if _, err := row.Update().
		AddRequestsUsed(1).
		SetRateWindowStart(*windowStart).
		SetRateWindowCount(int32(windowCount + 1)).
		SetLeaseUntil(now.Add(lease)).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	return biz.RequestAllowed, nil, nil
}

// RenewLease extends the lease of the current owner; a stale worker is
// rejected instead of silently continuing.
func (r *SourceCatalogRepo) RenewLease(ctx context.Context, runID int64, worker string, version int, lease time.Duration) error {
	affected, err := r.db.IngestionRun.Update().
		Where(
			ingestionrun.IDEQ(runID),
			ingestionrun.VersionEQ(int32(version)),
			ingestionrun.LeaseOwnerEQ(worker),
			ingestionrun.StatusEQ(biz.RunStatusRunning),
			ingestionrun.LeaseUntilGT(time.Now().UTC()),
		).
		SetLeaseUntil(time.Now().UTC().Add(lease)).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return biz.ErrRunLeaseLost
	}
	return nil
}

// AddProgress persists counters immediately so an interrupted page keeps what
// it already produced.
func (r *SourceCatalogRepo) AddProgress(ctx context.Context, runID int64, worker string, version int, seen, upserted, details, episodes int) error {
	affected, err := r.db.IngestionRun.Update().
		Where(
			ingestionrun.IDEQ(runID),
			ingestionrun.VersionEQ(int32(version)),
			ingestionrun.LeaseOwnerEQ(worker),
			ingestionrun.StatusEQ(biz.RunStatusRunning),
			ingestionrun.LeaseUntilGT(time.Now().UTC()),
		).
		AddItemsSeen(int32(seen)).
		AddItemsUpserted(int32(upserted)).
		AddDetailFetched(int32(details)).
		AddEpisodesFetched(int32(episodes)).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return biz.ErrRunLeaseLost
	}
	return nil
}

// MarkBudgetExhausted pauses a run without finishing it; consumed budget is
// never reset, and only AddBudget can queue it again.
func (r *SourceCatalogRepo) MarkBudgetExhausted(ctx context.Context, runID int64, worker string, version int, reason string) error {
	affected, err := r.db.IngestionRun.Update().
		Where(
			ingestionrun.IDEQ(runID),
			ingestionrun.VersionEQ(int32(version)),
			ingestionrun.LeaseOwnerEQ(worker),
			ingestionrun.StatusEQ(biz.RunStatusRunning),
		).
		SetStatus(biz.RunStatusBudget).
		SetErrorCode(string(biz.SourceCodeBudget)).
		SetErrorMessage(reason).
		SetLeaseOwner("").
		ClearLeaseUntil().
		ClearNextRequestAt().
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return biz.ErrRunLeaseLost
	}
	return nil
}

// AddBudget appends budget and queues a budget-exhausted/waiting run again.
func (r *SourceCatalogRepo) AddBudget(ctx context.Context, runID int64, extra int) (*biz.IngestionRun, error) {
	if extra <= 0 {
		return nil, biz.ErrPlanBudget
	}
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	row, err := tx.IngestionRun.Query().Where(ingestionrun.IDEQ(runID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRunNotFound
		}
		return nil, err
	}
	if row.Status != biz.RunStatusBudget && row.Status != biz.RunStatusWaiting {
		_ = tx.Rollback()
		return nil, biz.ErrRunNotResumable
	}
	updated, err := row.Update().
		AddRequestBudget(int32(extra)).
		SetStatus(biz.RunStatusQueued).
		SetErrorCode("").
		SetErrorMessage("").
		SetLeaseOwner("").
		ClearLeaseUntil().
		ClearNextRequestAt().
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	run := toRun(updated)
	return &run, nil
}

// CompletePage persists a page checkpoint after the worker processed it.
func (r *SourceCatalogRepo) CompletePage(ctx context.Context, runID int64, worker string, version int, page, seen int, nextRequestAt time.Time) error {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	affected, err := tx.IngestionRun.Update().
		Where(
			ingestionrun.IDEQ(runID),
			ingestionrun.VersionEQ(int32(version)),
			ingestionrun.LeaseOwnerEQ(worker),
			ingestionrun.StatusEQ(biz.RunStatusRunning),
			ingestionrun.LeaseUntilGT(time.Now().UTC()),
		).
		SetPageCursor(int32(page)).
		AddPagesFetched(1).
		SetNextRequestAt(nextRequestAt).
		SetLeaseUntil(time.Now().UTC().Add(90 * time.Second)).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if affected == 0 {
		_ = tx.Rollback()
		return biz.ErrRunLeaseLost
	}
	_, err = tx.IngestionRunPage.Create().
		SetRunID(runID).
		SetPageNo(int32(page)).
		SetStatus("succeeded").
		SetItemsSeen(int32(seen)).
		SetFetchedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil && !ent.IsConstraintError(err) {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// FinishRun closes a run owned by the worker.
func (r *SourceCatalogRepo) FinishRun(ctx context.Context, runID int64, worker string, version int, status, code, reason string) error {
	affected, err := r.db.IngestionRun.Update().
		Where(
			ingestionrun.IDEQ(runID),
			ingestionrun.VersionEQ(int32(version)),
			ingestionrun.LeaseOwnerEQ(worker),
			ingestionrun.StatusEQ(biz.RunStatusRunning),
			ingestionrun.LeaseUntilGT(time.Now().UTC()),
		).
		SetStatus(status).
		SetErrorCode(code).
		SetErrorMessage(reason).
		SetLeaseOwner("").
		SetFinishedAt(time.Now().UTC()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return biz.ErrRunLeaseLost
	}
	return nil
}

// ensureLease rejects a worker whose lease was taken over or expired.
func (r *SourceCatalogRepo) ensureLease(ctx context.Context, tx *ent.Tx, runID int64, worker string, version int, now time.Time) error {
	ok, err := tx.IngestionRun.Query().
		Where(
			ingestionrun.IDEQ(runID),
			ingestionrun.VersionEQ(int32(version)),
			ingestionrun.LeaseOwnerEQ(worker),
			ingestionrun.StatusEQ(biz.RunStatusRunning),
			ingestionrun.LeaseUntilGT(now),
		).
		Exist(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return biz.ErrRunLeaseLost
	}
	return nil
}

// RecordRunItem stores the result of one attempt. It uses the same lease
// fence as every other write, so a worker that lost its lease cannot append
// to the run. Every attempt is a row, so a failed attempt stays visible
// after a successful retry.
func (r *SourceCatalogRepo) RecordRunItem(ctx context.Context, runID int64, worker string, version int, candidateID int64, externalID string, stage string, result string, errorCode string, errorMessage string) error {
	now := time.Now().UTC()
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	if err := r.ensureLease(ctx, tx, runID, worker, version, now); err != nil {
		_ = tx.Rollback()
		return err
	}
	count, err := tx.IngestionRunItem.Query().
		Where(ingestionrunitem.RunIDEQ(runID), ingestionrunitem.CandidateIDEQ(candidateID), ingestionrunitem.StageEQ(stage)).
		Count(ctx)
	if err != nil {
		// The attempt number must not silently fall back to one.
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.IngestionRunItem.Create().
		SetRunID(runID).
		SetCandidateID(candidateID).
		SetCandidateExternalID(externalID).
		SetStage(stage).
		SetResult(result).
		SetErrorCode(errorCode).
		SetErrorMessage(errorMessage).
		SetAttempt(count + 1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ListRunItems reads the rows one run produced, newest first.
func (r *SourceCatalogRepo) ListRunItems(ctx context.Context, runID int64, page int, pageSize int) (*biz.RunItemPage, error) {
	q := r.db.IngestionRunItem.Query().Where(ingestionrunitem.RunIDEQ(runID))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.Order(ent.Asc(ingestionrunitem.FieldID)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]biz.IngestionRunItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, biz.IngestionRunItem{
			ID: row.ID, RunID: row.RunID, CandidateID: row.CandidateID,
			CandidateExternalID: row.CandidateExternalID, Stage: row.Stage, Result: row.Result,
			ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, Attempt: row.Attempt,
			CreatedAt: row.CreatedAt.UTC(),
		})
	}
	return &biz.RunItemPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// UpsertCandidateSummary stores one ranking snapshot without downgrading a
// candidate that already has a complete snapshot. The write is fenced by the
// run lease so an expired worker cannot overwrite newer data.
func (r *SourceCatalogRepo) UpsertCandidateSummary(ctx context.Context, runID int64, worker string, version int, sourceCode, channel string, summary biz.SeriesSummary) (*biz.Candidate, error) {
	now := time.Now().UTC()
	hash := biz.CandidateHash(summary)
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.ensureLease(ctx, tx, runID, worker, version, now); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	row, err := tx.SourceCandidate.Query().
		Where(sourcecandidate.SourceCodeEQ(sourceCode), sourcecandidate.ExternalIDEQ(summary.ExternalID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		row, err = tx.SourceCandidate.Create().
			SetSourceCode(sourceCode).
			SetChannel(channel).
			SetExternalID(summary.ExternalID).
			SetTitle(summary.Title).
			SetCoverURL(summary.CoverURL).
			SetDescription(summary.Description).
			SetTags(summary.Tags).
			SetMetrics(summary.Metrics).
			SetEpisodeCount(int32(summary.EpisodeCount)).
			SetOpenEpisodeCount(int32(summary.OpenEpisodeCount)).
			SetRankNo(int32(summary.Rank)).
			SetContentHash(hash).
			SetCompleteness(biz.CompletenessSummary).
			SetFirstSeenAt(now).
			SetLastSeenAt(now).
			SetLastRunID(runID).
			Save(ctx)
	} else if err == nil {
		update := row.Update().
			SetTitle(summary.Title).
			SetCoverURL(summary.CoverURL).
			SetDescription(summary.Description).
			SetTags(summary.Tags).
			SetMetrics(summary.Metrics).
			SetEpisodeCount(int32(summary.EpisodeCount)).
			SetOpenEpisodeCount(int32(summary.OpenEpisodeCount)).
			SetRankNo(int32(summary.Rank)).
			SetContentHash(hash).
			SetLastSeenAt(now).
			SetLastRunID(runID)
		if row.Channel == "" {
			update = update.SetChannel(channel)
		}
		if row.Completeness == "" {
			update = update.SetCompleteness(biz.CompletenessSummary)
		}
		row, err = update.Save(ctx)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// The row of this run is stored with its result.
	count, err := tx.IngestionRunItem.Query().
		Where(ingestionrunitem.RunIDEQ(runID), ingestionrunitem.CandidateIDEQ(row.ID), ingestionrunitem.StageEQ(biz.RunItemStageSummary)).
		Count(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.IngestionRunItem.Create().
		SetRunID(runID).
		SetCandidateID(row.ID).
		SetCandidateExternalID(summary.ExternalID).
		SetStage(biz.RunItemStageSummary).
		SetResult(biz.RunItemResultSuccess).
		SetAttempt(count + 1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	candidate := toCandidate(row)
	return &candidate, nil
}

// UpsertCandidateDetail stores the full detail and episode snapshot, fenced by
// the run lease.
func (r *SourceCatalogRepo) UpsertCandidateDetail(ctx context.Context, candidateID, runID int64, worker string, version int, detail *biz.SeriesDetail) error {
	now := time.Now().UTC()
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	if err := r.ensureLease(ctx, tx, runID, worker, version, now); err != nil {
		_ = tx.Rollback()
		return err
	}
	completeness := biz.CompletenessDetail
	if len(detail.Episodes) > 0 {
		completeness = biz.CompletenessComplete
	}
	update := tx.SourceCandidate.UpdateOneID(candidateID).
		SetTitle(detail.Title).
		SetDescription(detail.Description).
		SetCoverURL(detail.CoverURL).
		SetTags(detail.Tags).
		SetEpisodeCount(int32(detail.EpisodeCount)).
		SetOpenEpisodeCount(int32(detail.AccessibleCount)).
		SetDetailFetchedAt(now).
		SetCompleteness(completeness)
	if len(detail.Episodes) > 0 {
		update = update.SetEpisodesFetchedAt(now)
	}
	if _, err := update.Save(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	stage := biz.RunItemStageDetail
	externalID := ""
	if candidate, err := tx.SourceCandidate.Get(ctx, candidateID); err == nil {
		externalID = candidate.ExternalID
	}
	count, err := tx.IngestionRunItem.Query().
		Where(ingestionrunitem.RunIDEQ(runID), ingestionrunitem.CandidateIDEQ(candidateID), ingestionrunitem.StageEQ(stage)).
		Count(ctx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.IngestionRunItem.Create().
		SetRunID(runID).
		SetCandidateID(candidateID).
		SetCandidateExternalID(externalID).
		SetStage(stage).
		SetResult(biz.RunItemResultSuccess).
		SetAttempt(count + 1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(detail.Episodes) > 0 {
		if _, err := tx.CandidateEpisode.Delete().Where(candidateepisode.CandidateIDEQ(candidateID)).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return err
		}
		builders := make([]*ent.CandidateEpisodeCreate, 0, len(detail.Episodes))
		for _, episode := range detail.Episodes {
			builders = append(builders, tx.CandidateEpisode.Create().
				SetCandidateID(candidateID).
				SetExternalID(episode.ExternalID).
				SetEpisodeNo(int32(episode.Number)).
				SetCreatedAt(now))
		}
		if err := tx.CandidateEpisode.CreateBulk(builders...).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ListCandidates reads local snapshots only.
func (r *SourceCatalogRepo) ListCandidates(ctx context.Context, query biz.CandidateQuery) (*biz.CandidatePage, error) {
	q := r.db.SourceCandidate.Query()
	if query.SourceCode != "" {
		q = q.Where(sourcecandidate.SourceCodeEQ(query.SourceCode))
	}
	if query.Keyword != "" {
		q = q.Where(sourcecandidate.TitleContainsFold(query.Keyword))
	}
	if query.Completeness != "" {
		q = q.Where(sourcecandidate.CompletenessEQ(query.Completeness))
	}
	if query.Imported != nil {
		if *query.Imported {
			q = q.Where(sourcecandidate.SeriesIDNotNil())
		} else {
			q = q.Where(sourcecandidate.SeriesIDIsNil())
		}
	}
	if query.RunID != nil {
		q = q.Where(sourcecandidate.LastRunIDEQ(*query.RunID))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.Order(ent.Asc("rank_no"), ent.Asc("id")).
		Offset((query.Page - 1) * query.PageSize).
		Limit(query.PageSize).
		All(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]biz.Candidate, 0, len(rows))
	for _, row := range rows {
		items = append(items, toCandidate(row))
	}
	return &biz.CandidatePage{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

// GetCandidate reads one local snapshot.
func (r *SourceCatalogRepo) GetCandidate(ctx context.Context, id int64) (*biz.Candidate, error) {
	row, err := r.db.SourceCandidate.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, biz.ErrCandidateNotFound
	}
	if err != nil {
		return nil, err
	}
	candidate := toCandidate(row)
	return &candidate, nil
}

// GetCandidateByExternal looks up one local snapshot by source identity.
func (r *SourceCatalogRepo) GetCandidateByExternal(ctx context.Context, sourceCode, externalID string) (*biz.Candidate, error) {
	row, err := r.db.SourceCandidate.Query().
		Where(sourcecandidate.SourceCodeEQ(sourceCode), sourcecandidate.ExternalIDEQ(externalID)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, biz.ErrCandidateNotFound
	}
	if err != nil {
		return nil, err
	}
	candidate := toCandidate(row)
	return &candidate, nil
}

// ListCandidateEpisodes reads the collected episode snapshot.
func (r *SourceCatalogRepo) ListCandidateEpisodes(ctx context.Context, candidateID int64) ([]biz.CandidateEpisode, error) {
	rows, err := r.db.CandidateEpisode.Query().
		Where(candidateepisode.CandidateIDEQ(candidateID)).
		Order(ent.Asc("episode_no")).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]biz.CandidateEpisode, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.CandidateEpisode{ExternalID: row.ExternalID, Number: int(row.EpisodeNo), Title: row.Title})
	}
	return out, nil
}

// CreateSeriesFromCandidate copies the complete local snapshot into a draft
// series and revision. It performs no network call.
func (r *SourceCatalogRepo) CreateSeriesFromCandidate(ctx context.Context, candidateID int64) (*biz.SeriesView, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	candidate, err := tx.SourceCandidate.Get(ctx, candidateID)
	if ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, biz.ErrCandidateNotFound
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if candidate.SeriesID != nil && *candidate.SeriesID > 0 {
		_ = tx.Rollback()
		return r.GetSeriesDetail(ctx, *candidate.SeriesID)
	}
	episodes, err := tx.CandidateEpisode.Query().
		Where(candidateepisode.CandidateIDEQ(candidateID)).
		Order(ent.Asc("episode_no")).
		All(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if len(episodes) == 0 {
		_ = tx.Rollback()
		return nil, biz.ErrCandidateIncomplete
	}
	series, err := tx.ContentSeries.Create().
		SetSourceCode(candidate.SourceCode).
		SetExternalID(candidate.ExternalID).
		SetCandidateID(candidate.ID).
		SetTitle(candidate.Title).
		SetDescription(candidate.Description).
		SetCoverURL(candidate.CoverURL).
		SetStatus("draft").
		SetRevision(1).
		SetVersion(1).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	revision, err := tx.ContentRevision.Create().
		SetSeriesID(series.ID).
		SetRevisionNo(1).
		SetState("draft").
		SetVersion(1).
		SetTitle(candidate.Title).
		SetDescription(candidate.Description).
		SetCoverURL(candidate.CoverURL).
		SetTags(candidate.Tags).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	builders := make([]*ent.RevisionEpisodeCreate, 0, len(episodes))
	for _, episode := range episodes {
		builders = append(builders, tx.RevisionEpisode.Create().
			SetRevisionID(revision.ID).
			SetExternalID(episode.ExternalID).
			SetEpisodeNo(episode.EpisodeNo).
			SetSelected(false))
	}
	if err := tx.RevisionEpisode.CreateBulk(builders...).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := series.Update().SetCurrentDraftRevisionID(revision.ID).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := candidate.Update().SetSeriesID(series.ID).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, series.ID, int(series.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.GetSeriesDetail(ctx, series.ID)
}

// CreateCompletionRun queues a one-candidate detail/episode completion task.
func (r *SourceCatalogRepo) CreateCompletionRun(ctx context.Context, candidateID int64) (*biz.IngestionRun, error) {
	candidate, err := r.db.SourceCandidate.Get(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	row, err := r.db.IngestionRun.Create().
		SetKind(biz.RunKindCompletion).
		SetCandidateID(candidateID).
		SetSourceCode(candidate.SourceCode).
		SetChannel(candidate.Channel).
		SetPageStart(0).
		SetPageEnd(0).
		SetStatus(biz.RunStatusQueued).
		SetRequestBudget(4).
		SetItemBudget(1).
		SetRatePerMinute(30).
		SetPageCursor(0).
		SetVersion(1).
		SetStartedAt(time.Now().UTC()).
		SetPlanSnapshot(map[string]any{"kind": biz.RunKindCompletion, "candidate_id": candidateID, "fetch_details": true, "fetch_episodes": true}).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	run := toRun(row)
	return &run, nil
}

// ListSeries returns one page of the content list with the working draft and
// the published revision of every series.
func (r *SourceCatalogRepo) ListSeries(ctx context.Context, query biz.ContentSeriesQuery) ([]biz.ContentSeriesItem, int, error) {
	q := r.db.ContentSeries.Query()
	if query.Keyword != "" {
		q = q.Where(contentseries.Or(
			contentseries.TitleContains(query.Keyword),
			contentseries.DescriptionContains(query.Keyword),
		))
	}
	if query.Status != "" {
		q = q.Where(contentseries.StatusEQ(query.Status))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(contentseries.FieldID)).
		Offset((query.Page - 1) * query.PageSize).
		Limit(query.PageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	items := make([]biz.ContentSeriesItem, 0, len(rows))
	for _, row := range rows {
		item := biz.ContentSeriesItem{
			SeriesID: row.ID, Title: row.Title, Description: row.Description,
			Status: row.Status, Version: int(row.Version), Published: row.PublishedRevisionID != nil && *row.PublishedRevisionID > 0,
			UpdatedAt: row.UpdatedAt.UTC(),
		}
		var draftID int64
		if row.CurrentDraftRevisionID != nil {
			draftID = *row.CurrentDraftRevisionID
		}
		if draftID > 0 {
			if draft, err := r.db.ContentRevision.Get(ctx, draftID); err == nil {
				item.RevisionID = draft.ID
				item.RevisionNo = int(draft.RevisionNo)
				item.RevisionState = draft.State
				item.Title = draft.Title
				item.Description = draft.Description
				item.UpdatedAt = draft.UpdatedAt.UTC()
			}
		}
		if item.RevisionID > 0 {
			if count, err := r.db.RevisionEpisode.Query().
				Where(revisionepisode.RevisionIDEQ(item.RevisionID), revisionepisode.SelectedEQ(true)).
				Count(ctx); err == nil {
				item.EpisodeCount = count
			}
		}
		items = append(items, item)
	}
	return items, total, nil
}

// CreateRevision creates a new working draft for a series. The series row is
// locked, so two concurrent requests cannot produce two working drafts. The
// new draft inherits the metadata, the selection and the frozen material of
// the base revision; it never inherits an approval, a decision or a
// publication.
func (r *SourceCatalogRepo) CreateRevision(ctx context.Context, seriesID int64, baseRevisionID int64, replaceDraftRevisionID int64, replaceDraftVersion int) (*biz.SeriesView, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	series, err := tx.ContentSeries.Query().Where(contentseries.IDEQ(seriesID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrSeriesNotFound
		}
		return nil, err
	}
	// The base revision has to belong to this series. The check comes
	// first: a request that names a revision of another series is refused
	// before anything is written.
	base := series.CurrentDraftRevisionID
	if baseRevisionID > 0 {
		base = &baseRevisionID
	}
	if base == nil {
		if series.PublishedRevisionID != nil {
			base = series.PublishedRevisionID
		} else {
			_ = tx.Rollback()
			return nil, biz.ErrRevisionNotFound
		}
	}
	baseRow, err := tx.ContentRevision.Query().
		Where(contentrevision.IDEQ(*base), contentrevision.SeriesIDEQ(seriesID)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionConflict
		}
		return nil, err
	}
	// The pointer alone does not make a draft: a revision that was approved
	// or rejected is history, not an editable draft.
	var currentDraft *biz.ContentRevision
	if series.CurrentDraftRevisionID != nil {
		row, err := tx.ContentRevision.Query().Where(contentrevision.IDEQ(*series.CurrentDraftRevisionID)).Only(ctx)
		if err != nil {
			if !ent.IsNotFound(err) {
				_ = tx.Rollback()
				return nil, err
			}
		} else {
			currentDraft = &biz.ContentRevision{ID: row.ID, State: row.State, Version: int(row.Version)}
		}
	}
	if currentDraft != nil && currentDraft.State == biz.RevisionStateDraft {
		// An editable draft is never replaced silently: the caller names it
		// and confirms the version it read.
		if replaceDraftRevisionID != currentDraft.ID {
			_ = tx.Rollback()
			return nil, biz.ErrDraftExists
		}
		if replaceDraftVersion != currentDraft.Version {
			_ = tx.Rollback()
			return nil, biz.ErrRevisionConflict
		}
	}
	// The revision number grows, it is never reused.
	rows, err := tx.ContentRevision.Query().
		Where(contentrevision.SeriesIDEQ(seriesID)).
		Order(ent.Desc(contentrevision.FieldRevisionNo)).
		Limit(1).
		All(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	nextNo := int32(1)
	if len(rows) > 0 {
		nextNo = int32(rows[0].RevisionNo) + 1
	}
	created, err := tx.ContentRevision.Create().
		SetSeriesID(seriesID).
		SetRevisionNo(nextNo).
		// A new draft starts as a draft; it inherits no approval.
		SetState(biz.RevisionStateDraft).
		SetVersion(1).
		SetTitle(baseRow.Title).
		SetDescription(baseRow.Description).
		SetCoverURL(baseRow.CoverURL).
		SetTags(baseRow.Tags).
		SetCoverAssetID(baseRow.CoverAssetID).
		// A new draft inherits no authorization snapshot: the operator
		// chooses a grant explicitly when the draft is submitted. Keeping the
		// identifier of the base grant with a zero version would look like a
		// frozen authorization that was never confirmed.
		SetAuthorizationVersion(0).
		SetAuthorizationHash("").
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// The selection and the frozen material travel with the new draft.
	episodes, err := tx.RevisionEpisode.Query().Where(revisionepisode.RevisionIDEQ(baseRow.ID)).All(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	for _, episode := range episodes {
		if _, err := tx.RevisionEpisode.Create().
			SetRevisionID(created.ID).
			SetNillableExternalID(episode.ExternalID).
			SetEpisodeNo(episode.EpisodeNo).
			SetSelected(episode.Selected).
			SetInputKind(string(episode.InputKind)).
			SetInputRef(episode.InputRef).
			SetInputState(episode.InputState).
			// The material stays the one the revision froze.
			SetAssetID(episode.AssetID).
			SetAssetState(episode.AssetState).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if _, err := tx.ContentSeries.UpdateOneID(seriesID).
		SetCurrentDraftRevisionID(created.ID).
		SetRevision(nextNo).
		AddVersion(1).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, created.ID, int(created.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.GetSeriesDetail(ctx, seriesID)
}

// UpdateRevision edits a draft revision with a version check. A revision that
// is not a draft is refused, and the published revision is never touched.
func (r *SourceCatalogRepo) UpdateRevision(ctx context.Context, seriesID int64, revisionID int64, expectedVersion int, setTitle bool, title string, setDescription bool, description string) (*biz.SeriesView, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := tx.ContentRevision.Query().Where(contentrevision.IDEQ(revisionID)).ForUpdate().Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionConflict
		}
		return nil, err
	}
	if revision.SeriesID != seriesID || revision.State != biz.RevisionStateDraft {
		// Only a working draft may change.
		_ = tx.Rollback()
		return nil, biz.ErrRevisionConflict
	}
	series, err := tx.ContentSeries.Get(ctx, seriesID)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return nil, biz.ErrRevisionConflict
		}
		return nil, err
	}
	if series.CurrentDraftRevisionID == nil || *series.CurrentDraftRevisionID != revisionID {
		// The revision is not the working draft of the series any more.
		_ = tx.Rollback()
		return nil, biz.ErrRevisionConflict
	}
	if int(revision.Version) != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrRevisionConflict
	}
	updated := revision.Update()
	if setTitle {
		updated = updated.SetTitle(title)
	}
	if setDescription {
		updated = updated.SetDescription(description)
	}
	row, err := updated.AddVersion(1).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			// The row changed while the transaction held the lock: the
			// version of the draft is stale.
			return nil, biz.ErrRevisionConflict
		}
		return nil, err
	}
	auditResource(ctx, row.ID, int(row.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return r.GetSeriesDetail(ctx, seriesID)
}

// GetSeriesDetail returns a series with its draft revision and episodes.
func (r *SourceCatalogRepo) GetSeriesDetail(ctx context.Context, seriesID int64) (*biz.SeriesView, error) {
	series, err := r.db.ContentSeries.Get(ctx, seriesID)
	if ent.IsNotFound(err) {
		return nil, biz.ErrSeriesNotFound
	}
	if err != nil {
		return nil, err
	}
	if series.CurrentDraftRevisionID == nil {
		return nil, biz.ErrRevisionNotFound
	}
	revision, err := r.db.ContentRevision.Get(ctx, *series.CurrentDraftRevisionID)
	if ent.IsNotFound(err) {
		return nil, biz.ErrRevisionNotFound
	}
	if err != nil {
		return nil, err
	}
	episodes, selected, err := r.revisionEpisodes(ctx, revision.ID)
	if err != nil {
		return nil, err
	}
	var published *biz.ContentRevision
	if series.PublishedRevisionID != nil {
		if row, err := r.db.ContentRevision.Get(ctx, *series.PublishedRevisionID); err == nil {
			published = &biz.ContentRevision{
				ID: row.ID, SeriesID: row.SeriesID, RevisionNo: int(row.RevisionNo),
				State: row.State, Version: int(row.Version), Title: row.Title,
				Description: row.Description, CoverURL: row.CoverURL, Tags: row.Tags,
			}
		}
	}
	return &biz.SeriesView{
		Series: &biz.ContentSeries{
			ID: series.ID, SourceCode: series.SourceCode, ExternalID: textValue(series.ExternalID),
			CandidateID: valueOrZero(series.CandidateID), Title: series.Title,
			Description: series.Description, CoverURL: series.CoverURL, Status: series.Status,
			CurrentDraftRevisionID: revision.ID, PublishedRevisionID: valueOrZero(series.PublishedRevisionID),
			Version: int(series.Version),
		},
		PublishedRevision: published,
		Revision: &biz.ContentRevision{
			ID: revision.ID, SeriesID: revision.SeriesID, RevisionNo: int(revision.RevisionNo),
			State: revision.State, Version: int(revision.Version), Title: revision.Title,
			Description: revision.Description, CoverURL: revision.CoverURL, Tags: revision.Tags,
			SelectedCount: selected,
		},
		Episodes: episodes,
	}, nil
}

// ListRevisionEpisodes returns one page of a draft revision.
func (r *SourceCatalogRepo) ListRevisionEpisodes(ctx context.Context, revisionID int64, page, pageSize int) ([]biz.RevisionEpisode, int, error) {
	total, err := r.db.RevisionEpisode.Query().Where(revisionepisode.RevisionIDEQ(revisionID)).Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := r.db.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID)).
		Order(ent.Asc("episode_no")).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.RevisionEpisode, 0, len(rows))
	for _, row := range rows {
		out = append(out, biz.RevisionEpisode{ID: row.ID, ExternalID: textValue(row.ExternalID), Number: int(row.EpisodeNo), Selected: row.Selected})
	}
	return out, total, nil
}

// SetRevisionSelection replaces the selected set under a version check.
func (r *SourceCatalogRepo) SetRevisionSelection(ctx context.Context, revisionID int64, expectedVersion int, mode string, externalIDs []string) (*biz.ContentRevision, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	revision, err := tx.ContentRevision.Get(ctx, revisionID)
	if ent.IsNotFound(err) {
		_ = tx.Rollback()
		return nil, biz.ErrRevisionNotFound
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if int(revision.Version) != expectedVersion {
		_ = tx.Rollback()
		return nil, biz.ErrRevisionVersion
	}
	if revision.State != "draft" {
		_ = tx.Rollback()
		return nil, biz.ErrRevisionVersion
	}
	if mode == "explicit" {
		count, err := tx.RevisionEpisode.Query().
			Where(
				revisionepisode.RevisionIDEQ(revisionID),
				revisionepisode.ExternalIDIn(externalIDs...),
			).
			Count(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if count != len(externalIDs) {
			_ = tx.Rollback()
			return nil, biz.ErrSelectionMismatch
		}
	}
	if _, err := tx.RevisionEpisode.Update().
		Where(revisionepisode.RevisionIDEQ(revisionID)).
		SetSelected(mode == "all").
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if mode == "explicit" && len(externalIDs) > 0 {
		if _, err := tx.RevisionEpisode.Update().
			Where(revisionepisode.RevisionIDEQ(revisionID), revisionepisode.ExternalIDIn(externalIDs...)).
			SetSelected(true).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	updated, err := revision.Update().AddVersion(1).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	selected, err := tx.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID), revisionepisode.SelectedEQ(true)).
		Count(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	auditResource(ctx, updated.ID, int(updated.Version))
	if err := commitAudited(ctx, tx); err != nil {
		return nil, err
	}
	return &biz.ContentRevision{
		ID: updated.ID, SeriesID: updated.SeriesID, RevisionNo: int(updated.RevisionNo),
		State: updated.State, Version: int(updated.Version), Title: updated.Title,
		Description: updated.Description, CoverURL: updated.CoverURL, Tags: updated.Tags,
		SelectedCount: selected,
	}, nil
}

func (r *SourceCatalogRepo) revisionEpisodes(ctx context.Context, revisionID int64) ([]biz.RevisionEpisode, int, error) {
	rows, err := r.db.RevisionEpisode.Query().
		Where(revisionepisode.RevisionIDEQ(revisionID)).
		Order(ent.Asc("episode_no")).
		All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.RevisionEpisode, 0, len(rows))
	selected := 0
	for _, row := range rows {
		if row.Selected {
			selected++
		}
		out = append(out, biz.RevisionEpisode{ID: row.ID, ExternalID: textValue(row.ExternalID), Number: int(row.EpisodeNo), Selected: row.Selected})
	}
	return out, selected, nil
}

func toPlan(row *ent.IngestionPlan) biz.IngestionPlan {
	return biz.IngestionPlan{
		ID: row.ID, Code: row.Code, Name: row.Name, SourceCode: row.SourceCode,
		Channel: row.Channel, PageStart: int(row.PageStart), PageEnd: int(row.PageEnd),
		RequestBudget: int(row.RequestBudget), ItemBudget: int(row.ItemBudget), RatePerMinute: int(row.RatePerMinute),
		FetchDetails: row.FetchDetails, FetchEpisodes: row.FetchEpisodes,
		Enabled: row.Enabled, Version: int(row.Version),
		Cron: row.Cron, Timezone: row.Timezone, NextRunAt: row.NextRunAt,
	}
}

func toRun(row *ent.IngestionRun) biz.IngestionRun {
	run := biz.IngestionRun{
		ID: row.ID, PlanID: row.PlanID, Kind: row.Kind, CandidateID: valueOrZero(row.CandidateID),
		SourceCode: row.SourceCode, Channel: row.Channel, PageStart: int(row.PageStart),
		PageEnd: int(row.PageEnd), Status: row.Status, RequestBudget: int(row.RequestBudget),
		ItemBudget: int(row.ItemBudget), RequestsUsed: int(row.RequestsUsed),
		RatePerMinute: int(row.RatePerMinute), RateWindowStart: row.RateWindowStart, RateWindowCount: int(row.RateWindowCount),
		PageCursor: int(row.PageCursor), PagesFetched: int(row.PagesFetched),
		ItemsSeen: int(row.ItemsSeen), ItemsUpserted: int(row.ItemsUpserted),
		DetailFetched: int(row.DetailFetched), EpisodesFetched: int(row.EpisodesFetched),
		CancelRequested: row.CancelRequested, ErrorCode: row.ErrorCode, ErrorReason: row.ErrorMessage,
		TraceID: row.TraceID,
		Version: int(row.Version), StartedAt: row.StartedAt,
	}
	if row.PlanSnapshot != nil {
		if value, ok := row.PlanSnapshot["fetch_details"].(bool); ok {
			run.FetchDetails = value
		}
		if value, ok := row.PlanSnapshot["channel_base_url"].(string); ok {
			run.ChannelBaseURL = value
		}
		if value, ok := row.PlanSnapshot["channel_timeout_seconds"].(float64); ok && value > 0 {
			run.ChannelTimeout = time.Duration(value) * time.Second
		}
		if value, ok := row.PlanSnapshot["channel_timeout_seconds"].(int); ok && value > 0 {
			run.ChannelTimeout = time.Duration(value) * time.Second
		}
		if value, ok := row.PlanSnapshot["fetch_episodes"].(bool); ok {
			run.FetchEpisodes = value
		}
	}
	if row.FinishedAt != nil {
		run.FinishedAt = row.FinishedAt
	}
	if row.NextRequestAt != nil {
		run.NextRequestAt = row.NextRequestAt
	}
	return run
}

func toCandidate(row *ent.SourceCandidate) biz.Candidate {
	candidate := biz.Candidate{
		ID: row.ID, SourceCode: row.SourceCode, Channel: row.Channel, ExternalID: row.ExternalID,
		Title: row.Title, CoverURL: row.CoverURL, Description: row.Description, Tags: row.Tags,
		EpisodeCount: int(row.EpisodeCount), OpenEpisodeCount: int(row.OpenEpisodeCount),
		Rank: int(row.RankNo), Metrics: row.Metrics, ContentHash: row.ContentHash,
		Completeness: row.Completeness, SeriesID: valueOrZero(row.SeriesID), LastRunID: valueOrZero(row.LastRunID),
		FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
	}
	if row.DetailFetchedAt != nil {
		candidate.DetailFetchedAt = row.DetailFetchedAt
	}
	if row.EpisodesFetchedAt != nil {
		candidate.EpisodesFetchedAt = row.EpisodesFetchedAt
	}
	return candidate
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// textValue reads an optional text column. A manual work keeps NULL where an
// imported one carries the provider identifier, and the domain model uses an
// empty string for both, so the rest of the code does not branch on it.
func textValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
