package server

import (
	stdhttp "net/http"

	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/middleware/validate"
	"github.com/go-kratos/kratos/v3/transport/http"
	adminv1 "openduanju-server/api/admin/v1"
	v1 "openduanju-server/api/todo/v1"
	webv1 "openduanju-server/api/web/v1"
	"openduanju-server/internal/biz"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/service"

	"go.einride.tech/aip/fieldbehavior"
	"google.golang.org/protobuf/proto"
)

// NewHTTPServer new an HTTP server.
func NewHTTPServer(c *conf.Server, todo *service.TodoService, ingestion *service.AdminIngestionService, candidates *service.AdminCandidateService, sources *service.AdminSourceService, audits *service.AdminAuditService, content *service.AdminContentService, media *service.AdminMediaService, uploads *service.AdminUploadService, authorizations *service.AdminAuthorizationService, runtime *service.AdminRuntimeService, auth *service.AdminAuthService, staff *service.AdminStaffService, reviews *service.AdminReviewService, web *service.WebService, accounts *biz.StaffAccounts, staging stdhttp.Handler) *http.Server {
	var opts = []http.ServerOption{
		http.Middleware(
			recovery.Recovery(),
			SessionAuth(accounts),
			validate.Validator(func(req any) error {
				if msg, ok := req.(proto.Message); ok {
					if err := fieldbehavior.ValidateRequiredFields(msg); err != nil {
						return err
					}
				}
				return nil
			}),
		),
		// The trace filter is outermost: a request refused by the session
		// layer still carries an identity, and the response header is set
		// before the body is written.
		http.Filter(traceFilter, adminMethodFilter, statusFilter),
		// An error response names the request it belongs to, so the report a
		// user copies from the page is the identity the logs carry.
		http.ErrorEncoder(traceErrorEncoder),
	}
	if c.Http.Network != "" {
		opts = append(opts, http.Network(c.Http.Network))
	}
	if c.Http.Addr != "" {
		opts = append(opts, http.Address(c.Http.Addr))
	}
	if c.Http.Timeout != nil {
		opts = append(opts, http.Timeout(c.Http.Timeout.AsDuration()))
	}
	srv := http.NewServer(opts...)
	v1.RegisterTodoServiceHTTPServer(srv, todo)
	adminv1.RegisterAdminIngestionServiceHTTPServer(srv, ingestion)
	adminv1.RegisterAdminCandidateServiceHTTPServer(srv, candidates)
	adminv1.RegisterAdminSourceServiceHTTPServer(srv, sources)
	adminv1.RegisterAdminAuditServiceHTTPServer(srv, audits)
	adminv1.RegisterAdminContentServiceHTTPServer(srv, content)
	adminv1.RegisterAdminMediaServiceHTTPServer(srv, media)
	adminv1.RegisterAdminUploadServiceHTTPServer(srv, uploads)
	adminv1.RegisterAdminAuthorizationServiceHTTPServer(srv, authorizations)
	adminv1.RegisterAdminAuthServiceHTTPServer(srv, auth)
	adminv1.RegisterAdminStaffServiceHTTPServer(srv, staff)
	adminv1.RegisterAdminReviewServiceHTTPServer(srv, reviews)
	webv1.RegisterWebServiceHTTPServer(srv, web)
	adminv1.RegisterAdminRuntimeServiceHTTPServer(srv, runtime)
	// The local storage driver accepts the bytes of one signed staging
	// object. The token is the only credential, so the route is mounted
	// outside the admin bearer check and validates its own signature.
	if staging != nil {
		srv.HandleFunc("/api/admin/v1/uploads/staging/{token}", stdhttp.HandlerFunc(staging.ServeHTTP))
		// The same handler serves signed private previews; the token action
		// decides whether a PUT upload or a GET preview is allowed.
		srv.HandleFunc("/api/admin/v1/media-preview/{token}", stdhttp.HandlerFunc(staging.ServeHTTP))
		// Public playback is a signed URL, not a staff session.
		srv.HandleFunc("/api/web/v1/media/{token}", stdhttp.HandlerFunc(staging.ServeHTTP))
	}
	return srv
}
