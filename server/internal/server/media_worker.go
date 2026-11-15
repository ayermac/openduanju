package server

import (
	"context"

	"openduanju-server/internal/biz"

	"github.com/go-kratos/kratos/v3/transport"
)

// MediaWorkerServer adapts the persistent media worker to the Kratos
// lifecycle.
type MediaWorkerServer struct {
	worker *biz.MediaWorker
}

// NewMediaWorkerServer wraps one media worker.
func NewMediaWorkerServer(worker *biz.MediaWorker) *MediaWorkerServer {
	return &MediaWorkerServer{worker: worker}
}

// Start runs the media worker until the application context is cancelled. A
// deployment without a usable media configuration has no worker; the admin
// API keeps serving and the operator sees the reason in the start-up log.
func (s *MediaWorkerServer) Start(ctx context.Context) error {
	if s.worker == nil {
		return nil
	}
	s.worker.Run(ctx)
	return nil
}

// Stop is a no-op because Run observes the application context.
func (s *MediaWorkerServer) Stop(context.Context) error { return nil }

var _ transport.Server = (*MediaWorkerServer)(nil)
