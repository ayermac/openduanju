package biz

import "github.com/google/wire"

// ProviderSet is biz providers.
var ProviderSet = wire.NewSet(NewTodoUsecase, NewSourceCatalog, NewIngestWorker, NewPlanScheduler, NewMediaBatches, NewAuthorizations, NewStaffAccounts, NewReviews, NewWeb, NewAuditor)
