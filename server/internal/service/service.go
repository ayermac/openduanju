package service

import "github.com/google/wire"

// ProviderSet is service providers.
var ProviderSet = wire.NewSet(NewTodoService, NewAdminIngestionService, NewAdminCandidateService, NewAdminSourceService, NewAdminAuditService, NewAdminContentService, NewAdminMediaService, NewAdminUploadService, NewAdminAuthService, NewAdminReviewService, NewWebService,
	PlaybackTicketTTL, NewAdminStaffService, NewAdminAuthorizationService, NewAdminRuntimeService)
