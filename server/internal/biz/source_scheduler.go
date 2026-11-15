package biz

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// PlanSchedulerConfig bounds one scheduler loop.
type PlanSchedulerConfig struct {
	PollInterval time.Duration
	BatchSize    int
	// Now allows deterministic tests to drive the clock.
	Now func() time.Time
}

// PlanScheduler fires plans on their cron schedule. It only claims a schedule
// slot; the run itself is created inside a repository transaction, so a
// scheduled run and a manual trigger never produce two concurrent runs for
// the same plan.
type PlanScheduler struct {
	repo SourceCatalogRepo
	cfg  PlanSchedulerConfig
	log  *slog.Logger
}

// NewPlanScheduler validates dependencies and defaults.
func NewPlanScheduler(repo SourceCatalogRepo, cfg PlanSchedulerConfig, logger *slog.Logger) (*PlanScheduler, error) {
	if repo == nil {
		return nil, errors.New("plan scheduler requires repo")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PlanScheduler{repo: repo, cfg: cfg, log: logger}, nil
}

// Run polls for due plans until the context is cancelled.
func (s *PlanScheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		created, err := s.ProcessOnce(ctx)
		if err != nil {
			s.log.Error("plan schedule poll failed", "err", err)
		} else if created > 0 {
			s.log.Info("scheduled collection runs queued", "count", created)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessOnce fires every due plan at most once. A schedule slot is claimed
// with a compare-and-set on the stored fire time, so two schedulers cannot
// both act on the same slot.
func (s *PlanScheduler) ProcessOnce(ctx context.Context) (int, error) {
	now := s.cfg.Now().UTC()
	plans, err := s.repo.DuePlans(ctx, now, s.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	created := 0
	for index := range plans {
		plan := plans[index]
		if plan.NextRunAt == nil {
			continue
		}
		next, err := NextRunAfter(plan.Cron, plan.Timezone, now)
		if err != nil {
			s.log.Error("plan schedule is invalid", "plan_id", plan.ID, "cron", plan.Cron, "err", err)
			continue
		}
		claimed, err := s.repo.AdvancePlanSchedule(ctx, plan.ID, *plan.NextRunAt, next)
		if err != nil {
			s.log.Error("advance plan schedule failed", "plan_id", plan.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		run, err := s.repo.CreateRun(ctx, &plan, RunKindRanking, 0)
		if err != nil {
			if errors.Is(err, ErrRunAlreadyActive) {
				s.log.Info("scheduled run skipped; plan already has an active run", "plan_id", plan.ID)
				continue
			}
			if errors.Is(err, ErrSourcePaused) || errors.Is(err, ErrChannelNotFound) {
				// The slot is consumed and no run is created: pausing an entry
				// stops scheduled collection without touching stored data.
				s.log.Info("scheduled run skipped; the channel is paused or unknown",
					"plan_id", plan.ID, "err", err)
				continue
			}
			s.log.Error("scheduled run failed", "plan_id", plan.ID, "err", err)
			continue
		}
		created++
		s.log.Info("scheduled run queued", "plan_id", plan.ID, "run_id", run.ID, "next_run_at", next)
	}
	return created, nil
}
