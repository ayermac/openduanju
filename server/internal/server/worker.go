package server

import (
	"context"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"

	"github.com/go-kratos/kratos/v3/transport"
)

// NewIngestWorkerConfig derives worker bounds from the source configuration.
func NewIngestWorkerConfig(c *conf.Source) biz.IngestWorkerConfig {
	cfg := biz.IngestWorkerConfig{
		WorkerID:       "ingest-1",
		PollInterval:   2 * time.Second,
		Lease:          90 * time.Second,
		PageInterval:   time.Second,
		DetailInterval: 300 * time.Millisecond,
	}
	if worker := c.GetWorker(); worker != nil {
		if d := worker.GetPollInterval(); d != nil && d.AsDuration() > 0 {
			cfg.PollInterval = d.AsDuration()
		}
		if d := worker.GetLease(); d != nil && d.AsDuration() > 0 {
			cfg.Lease = d.AsDuration()
		}
		if d := worker.GetPageInterval(); d != nil {
			cfg.PageInterval = d.AsDuration()
		}
		if d := worker.GetDetailInterval(); d != nil {
			cfg.DetailInterval = d.AsDuration()
		}
	}
	return cfg
}

// WorkerServer adapts the persisted ingest worker to the Kratos lifecycle.
type WorkerServer struct {
	worker *biz.IngestWorker
}

// NewWorkerServer wraps one worker.
func NewWorkerServer(worker *biz.IngestWorker) *WorkerServer {
	return &WorkerServer{worker: worker}
}

// Start runs the polling loop until the application context is cancelled.
func (s *WorkerServer) Start(ctx context.Context) error {
	s.worker.Run(ctx)
	return nil
}

// Stop is a no-op because Run observes the application context.
func (s *WorkerServer) Stop(context.Context) error { return nil }

var _ transport.Server = (*WorkerServer)(nil)
