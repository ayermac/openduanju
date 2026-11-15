package server

import (
	"context"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"

	"github.com/go-kratos/kratos/v3/transport"
)

// NewPlanSchedulerConfig derives scheduler bounds from the source configuration.
func NewPlanSchedulerConfig(c *conf.Source) biz.PlanSchedulerConfig {
	cfg := biz.PlanSchedulerConfig{
		PollInterval: 10 * time.Second,
		BatchSize:    20,
	}
	if scheduler := c.GetScheduler(); scheduler != nil {
		if d := scheduler.GetPollInterval(); d != nil && d.AsDuration() > 0 {
			cfg.PollInterval = d.AsDuration()
		}
		if size := scheduler.GetBatchSize(); size > 0 {
			cfg.BatchSize = int(size)
		}
	}
	return cfg
}

// SchedulerServer adapts the plan scheduler to the Kratos lifecycle.
type SchedulerServer struct {
	scheduler *biz.PlanScheduler
}

// NewSchedulerServer wraps one scheduler.
func NewSchedulerServer(scheduler *biz.PlanScheduler) *SchedulerServer {
	return &SchedulerServer{scheduler: scheduler}
}

// Start runs the scheduling loop until the application context is cancelled.
func (s *SchedulerServer) Start(ctx context.Context) error {
	s.scheduler.Run(ctx)
	return nil
}

// Stop is a no-op because Run observes the application context.
func (s *SchedulerServer) Stop(context.Context) error { return nil }

var _ transport.Server = (*SchedulerServer)(nil)
