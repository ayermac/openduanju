package service

import (
	"context"

	v1 "openduanju-server/api/admin/v1"
	"openduanju-server/internal/bootstrap"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AdminRuntimeService reports the environment the admin API is bound to.
type AdminRuntimeService struct {
	v1.UnimplementedAdminRuntimeServiceServer

	identity bootstrap.Identity
}

// NewAdminRuntimeService builds the runtime service from the database
// identity resolved during startup validation.
func NewAdminRuntimeService(identity bootstrap.Identity) *AdminRuntimeService {
	return &AdminRuntimeService{identity: identity}
}

// GetRuntimeInfo returns the database the server is connected to. The
// identity never contains credentials.
func (s *AdminRuntimeService) GetRuntimeInfo(_ context.Context, _ *emptypb.Empty) (*v1.RuntimeInfo, error) {
	return &v1.RuntimeInfo{
		Database:     s.identity.Database,
		DatabaseHost: s.identity.Host,
	}, nil
}
