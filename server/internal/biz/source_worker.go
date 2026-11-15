package biz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// IngestWorkerConfig bounds one worker loop.
type IngestWorkerConfig struct {
	WorkerID       string
	PollInterval   time.Duration
	Lease          time.Duration
	PageInterval   time.Duration
	DetailInterval time.Duration
}

// IngestWorker runs persisted ingestion tasks. State lives in MySQL, so a
// restarted process resumes from the stored page cursor and lease.
type IngestWorker struct {
	repo    SourceCatalogRepo
	adapter WebMetadataAdapter
	cfg     IngestWorkerConfig
	log     *slog.Logger
}

// NewIngestWorker validates dependencies and defaults.
func NewIngestWorker(repo SourceCatalogRepo, adapter WebMetadataAdapter, cfg IngestWorkerConfig, logger *slog.Logger) (*IngestWorker, error) {
	if repo == nil || adapter == nil {
		return nil, errors.New("ingest worker requires repo and adapter")
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.Lease <= 0 {
		cfg.Lease = 90 * time.Second
	}
	if cfg.PageInterval < 0 {
		cfg.PageInterval = 0
	}
	if cfg.DetailInterval < 0 {
		cfg.DetailInterval = 0
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &IngestWorker{repo: repo, adapter: adapter, cfg: cfg, log: logger}, nil
}

// Run polls the persisted queue until the context is cancelled.
func (w *IngestWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		processed, err := w.ProcessOnce(ctx)
		if err != nil {
			w.log.Error("claim ingestion run failed", "err", err)
		} else if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessOnce claims and handles at most one persisted run. It is used by the
// loop and by deterministic integration tests.
func (w *IngestWorker) ProcessOnce(ctx context.Context) (bool, error) {
	run, err := w.repo.ClaimRun(ctx, w.cfg.WorkerID, w.cfg.Lease)
	if err != nil {
		return false, err
	}
	if run == nil {
		return false, nil
	}
	w.process(ctx, run)
	return true, nil
}

func (w *IngestWorker) process(ctx context.Context, run *IngestionRun) {
	// The run was queued by a request that no longer exists, possibly before
	// a restart. The identity is read back from the row and put on the
	// context, so every line of this run names its trigger.
	if run.TraceID != "" {
		ctx = WithTraceID(ctx, run.TraceID)
	}
	if run.NextRequestAt != nil {
		if wait := time.Until(*run.NextRequestAt); wait > 0 {
			if err := sleepCtx(ctx, wait); err != nil {
				w.finish(run, RunStatusCancelled, "", "worker stopped while rate limited")
				return
			}
		}
	}
	w.log.InfoContext(ctx, "ingestion run started",
		"run_id", run.ID, "kind", run.Kind,
		"source", run.SourceCode, "channel", run.Channel,
		"cursor", run.PageCursor, "version", run.Version)
	if run.Kind == RunKindCompletion {
		w.processCompletion(ctx, run)
		return
	}
	for page := run.PageCursor + 1; page <= run.PageEnd; page++ {
		if cancelled, current := w.cancelled(ctx, run); cancelled {
			w.finish(current, RunStatusCancelled, "", "cancelled before page")
			return
		}
		if !w.consume(ctx, run, "page") {
			return
		}
		if err := sleepCtx(ctx, w.cfg.PageInterval); err != nil {
			w.finish(run, RunStatusCancelled, "", "worker stopped")
			return
		}
		// The channel's own timeout bounds every outbound call; the run snapshot
		// froze it, so a later configuration edit cannot change a run in flight.
		callCtx := ctx
		cancel := func() {}
		if run.ChannelTimeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, run.ChannelTimeout)
		}
		pageData, err := w.adapter.ListSeries(callCtx, ListSeriesRequest{Channel: run.Channel, Page: page, Origin: run.ChannelBaseURL})
		cancel()
		if err != nil {
			w.finish(run, RunStatusFailed, sourceCode(err), err.Error())
			return
		}
		if pageData == nil || len(pageData.Items) == 0 {
			w.finish(run, RunStatusFailed, string(SourceCodeParseFailed), "empty ranking page")
			return
		}
		if run.ItemsSeen+len(pageData.Items) > run.ItemBudget {
			w.exhaustBudget(run, "item budget exhausted")
			return
		}
		if err := w.repo.AddProgress(ctx, run.ID, w.cfg.WorkerID, run.Version, len(pageData.Items), 0, 0, 0); err != nil {
			w.leaseError(run, err)
			return
		}
		run.ItemsSeen += len(pageData.Items)
		if err := w.repo.RenewLease(ctx, run.ID, w.cfg.WorkerID, run.Version, w.cfg.Lease); err != nil {
			w.leaseError(run, err)
			return
		}

		for _, summary := range pageData.Items {
			if cancelled, current := w.cancelled(ctx, run); cancelled {
				w.finish(current, RunStatusCancelled, "", "cancelled during page")
				return
			}
			candidate, err := w.repo.UpsertCandidateSummary(ctx, run.ID, w.cfg.WorkerID, run.Version, run.SourceCode, run.Channel, summary)
			if err != nil {
				w.leaseError(run, err)
				return
			}
			if err := w.repo.AddProgress(ctx, run.ID, w.cfg.WorkerID, run.Version, 0, 1, 0, 0); err != nil {
				w.leaseError(run, err)
				return
			}
			run.ItemsUpserted++
			if !run.FetchDetails {
				continue
			}
			// A resumed page may already contain details fetched in this same
			// run; skip them so a rate window can make progress.
			existing, lookupErr := w.repo.GetCandidateByExternal(ctx, run.SourceCode, summary.ExternalID)
			if lookupErr == nil && existing.Completeness == CompletenessComplete &&
				existing.DetailFetchedAt != nil && !existing.DetailFetchedAt.Before(run.StartedAt) {
				continue
			}
			if !w.consume(ctx, run, "detail") {
				return
			}
			if err := sleepCtx(ctx, w.cfg.DetailInterval); err != nil {
				w.finish(run, RunStatusCancelled, "", "worker stopped")
				return
			}
			detail, err := w.adapter.GetSeries(ctx, summary.ExternalID)
			if err != nil {
				// The source request failed: the row, its stage and the
				// source error code are recorded before the run ends.
				if recordErr := w.repo.RecordRunItem(ctx, run.ID, w.cfg.WorkerID, run.Version, candidate.ID, candidate.ExternalID, RunItemStageDetail, RunItemResultFailed, sourceCode(err), err.Error()); recordErr != nil {
					w.log.Error("record the failed detail request failed", "run_id", run.ID, "candidate_id", candidate.ID, "err", recordErr)
					w.leaseError(run, recordErr)
					return
				}
				w.finish(run, RunStatusFailed, sourceCode(err), err.Error())
				return
			}
			episodeCount := 0
			if run.FetchEpisodes {
				episodeCount = len(detail.Episodes)
			} else {
				detail.Episodes = nil
			}
			if err := w.repo.UpsertCandidateDetail(ctx, candidate.ID, run.ID, w.cfg.WorkerID, run.Version, detail); err != nil {
				// The failed attempt stays visible in the run, so the row can
				// be located even after a later attempt succeeds. A failure
				// to record it is not swallowed.
				if recordErr := w.repo.RecordRunItem(ctx, run.ID, w.cfg.WorkerID, run.Version, candidate.ID, candidate.ExternalID, RunItemStageDetail, RunItemResultFailed, "DETAIL_FAILED", err.Error()); recordErr != nil {
					w.log.Error("record the failed detail write failed", "run_id", run.ID, "candidate_id", candidate.ID, "err", recordErr)
				}
				w.leaseError(run, err)
				return
			}
			if err := w.repo.AddProgress(ctx, run.ID, w.cfg.WorkerID, run.Version, 0, 0, 1, episodeCount); err != nil {
				w.leaseError(run, err)
				return
			}
			run.DetailFetched++
			run.EpisodesFetched += episodeCount
		}
		if err := w.repo.CompletePage(ctx, run.ID, w.cfg.WorkerID, run.Version, page, len(pageData.Items), time.Now().UTC()); err != nil {
			w.leaseError(run, err)
			return
		}
		run.PageCursor = page
		run.PagesFetched++
	}
	w.finish(run, RunStatusSucceeded, "", "")
}

func (w *IngestWorker) processCompletion(ctx context.Context, run *IngestionRun) {
	candidate, err := w.repo.GetCandidate(ctx, run.CandidateID)
	if err != nil {
		w.finish(run, RunStatusFailed, "CANDIDATE_NOT_FOUND", err.Error())
		return
	}
	if candidate.Completeness == CompletenessComplete {
		w.finish(run, RunStatusSucceeded, "", "")
		return
	}
	if cancelled, current := w.cancelled(ctx, run); cancelled {
		w.finish(current, RunStatusCancelled, "", "cancelled before completion")
		return
	}
	if !w.consume(ctx, run, "completion") {
		return
	}
	detail, err := w.adapter.GetSeries(ctx, candidate.ExternalID)
	if err != nil {
		w.finish(run, RunStatusFailed, sourceCode(err), err.Error())
		return
	}
	if err := w.repo.UpsertCandidateDetail(ctx, candidate.ID, run.ID, w.cfg.WorkerID, run.Version, detail); err != nil {
		w.leaseError(run, err)
		return
	}
	if err := w.repo.AddProgress(ctx, run.ID, w.cfg.WorkerID, run.Version, 0, 0, 1, len(detail.Episodes)); err != nil {
		w.leaseError(run, err)
		return
	}
	w.finish(run, RunStatusSucceeded, "", "")
}

// consume reserves one outbound request. It returns false when the run must
// stop because it is rate limited, out of budget, cancelled or lease-lost.
func (w *IngestWorker) consume(ctx context.Context, run *IngestionRun, stage string) bool {
	decision, next, err := w.repo.ConsumeRequest(ctx, run.ID, w.cfg.WorkerID, run.Version, time.Now().UTC(), w.cfg.Lease)
	if err != nil {
		w.leaseError(run, err)
		return false
	}
	switch decision {
	case RequestAllowed:
		return true
	case RequestCancelled:
		w.finish(run, RunStatusCancelled, "", "cancelled before "+stage)
	case RequestRateLimit:
		w.log.Info("ingestion rate limited", "run_id", run.ID, "resume_at", next)
	case RequestBudget:
		w.log.Info("ingestion budget exhausted", "run_id", run.ID, "stage", stage)
	}
	return false
}

func (w *IngestWorker) exhaustBudget(run *IngestionRun, reason string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 10*time.Second)
	defer cancel()
	if err := w.repo.MarkBudgetExhausted(ctx, run.ID, w.cfg.WorkerID, run.Version, reason); err != nil {
		w.log.Warn("mark budget exhausted failed", "run_id", run.ID, "err", err)
	}
}

func (w *IngestWorker) leaseError(run *IngestionRun, err error) {
	if errors.Is(err, ErrRunLeaseLost) {
		w.log.Warn("ingestion lease lost; abandoning run", "run_id", run.ID)
		return
	}
	w.log.Error("ingestion write failed", "run_id", run.ID, "err", err)
}

func (w *IngestWorker) cancelled(ctx context.Context, run *IngestionRun) (bool, *IngestionRun) {
	current, err := w.repo.GetRun(ctx, run.ID)
	if err != nil {
		return false, run
	}
	return current.CancelRequested, current
}

func (w *IngestWorker) finish(run *IngestionRun, status, code, reason string) {
	if run == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 10*time.Second)
	defer cancel()
	if err := w.repo.FinishRun(ctx, run.ID, w.cfg.WorkerID, run.Version, status, code, reason); err != nil {
		w.log.Warn("finish ingestion run failed", "trace_id", run.TraceID, "run_id", run.ID, "err", err)
		return
	}
	w.log.Info("ingestion run finished",
		"trace_id", run.TraceID, "run_id", run.ID, "worker", w.cfg.WorkerID,
		"status", status, "code", code, "pages", run.PagesFetched, "items", run.ItemsUpserted)
}

func sourceCode(err error) string {
	if code, ok := SourceErrorCodeOf(err); ok {
		return string(code)
	}
	return string(SourceCodeUnavailable)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
