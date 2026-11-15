package server

import (
	adminv1 "openduanju-server/api/admin/v1"
	v1 "openduanju-server/api/todo/v1"
	webv1 "openduanju-server/api/web/v1"
	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/service"

	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer new a gRPC server. It applies the same session and role
// checks as the HTTP server, so the two transports cannot disagree.
func NewGRPCServer(c *conf.Server, todo *service.TodoService, ingestion *service.AdminIngestionService, candidates *service.AdminCandidateService, sources *service.AdminSourceService, audits *service.AdminAuditService, content *service.AdminContentService, media *service.AdminMediaService, uploads *service.AdminUploadService, authorizations *service.AdminAuthorizationService, runtime *service.AdminRuntimeService, auth *service.AdminAuthService, staff *service.AdminStaffService, reviews *service.AdminReviewService, web *service.WebService, accounts *biz.StaffAccounts) *grpc.Server {
	var opts = []grpc.ServerOption{
		grpc.Middleware(
			// The trace middleware runs first: the session layer and the
			// error it returns both belong to an identified request.
			TraceMiddleware(),
			recovery.Recovery(),
			SessionAuth(accounts),
		),
	}
	if c.Grpc.Network != "" {
		opts = append(opts, grpc.Network(c.Grpc.Network))
	}
	if c.Grpc.Addr != "" {
		opts = append(opts, grpc.Address(c.Grpc.Addr))
	}
	if c.Grpc.Timeout != nil {
		opts = append(opts, grpc.Timeout(c.Grpc.Timeout.AsDuration()))
	}
	srv := grpc.NewServer(opts...)
	v1.RegisterTodoServiceServer(srv, todo)
	adminv1.RegisterAdminIngestionServiceServer(srv, ingestion)
	adminv1.RegisterAdminCandidateServiceServer(srv, candidates)
	adminv1.RegisterAdminSourceServiceServer(srv, sources)
	adminv1.RegisterAdminAuditServiceServer(srv, audits)
	adminv1.RegisterAdminContentServiceServer(srv, content)
	adminv1.RegisterAdminMediaServiceServer(srv, media)
	adminv1.RegisterAdminUploadServiceServer(srv, uploads)
	adminv1.RegisterAdminAuthorizationServiceServer(srv, authorizations)
	adminv1.RegisterAdminAuthServiceServer(srv, auth)
	adminv1.RegisterAdminStaffServiceServer(srv, staff)
	adminv1.RegisterAdminReviewServiceServer(srv, reviews)
	webv1.RegisterWebServiceServer(srv, web)
	adminv1.RegisterAdminRuntimeServiceServer(srv, runtime)
	return srv
}
