package biz_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"openduanju-server/internal/biz"
)

func TestScheduleValidationRejectsBadExpression(t *testing.T) {
	if err := biz.ValidateSchedule("not a cron", "Asia/Shanghai"); !errors.Is(err, biz.ErrPlanSchedule) {
		t.Fatalf("invalid expression error = %v", err)
	}
	if err := biz.ValidateSchedule("", "Asia/Shanghai"); !errors.Is(err, biz.ErrPlanSchedule) {
		t.Fatalf("empty expression error = %v", err)
	}
	if err := biz.ValidateSchedule("0 3 * * *", "Mars/Olympus"); !errors.Is(err, biz.ErrPlanSchedule) {
		t.Fatalf("invalid timezone error = %v", err)
	}
	if err := biz.ValidateSchedule("0 3 * * *", "Asia/Shanghai"); err != nil {
		t.Fatalf("valid schedule error = %v", err)
	}
}

func TestNextRunAfterUsesPlanTimezone(t *testing.T) {
	from := time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC)
	next, err := biz.NextRunAfter("30 3 * * *", "Asia/Shanghai", from)
	if err != nil {
		t.Fatalf("NextRunAfter: %v", err)
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	local := next.In(shanghai)
	if local.Hour() != 3 || local.Minute() != 30 {
		t.Fatalf("next run local time = %s", local.Format(time.RFC3339))
	}
	if !next.After(from) {
		t.Fatalf("next run %s must be after %s", next, from)
	}
}

func TestNextRunAfterRejectsUnknownTimezone(t *testing.T) {
	if _, err := biz.NextRunAfter("0 * * * *", "Mars/Olympus", time.Now()); !errors.Is(err, biz.ErrPlanSchedule) {
		t.Fatalf("timezone error = %v", err)
	}
}

func TestSourceCatalogRejectsInvalidSchedule(t *testing.T) {
	uc := newValidationCatalog(t)
	plan := biz.IngestionPlan{
		Code: "p", Name: "\u8ba1\u5212", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 10,
		Cron: "every minute please",
	}
	if _, err := uc.CreatePlan(context.Background(), plan); !errors.Is(err, biz.ErrPlanSchedule) {
		t.Fatalf("schedule error = %v", err)
	}
}

type schedulerRepo struct {
	biz.SourceCatalogRepo
	due       []biz.IngestionPlan
	created   []biz.IngestionRun
	advances  int
	advanceTo time.Time
	createErr error
}

func (r *schedulerRepo) DuePlans(context.Context, time.Time, int) ([]biz.IngestionPlan, error) {
	return r.due, nil
}

func (r *schedulerRepo) AdvancePlanSchedule(_ context.Context, _ int64, _, nextRunAt time.Time) (bool, error) {
	r.advances++
	r.advanceTo = nextRunAt
	return true, nil
}

func (r *schedulerRepo) CreateRun(_ context.Context, plan *biz.IngestionPlan, _ string, _ int64) (*biz.IngestionRun, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	run := biz.IngestionRun{ID: int64(len(r.created) + 1), PlanID: plan.ID, Status: biz.RunStatusQueued}
	r.created = append(r.created, run)
	return &run, nil
}

func TestPlanSchedulerFiresDuePlanOnce(t *testing.T) {
	due := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	repo := &schedulerRepo{
		due: []biz.IngestionPlan{{
			ID: 7, Code: "p", Name: "\u8ba1\u5212", SourceCode: "hongguo", Channel: "hot-drama",
			Cron: "0 3 * * *", Timezone: "Asia/Shanghai", NextRunAt: &due,
		}},
	}
	scheduler, err := biz.NewPlanScheduler(repo, biz.PlanSchedulerConfig{
		Now: func() time.Time { return due.Add(time.Minute) },
	}, nil)
	if err != nil {
		t.Fatalf("NewPlanScheduler: %v", err)
	}
	created, err := scheduler.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if created != 1 || len(repo.created) != 1 || repo.created[0].PlanID != 7 {
		t.Fatalf("created = %d, runs = %v", created, repo.created)
	}
	if !repo.advanceTo.After(due) {
		t.Fatalf("schedule must advance past %s, got %s", due, repo.advanceTo)
	}
}

func TestPlanSchedulerSkipsPlanWithActiveRun(t *testing.T) {
	due := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	repo := &schedulerRepo{
		due: []biz.IngestionPlan{{
			ID: 7, Code: "p", Name: "\u8ba1\u5212", SourceCode: "hongguo", Channel: "hot-drama",
			Cron: "0 3 * * *", Timezone: "Asia/Shanghai", NextRunAt: &due,
		}},
		createErr: biz.ErrRunAlreadyActive,
	}
	scheduler, err := biz.NewPlanScheduler(repo, biz.PlanSchedulerConfig{
		Now: func() time.Time { return due.Add(time.Minute) },
	}, nil)
	if err != nil {
		t.Fatalf("NewPlanScheduler: %v", err)
	}
	created, err := scheduler.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if created != 0 || len(repo.created) != 0 {
		t.Fatalf("a plan with an active run must not be fired again: %v", repo.created)
	}
	if repo.advances != 1 {
		t.Fatalf("the schedule slot must still be consumed once, advances = %d", repo.advances)
	}
}

func TestPlanSchedulerReportsRepositoryFailure(t *testing.T) {
	repo := &schedulerRepo{}
	repo.createErr = errors.New("boom")
	scheduler, err := biz.NewPlanScheduler(repo, biz.PlanSchedulerConfig{}, nil)
	if err != nil {
		t.Fatalf("NewPlanScheduler: %v", err)
	}
	if _, err := scheduler.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
}
