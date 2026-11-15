package data_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
	"openduanju-server/internal/data/ent/contentrevision"
	"openduanju-server/internal/data/ent/ingestionrunbudget"
	"openduanju-server/internal/data/ent/revisionepisode"
	"openduanju-server/internal/migrate"
	"openduanju-server/migrations"

	_ "github.com/go-sql-driver/mysql"
)

type fakeWeb struct {
	page     *biz.SeriesPage
	detail   *biz.SeriesDetail
	calls    int
	disabled bool
	// detailErr makes the source request fail, so the run records the
	// failure of that stage.
	detailErr error
}

func (f *fakeWeb) Key() string { return "fake_web" }

// Entries declares the entry the fake serves, so a plan and a channel can both
// name it in the tests.
func (f *fakeWeb) Entries() []biz.SourceEntry {
	return []biz.SourceEntry{{
		SourceCode: "hongguo", AdapterKey: "fake_web", Kind: "rank", Channel: "hot-drama",
		Label: "测试榜单", BaseURL: "https://hongguoduanju.com",
		AllowedHosts: []string{"hongguoduanju.com", "www.hongguoduanju.com"},
	}}
}

func (f *fakeWeb) ListSeries(context.Context, biz.ListSeriesRequest) (*biz.SeriesPage, error) {
	if f.disabled {
		return nil, errors.New("source must not be called")
	}
	f.calls++
	return f.page, nil
}

func (f *fakeWeb) GetSeries(context.Context, string) (*biz.SeriesDetail, error) {
	if f.disabled {
		return nil, errors.New("source must not be called")
	}
	if f.detailErr != nil {
		f.calls++
		return nil, f.detailErr
	}
	f.calls++
	return f.detail, nil
}

func openCatalogClient(t *testing.T) *ent.Client {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set; catalog integration tests need a disposable MySQL database")
	}
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	if _, err := migrate.Up(ctx, sqlDB, migrations.FS, migrate.Options{}); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	client, err := ent.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open ent client: %v", err)
	}
	t.Cleanup(func() {
		cleanCatalog(t, sqlDB)
		_ = client.Close()
		_ = sqlDB.Close()
	})
	cleanCatalog(t, sqlDB)
	return client
}

func cleanCatalog(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"revision_episode", "content_revision", "content_series", "candidate_episode",
		"source_candidate", "ingestion_run_page", "ingestion_run", "ingestion_plan", "series_episode",
	} {
		if _, err := db.Exec("DELETE FROM `" + table + "`"); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	// The source and channel rows are configuration, not test data: every test
	// starts from the deployed state, so a pause cannot leak into the next one.
	for _, statement := range []string{
		"UPDATE source_channel SET status='enabled', rate_per_minute=30, timeout_seconds=20, version=1, name='热播榜'",
		"UPDATE source SET status='enabled', version=1",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("reset the source configuration: %v", err)
		}
	}
}

func catalogSummary(id, title string) biz.SeriesSummary {
	return biz.SeriesSummary{
		ExternalID: id, Title: title, CoverURL: "https://p3.example/" + id + ".jpg",
		Description: "简介 " + title, Rank: 1, Tags: []string{"玄幻", "漫剧"},
		EpisodeCount: 3, OpenEpisodeCount: 1,
		Metrics: map[string]string{"heat": "100万热度"},
	}
}

func catalogDetail(id, title string) *biz.SeriesDetail {
	return &biz.SeriesDetail{
		ExternalID: id, Title: title, CoverURL: "https://p3.example/" + id + ".jpg",
		Description: "详情 " + title, Tags: []string{"玄幻"}, EpisodeCount: 3, AccessibleCount: 1,
		Episodes: []biz.EpisodeRef{{ExternalID: id + "-1", Number: 1}, {ExternalID: id + "-2", Number: 2}, {ExternalID: id + "-3", Number: 3}},
	}
}

func TestCatalogPersistsPlanRunAndWorker(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()

	plan, err := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "test-plan", Name: "测试计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 10,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	web := &fakeWeb{
		page: &biz.SeriesPage{
			Channel: "hot-drama", Page: 1, TotalPages: 1,
			Items: []biz.SeriesSummary{catalogSummary("3001", "离线测试剧")},
		},
		detail: catalogDetail("3001", "离线测试剧"),
	}
	worker, err := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "test-worker", Lease: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.Status != biz.RunStatusQueued {
		t.Fatalf("queued run = %+v", run)
	}
	if active, err := repo.ActiveRun(ctx, plan.ID); err != nil || active == nil || active.ID != run.ID {
		t.Fatalf("active run = %+v err=%v", active, err)
	}
	processed, err := worker.ProcessOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("ProcessOnce processed=%v err=%v", processed, err)
	}
	finished, err := repo.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != biz.RunStatusSucceeded || finished.PagesFetched != 1 || finished.ItemsUpserted != 1 {
		t.Fatalf("finished run = %+v", finished)
	}
	if finished.RequestsUsed != 2 {
		t.Fatalf("requests used = %d, want page+detail", finished.RequestsUsed)
	}

	candidates, err := repo.ListCandidates(ctx, biz.CandidateQuery{Page: 1, PageSize: 10})
	if err != nil || candidates.Total != 1 {
		t.Fatalf("candidates = %+v err=%v", candidates, err)
	}
	candidate := candidates.Items[0]
	if candidate.Completeness != biz.CompletenessComplete || candidate.SeriesID != 0 {
		t.Fatalf("candidate snapshot = %+v", candidate)
	}
	episodes, err := repo.ListCandidateEpisodes(ctx, candidate.ID)
	if err != nil || len(episodes) != 3 {
		t.Fatalf("episodes = %v err=%v", episodes, err)
	}

	// Offline assertion: reader/selection paths must not touch the adapter.
	web.disabled = true
	callsBefore := web.calls
	uc, _ := biz.NewSourceCatalog(repo, web, nil, nil)
	view, err := uc.CreateSeries(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("CreateSeries offline: %v", err)
	}
	if view.Revision == nil || len(view.Episodes) != 3 {
		t.Fatalf("series view = %+v", view)
	}
	selection, err := uc.SetSelection(ctx, view.Revision.ID, view.Revision.Version, "explicit", []string{"3001-2", "3001-3"})
	if err != nil {
		t.Fatalf("SetSelection offline: %v", err)
	}
	if selection.SelectedCount != 2 {
		t.Fatalf("selected = %d", selection.SelectedCount)
	}
	if web.calls != callsBefore {
		t.Fatalf("selection called the source %d times", web.calls-callsBefore)
	}

	// Version conflict and unknown ids leave the saved selection intact.
	if _, err := uc.SetSelection(ctx, view.Revision.ID, view.Revision.Version, "explicit", []string{"3001-1"}); !errors.Is(err, biz.ErrRevisionVersion) {
		t.Fatalf("stale version error = %v", err)
	}
	if _, err := uc.SetSelection(ctx, selection.ID, selection.Version, "explicit", []string{"3001-1", "missing"}); !errors.Is(err, biz.ErrSelectionMismatch) {
		t.Fatalf("unknown id error = %v", err)
	}
	refreshed, err := uc.GetSeries(ctx, view.Series.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Revision.SelectedCount != 2 {
		t.Fatalf("selection changed after failed update: %d", refreshed.Revision.SelectedCount)
	}
}

func TestCatalogRejectsIncompleteCandidate(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan, err := repo.CreatePlan(ctx, biz.IngestionPlan{Code: "incomplete", Name: "未补全", SourceCode: "hongguo", Channel: "hot-drama", PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, RatePerMinute: 30, FetchDetails: true, FetchEpisodes: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	claimed, _ := repo.ClaimRun(ctx, "incomplete-worker", time.Minute)
	candidate, err := repo.UpsertCandidateSummary(ctx, claimed.ID, "incomplete-worker", claimed.Version, "hongguo", "hot-drama", catalogSummary("4001", "未补全"))
	if err != nil {
		t.Fatal(err)
	}
	_ = run
	web := &fakeWeb{disabled: true}
	uc, _ := biz.NewSourceCatalog(repo, web, nil, nil)
	if _, err := uc.CreateSeries(ctx, candidate.ID); !errors.Is(err, biz.ErrCandidateIncomplete) {
		t.Fatalf("incomplete error = %v", err)
	}
	if web.calls != 0 {
		t.Fatalf("selection path called the source %d times", web.calls)
	}
}

func TestCatalogRunCancelAndBudget(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	page := &biz.SeriesPage{Channel: "hot-drama", Page: 1, TotalPages: 1, Items: []biz.SeriesSummary{catalogSummary("5001", "预算测试")}}
	web := &fakeWeb{page: page, detail: catalogDetail("5001", "预算测试")}
	worker, _ := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "cancel-worker", Lease: time.Minute}, nil)

	cancelPlan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "cancel-plan", Name: "取消", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	cancelledRun, _ := repo.CreateRun(ctx, cancelPlan, biz.RunKindRanking, 0)
	if err := repo.RequestCancel(ctx, cancelledRun.ID, cancelledRun.Version); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	if processed, err := worker.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("cancel ProcessOnce=%v err=%v", processed, err)
	}
	afterCancel, _ := repo.GetRun(ctx, cancelledRun.ID)
	if afterCancel.Status != biz.RunStatusCancelled {
		t.Fatalf("cancelled run = %+v", afterCancel)
	}
	if web.calls != 0 {
		t.Fatalf("cancelled run called the source %d times", web.calls)
	}

	budgetPlan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "budget-plan", Name: "预算", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 1, ItemBudget: 5, FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	budgetRun, _ := repo.CreateRun(ctx, budgetPlan, biz.RunKindRanking, 0)
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("budget ProcessOnce: %v", err)
	}
	afterBudget, _ := repo.GetRun(ctx, budgetRun.ID)
	if afterBudget.Status != biz.RunStatusBudget || afterBudget.ErrorCode != string(biz.SourceCodeBudget) {
		t.Fatalf("budget run = %+v", afterBudget)
	}
	if afterBudget.RequestsUsed != 1 {
		t.Fatalf("partial page requests used = %d, want 1", afterBudget.RequestsUsed)
	}
	candidates, err := repo.ListCandidates(ctx, biz.CandidateQuery{Page: 1, PageSize: 10, SourceCode: "hongguo"})
	if err != nil || candidates.Total != 1 {
		t.Fatalf("summary after budget stop = %+v err=%v", candidates, err)
	}
	if _, err := repo.AddBudget(ctx, budgetRun.ID, 5); err != nil {
		t.Fatalf("AddBudget: %v", err)
	}
	if processed, err := worker.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("resume after budget ProcessOnce=%v err=%v", processed, err)
	}
	resumed, _ := repo.GetRun(ctx, budgetRun.ID)
	if resumed.Status != biz.RunStatusSucceeded || resumed.RequestsUsed <= 1 {
		t.Fatalf("resumed budget run = %+v (consumed budget must not reset)", resumed)
	}
}

func TestCatalogRunResumesExpiredLease(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "resume-plan", Name: "恢复", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 5, FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	run, _ := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	claimed, err := repo.ClaimRun(ctx, "dead-worker", time.Second)
	if err != nil || claimed == nil || claimed.ID != run.ID {
		t.Fatalf("initial claim = %+v err=%v", claimed, err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := client.IngestionRun.UpdateOneID(run.ID).SetLeaseUntil(past).SetLeaseOwner("dead-worker").Exec(ctx); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	web := &fakeWeb{page: &biz.SeriesPage{Channel: "hot-drama", Page: 1, TotalPages: 1, Items: []biz.SeriesSummary{catalogSummary("6001", "恢复测试")}}, detail: catalogDetail("6001", "恢复测试")}
	worker, _ := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "resume-worker", Lease: time.Minute}, nil)
	if processed, err := worker.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("resume ProcessOnce=%v err=%v", processed, err)
	}
	finished, _ := repo.GetRun(ctx, run.ID)
	if finished.Status != biz.RunStatusSucceeded {
		t.Fatalf("resumed run = %+v", finished)
	}
}

func TestCatalogRateLimitWaitsThenResumes(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	page := &biz.SeriesPage{
		Channel: "hot-drama", Page: 1, TotalPages: 1,
		Items: []biz.SeriesSummary{catalogSummary("7001", "一"), catalogSummary("7002", "二")},
	}
	web := &fakeWeb{page: page, detail: catalogDetail("7001", "一")}
	worker, _ := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "rate-worker", Lease: time.Minute}, nil)
	plan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "rate-plan", Name: "限频", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 20, ItemBudget: 10, RatePerMinute: 2,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	run, _ := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("first ProcessOnce: %v", err)
	}
	waiting, _ := repo.GetRun(ctx, run.ID)
	if waiting.Status != biz.RunStatusWaiting || waiting.NextRequestAt == nil {
		t.Fatalf("rate limited run = %+v", waiting)
	}
	if waiting.RequestsUsed != 2 {
		t.Fatalf("requests used = %d, want window limit 2", waiting.RequestsUsed)
	}
	if !waiting.NextRequestAt.After(time.Now().UTC()) {
		t.Fatalf("next request at %s is not in the future", waiting.NextRequestAt)
	}

	// Simulate the minute boundary without sleeping: reset window and due time.
	past := time.Now().UTC().Add(-2 * time.Minute)
	if err := client.IngestionRun.UpdateOneID(run.ID).
		SetNextRequestAt(past).
		SetRateWindowStart(past).
		SetRateWindowCount(0).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if processed, err := worker.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("resume ProcessOnce=%v err=%v", processed, err)
	}
	resumed, _ := repo.GetRun(ctx, run.ID)
	if resumed.Status != biz.RunStatusSucceeded {
		t.Fatalf("resumed rate run = %+v", resumed)
	}
	if resumed.RequestsUsed <= waiting.RequestsUsed {
		t.Fatalf("requests used did not grow after resume: %d", resumed.RequestsUsed)
	}
}

func TestCatalogLeaseFencesCandidateWrites(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "lease-plan", Name: "租约", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 20, ItemBudget: 10, RatePerMinute: 30,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	run, _ := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	stale, err := repo.ClaimRun(ctx, "worker-A", time.Second)
	if err != nil || stale == nil {
		t.Fatalf("A claim = %+v err=%v", stale, err)
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := client.IngestionRun.UpdateOneID(run.ID).SetLeaseUntil(past).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err := repo.ClaimRun(ctx, "worker-B", time.Minute)
	if err != nil || fresh == nil || fresh.ID != run.ID {
		t.Fatalf("B claim = %+v err=%v", fresh, err)
	}
	if fresh.Version <= stale.Version {
		t.Fatalf("claim did not bump version: A=%d B=%d", stale.Version, fresh.Version)
	}
	if decision, _, err := repo.ConsumeRequest(ctx, run.ID, "worker-B", fresh.Version, time.Now().UTC(), time.Minute); err != nil || decision != biz.RequestAllowed {
		t.Fatalf("B consume = %s err=%v", decision, err)
	}
	candidate, err := repo.UpsertCandidateSummary(ctx, run.ID, "worker-B", fresh.Version, "hongguo", "hot-drama", catalogSummary("8001", "B写入"))
	if err != nil {
		t.Fatalf("B summary: %v", err)
	}
	detail := catalogDetail("8001", "B写入")
	if err := repo.UpsertCandidateDetail(ctx, candidate.ID, run.ID, "worker-B", fresh.Version, detail); err != nil {
		t.Fatalf("B detail: %v", err)
	}

	staleSummary := catalogSummary("8001", "A覆盖")
	if _, err := repo.UpsertCandidateSummary(ctx, run.ID, "worker-A", stale.Version, "hongguo", "hot-drama", staleSummary); !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("stale summary error = %v", err)
	}
	if err := repo.UpsertCandidateDetail(ctx, candidate.ID, run.ID, "worker-A", stale.Version, detail); !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("stale detail error = %v", err)
	}
	if err := repo.AddProgress(ctx, run.ID, "worker-A", stale.Version, 1, 1, 1, 1); !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("stale progress error = %v", err)
	}
	if err := repo.CompletePage(ctx, run.ID, "worker-A", stale.Version, 1, 1, time.Now().UTC()); !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("stale checkpoint error = %v", err)
	}
	if err := repo.FinishRun(ctx, run.ID, "worker-A", stale.Version, biz.RunStatusSucceeded, "", ""); !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("stale finish error = %v", err)
	}
	if _, _, err := repo.ConsumeRequest(ctx, run.ID, "worker-A", stale.Version, time.Now().UTC(), time.Minute); !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("stale consume error = %v", err)
	}
	after, err := repo.GetCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "B写入" {
		t.Fatalf("candidate title = %q, stale worker overwrote new data", after.Title)
	}
	episodes, err := repo.ListCandidateEpisodes(ctx, candidate.ID)
	if err != nil || len(episodes) != 3 {
		t.Fatalf("episodes after stale writes = %d err=%v", len(episodes), err)
	}
}

func TestCompletionRunHonorsCancelAndBudget(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "completion-plan", Name: "补全", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, RatePerMinute: 30,
		FetchDetails: false, FetchEpisodes: false, Enabled: true,
	})
	ranking, _ := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	claimed, _ := repo.ClaimRun(ctx, "completion-worker", time.Minute)
	candidate, err := repo.UpsertCandidateSummary(ctx, ranking.ID, "completion-worker", claimed.Version, "hongguo", "hot-drama", catalogSummary("9001", "待补全"))
	if err != nil {
		t.Fatal(err)
	}
	web := &fakeWeb{detail: catalogDetail("9001", "待补全")}
	worker, _ := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "completion-worker", Lease: time.Minute}, nil)

	cancelled, err := repo.CreateCompletionRun(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RequestCancel(ctx, cancelled.ID, cancelled.Version); err != nil {
		t.Fatalf("cancel completion: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("cancelled completion: %v", err)
	}
	afterCancel, _ := repo.GetRun(ctx, cancelled.ID)
	if afterCancel.Status != biz.RunStatusCancelled {
		t.Fatalf("completion after cancel = %+v", afterCancel)
	}
	if web.calls != 0 {
		t.Fatalf("cancelled completion called source %d times", web.calls)
	}

	budgeted, err := repo.CreateCompletionRun(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.IngestionRun.UpdateOneID(budgeted.ID).SetRequestBudget(0).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("budget completion: %v", err)
	}
	afterBudget, _ := repo.GetRun(ctx, budgeted.ID)
	if afterBudget.Status != biz.RunStatusBudget || afterBudget.RequestsUsed != 0 {
		t.Fatalf("completion budget = %+v", afterBudget)
	}
	if web.calls != 0 {
		t.Fatalf("budget-stopped completion called source %d times", web.calls)
	}
}

func TestRetryRunExtendsBudgetAndSkipsCompletedDetails(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	page := &biz.SeriesPage{
		Channel: "hot-drama", Page: 1, TotalPages: 1,
		Items: []biz.SeriesSummary{catalogSummary("9101", "一"), catalogSummary("9102", "二")},
	}
	web := &fakeWeb{page: page, detail: catalogDetail("9101", "一")}
	worker, _ := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "retry-worker", Lease: time.Minute}, nil)
	plan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "retry-plan", Name: "重试", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 2, ItemBudget: 10, RatePerMinute: 30,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	run, _ := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, _ := repo.GetRun(ctx, run.ID)
	if stopped.Status != biz.RunStatusBudget || stopped.RequestsUsed != 2 {
		t.Fatalf("stopped run = %+v", stopped)
	}
	callsAfterStop := web.calls
	if _, err := repo.RetryRun(ctx, run.ID, stopped.Version, 0, 0, ""); !errors.Is(err, biz.ErrBudgetExtension) {
		t.Fatalf("retry without budget error = %v", err)
	}
	retried, err := repo.RetryRun(ctx, run.ID, stopped.Version, 5, 0, "运营追加")
	if err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	if retried.Status != biz.RunStatusQueued || retried.RequestBudget != 7 || retried.RequestsUsed != 2 {
		t.Fatalf("retried run = %+v", retried)
	}
	if _, err := repo.RetryRun(ctx, run.ID, stopped.Version-1, 5, 0, "重复"); !errors.Is(err, biz.ErrRunVersion) {
		t.Fatalf("stale retry error = %v", err)
	}
	auditCount, err := client.IngestionRunBudget.Query().Where(ingestionrunbudget.RunIDEQ(run.ID)).Count(ctx)
	if err != nil || auditCount != 1 {
		t.Fatalf("audit count = %d err=%v", auditCount, err)
	}
	if processed, err := worker.ProcessOnce(ctx); err != nil || !processed {
		t.Fatalf("retry ProcessOnce=%v err=%v", processed, err)
	}
	finished, _ := repo.GetRun(ctx, run.ID)
	if finished.Status != biz.RunStatusSucceeded {
		t.Fatalf("finished retry run = %+v", finished)
	}
	if finished.RequestsUsed != 4 {
		t.Fatalf("requests after retry = %d, want 4 (2 before + list/detail after)", finished.RequestsUsed)
	}
	if web.calls != callsAfterStop+2 {
		t.Fatalf("source calls after retry = %d, want %d (skip completed detail)", web.calls, callsAfterStop+2)
	}
}

func TestUpdatePlanUsesVersionCas(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan, _ := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "edit-plan", Name: "旧名", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 20, RatePerMinute: 30,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	updated, err := repo.UpdatePlan(ctx, plan.ID, plan.Version, biz.IngestionPlan{
		Name: "新名", PageStart: 1, PageEnd: 2, RequestBudget: 12, ItemBudget: 24,
		RatePerMinute: 10, FetchDetails: false, FetchEpisodes: true, Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	if updated.Name != "新名" || updated.PageEnd != 2 || updated.RatePerMinute != 10 ||
		updated.FetchDetails || updated.Enabled || updated.Version != plan.Version+1 {
		t.Fatalf("updated plan = %+v", updated)
	}
	if _, err := repo.UpdatePlan(ctx, plan.ID, plan.Version, *plan); !errors.Is(err, biz.ErrPlanVersion) {
		t.Fatalf("stale plan update error = %v", err)
	}
}

func TestCatalogPlanScheduleIsPersistedAndFiredOnce(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	due := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	plan, err := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "cron-plan", Name: "定时计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 20, RatePerMinute: 30,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
		Cron: "0 3 * * *", Timezone: "Asia/Shanghai", NextRunAt: &due,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if plan.Cron != "0 3 * * *" || plan.Timezone != "Asia/Shanghai" || plan.NextRunAt == nil {
		t.Fatalf("stored schedule = %+v", plan)
	}

	duePlans, err := repo.DuePlans(ctx, due.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("DuePlans: %v", err)
	}
	if len(duePlans) != 1 || duePlans[0].ID != plan.ID {
		t.Fatalf("due plans = %+v", duePlans)
	}
	notDue, err := repo.DuePlans(ctx, due.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("DuePlans before slot: %v", err)
	}
	if len(notDue) != 0 {
		t.Fatalf("plan must not be due before its slot: %+v", notDue)
	}

	next := due.Add(24 * time.Hour)
	claimed, err := repo.AdvancePlanSchedule(ctx, plan.ID, due, next)
	if err != nil {
		t.Fatalf("AdvancePlanSchedule: %v", err)
	}
	if !claimed {
		t.Fatal("the due slot must be claimed once")
	}
	claimed, err = repo.AdvancePlanSchedule(ctx, plan.ID, due, next.Add(time.Hour))
	if err != nil {
		t.Fatalf("AdvancePlanSchedule again: %v", err)
	}
	if claimed {
		t.Fatal("a consumed slot must not be claimed twice")
	}
	stored, err := repo.GetPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if stored.NextRunAt == nil || !stored.NextRunAt.Equal(next) {
		t.Fatalf("stored next run = %v, want %v", stored.NextRunAt, next)
	}
}

func TestCatalogManualAndScheduledTriggerCreateOneRun(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan, err := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "dedup-plan", Name: "去重计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 10, ItemBudget: 20, RatePerMinute: 30,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			_, runErr := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
			results <- runErr
		}()
	}
	close(start)

	successes, conflicts := 0, 0
	for index := 0; index < 2; index++ {
		runErr := <-results
		switch {
		case runErr == nil:
			successes++
		case errors.Is(runErr, biz.ErrRunAlreadyActive):
			conflicts++
		default:
			t.Fatalf("unexpected run error = %v", runErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, conflicts = %d", successes, conflicts)
	}
	runs, total, err := repo.ListRuns(ctx, biz.RunQuery{PlanID: plan.ID, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("total runs = %d, runs = %+v", total, runs)
	}
}

// catalogPlan builds a plan for the run item tests.
func catalogPlan(t *testing.T, repo biz.SourceCatalogRepo, ctx context.Context) *biz.IngestionPlan {
	t.Helper()
	plan, err := repo.CreatePlan(ctx, biz.IngestionPlan{
		Code: "item-plan", Name: "条目计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 20, ItemBudget: 20,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	return plan
}

// TestRunItemsSurviveALaterRun proves the rows of a run are stored, not
// derived from the candidate snapshot: a later run over the same candidate
// does not change the rows of the earlier one.
func TestRunItemsSurviveALaterRun(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan := catalogPlan(t, repo, ctx)
	web := &fakeWeb{
		page: &biz.SeriesPage{
			Channel: "hot-drama", Page: 1, TotalPages: 1,
			Items: []biz.SeriesSummary{catalogSummary("3001", "离线测试剧")},
		},
		detail: catalogDetail("3001", "离线测试剧"),
	}
	worker, err := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "item-worker", Lease: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	runA, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("run A: %v", err)
	}
	first, err := repo.ListRunItems(ctx, runA.ID, 1, 50)
	if err != nil {
		t.Fatalf("list run A: %v", err)
	}
	if len(first.Items) == 0 {
		t.Fatal("run A has no rows")
	}
	// A second run collects the same candidate again.
	runB, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun B: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("run B: %v", err)
	}
	again, err := repo.ListRunItems(ctx, runA.ID, 1, 50)
	if err != nil {
		t.Fatalf("list run A again: %v", err)
	}
	if len(again.Items) != len(first.Items) {
		t.Fatalf("run A lost rows: %d -> %d", len(first.Items), len(again.Items))
	}
	second, err := repo.ListRunItems(ctx, runB.ID, 1, 50)
	if err != nil {
		t.Fatalf("list run B: %v", err)
	}
	if len(second.Items) == 0 {
		t.Fatal("run B has no rows")
	}
}

// TestRunItemRecordsTheFailedSourceRequest proves the failure of a source
// request leaves a row with its stage and the source error code.
func TestRunItemRecordsTheFailedSourceRequest(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan := catalogPlan(t, repo, ctx)
	web := &fakeWeb{
		page: &biz.SeriesPage{
			Channel: "hot-drama", Page: 1, TotalPages: 1,
			Items: []biz.SeriesSummary{catalogSummary("3002", "失败测试剧")},
		},
		detailErr: errors.New("source request failed"),
	}
	worker, err := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "fail-worker", Lease: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	items, err := repo.ListRunItems(ctx, run.ID, 1, 50)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	found := false
	for _, item := range items.Items {
		if item.Stage == biz.RunItemStageDetail && item.Result == biz.RunItemResultFailed {
			found = true
			if item.ErrorCode == "" || item.ErrorMessage == "" {
				t.Fatalf("the failed row has no error: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("no failed detail row: %+v", items.Items)
	}
}

// TestRunItemRecordRejectsAStaleWorker proves the failure record uses the
// same lease fence as the other writes.
func TestRunItemRecordRejectsAStaleWorker(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan := catalogPlan(t, repo, ctx)
	web := &fakeWeb{
		page: &biz.SeriesPage{
			Channel: "hot-drama", Page: 1, TotalPages: 1,
			Items: []biz.SeriesSummary{catalogSummary("3003", "围栏测试剧")},
		},
		detail: catalogDetail("3003", "围栏测试剧"),
	}
	worker, err := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "fence-worker", Lease: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	items, err := repo.ListRunItems(ctx, run.ID, 1, 50)
	if err != nil || len(items.Items) == 0 {
		t.Fatalf("list items: %v", err)
	}
	// A worker that never held the lease cannot append a record.
	err = repo.RecordRunItem(ctx, run.ID, "other-worker", run.Version, items.Items[0].CandidateID, "3003", biz.RunItemStageDetail, biz.RunItemResultFailed, "X", "message")
	if !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("a stale worker recorded a row: %v", err)
	}
}

// TestRunItemsPageBeyondFifty proves the rows page completely.
func TestRunItemsPageBeyondFifty(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan := catalogPlan(t, repo, ctx)
	web := &fakeWeb{
		page: &biz.SeriesPage{
			Channel: "hot-drama", Page: 1, TotalPages: 1,
			Items: []biz.SeriesSummary{catalogSummary("3004", "分页测试剧")},
		},
		detail: catalogDetail("3004", "分页测试剧"),
	}
	worker, err := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "page-worker", Lease: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Fill the run with more rows than one page holds. The rows are written
	// directly: the run already finished, so its lease is gone and the
	// fenced write would be refused on purpose.
	for index := 0; index < 55; index++ {
		if _, err := client.IngestionRunItem.Create().
			SetRunID(run.ID).
			SetCandidateID(1).
			SetCandidateExternalID("3004").
			SetStage(biz.RunItemStageSummary).
			SetResult(biz.RunItemResultSuccess).
			SetAttempt(1).
			Save(ctx); err != nil {
			t.Fatalf("insert item %d: %v", index, err)
		}
	}
	seen := map[int64]bool{}
	page := 1
	for {
		result, err := repo.ListRunItems(ctx, run.ID, page, 20)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if result.Total < 55 {
			t.Fatalf("total = %d", result.Total)
		}
		if len(result.Items) == 0 {
			break
		}
		for _, item := range result.Items {
			if seen[item.ID] {
				t.Fatalf("item %d appears twice", item.ID)
			}
			seen[item.ID] = true
		}
		page++
		if page > 10 {
			t.Fatal("the paging does not end")
		}
	}
	if len(seen) < 55 {
		t.Fatalf("paged rows = %d", len(seen))
	}
}

// TestRunItemRecordsTheFailedDetailThenTheRetry proves the recovery path of
// the real worker: the source request fails, the run is retried through the
// existing entry point and the second attempt succeeds. Both attempts stay
// in the same run with an increasing attempt number.
func TestRunItemRecordsTheFailedDetailThenTheRetry(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan := catalogPlan(t, repo, ctx)
	web := &fakeWeb{
		page: &biz.SeriesPage{
			Channel: "hot-drama", Page: 1, TotalPages: 1,
			Items: []biz.SeriesSummary{catalogSummary("3005", "恢复测试剧")},
		},
		detailErr: errors.New("source temporarily unavailable"),
	}
	worker, err := biz.NewIngestWorker(repo, web, biz.IngestWorkerConfig{WorkerID: "retry-worker", Lease: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	failed, err := repo.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != biz.RunStatusFailed {
		t.Fatalf("run status = %s", failed.Status)
	}
	items, err := repo.ListRunItems(ctx, run.ID, 1, 50)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	var failure *biz.IngestionRunItem
	for index := range items.Items {
		if items.Items[index].Stage == biz.RunItemStageDetail && items.Items[index].Result == biz.RunItemResultFailed {
			failure = &items.Items[index]
		}
	}
	if failure == nil {
		t.Fatalf("no failed detail row: %+v", items.Items)
	}
	firstError := failure.ErrorMessage
	if firstError == "" {
		t.Fatal("the failed row has no error message")
	}

	// The source recovers and the run is retried through the existing entry
	// point.
	web.detailErr = nil
	web.detail = catalogDetail("3005", "恢复测试剧")
	if _, err := repo.RetryRun(ctx, run.ID, failed.Version, 0, 0, "retry after a source failure"); err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	if _, err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	after, err := repo.ListRunItems(ctx, run.ID, 1, 50)
	if err != nil {
		t.Fatalf("list items again: %v", err)
	}
	var success *biz.IngestionRunItem
	for index := range after.Items {
		if after.Items[index].Stage == biz.RunItemStageDetail && after.Items[index].Result == biz.RunItemResultSuccess {
			success = &after.Items[index]
		}
	}
	if success == nil {
		t.Fatalf("no successful detail row: %+v", after.Items)
	}
	if success.CandidateID != failure.CandidateID {
		t.Fatalf("the retry used another candidate: %d != %d", success.CandidateID, failure.CandidateID)
	}
	if success.Attempt <= failure.Attempt {
		t.Fatalf("the attempt number did not grow: %d -> %d", failure.Attempt, success.Attempt)
	}
	// The original error is still there.
	var kept bool
	for _, item := range after.Items {
		if item.Result == biz.RunItemResultFailed && item.ErrorMessage == firstError {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("the original failure was lost: %+v", after.Items)
	}
}

// TestRunItemRecordRejectsAnExpiredLease proves the fence with the real
// lease: the worker and the version are correct, but the lease expired.
func TestRunItemRecordRejectsAnExpiredLease(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	plan := catalogPlan(t, repo, ctx)
	run, err := repo.CreateRun(ctx, plan, biz.RunKindRanking, 0)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	claimed, err := repo.ClaimRun(ctx, "lease-worker", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimRun: %+v err=%v", claimed, err)
	}
	// The lease really expires.
	if _, err := client.IngestionRun.UpdateOneID(run.ID).
		SetLeaseUntil(time.Now().UTC().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatalf("expire the lease: %v", err)
	}
	err = repo.RecordRunItem(ctx, run.ID, "lease-worker", claimed.Version, 1, "3006", biz.RunItemStageDetail, biz.RunItemResultFailed, "X", "message")
	if !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("an expired lease was accepted: %v", err)
	}
	items, err := repo.ListRunItems(ctx, run.ID, 1, 50)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items.Items) != 0 {
		t.Fatalf("the rejected write left rows: %+v", items.Items)
	}

	// A new worker takes the run over; the old one cannot write any more.
	taken, err := repo.ClaimRun(ctx, "second-worker", time.Minute)
	if err != nil || taken == nil {
		t.Fatalf("take over: %+v err=%v", taken, err)
	}
	err = repo.RecordRunItem(ctx, run.ID, "lease-worker", claimed.Version, 1, "3006", biz.RunItemStageDetail, biz.RunItemResultFailed, "X", "message")
	if !errors.Is(err, biz.ErrRunLeaseLost) {
		t.Fatalf("the replaced worker wrote: %v", err)
	}
}

// contentFixture builds a series with a working draft and a published
// revision.
func contentFixture(t *testing.T, client *ent.Client) (int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	series, err := client.ContentSeries.Create().
		SetSourceCode("hongguo").SetExternalID("content-fixture").SetTitle("在架标题").
		SetDescription("在架简介").SetStatus("published").SetRevision(2).SetVersion(1).Save(ctx)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	draft, err := client.ContentRevision.Create().
		SetSeriesID(series.ID).SetRevisionNo(2).SetState(biz.RevisionStateDraft).SetVersion(1).
		SetTitle("草稿标题").SetDescription("草稿简介").Save(ctx)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	published, err := client.ContentRevision.Create().
		SetSeriesID(series.ID).SetRevisionNo(1).SetState(biz.RevisionStateApproved).SetVersion(3).
		SetTitle("在架标题").SetDescription("在架简介").Save(ctx)
	if err != nil {
		t.Fatalf("published: %v", err)
	}
	if _, err := client.ContentSeries.UpdateOneID(series.ID).
		SetCurrentDraftRevisionID(draft.ID).SetPublishedRevisionID(published.ID).Save(ctx); err != nil {
		t.Fatalf("point the series at its revisions: %v", err)
	}
	if _, err := client.RevisionEpisode.Create().
		SetRevisionID(published.ID).SetExternalID("ep-1").SetEpisodeNo(1).SetSelected(true).Save(ctx); err != nil {
		t.Fatalf("episode: %v", err)
	}
	return series.ID, draft.ID, published.ID
}

// TestUpdateRevisionChangesOnlyTheWorkingDraft proves the edit reaches the
// working draft only and leaves the published revision alone.
func TestUpdateRevisionChangesOnlyTheWorkingDraft(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, draftID, publishedID := contentFixture(t, client)

	view, err := repo.UpdateRevision(ctx, seriesID, draftID, 1, true, "新的草稿标题", false, "")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if view.Revision == nil || view.Revision.ID != draftID {
		t.Fatalf("the wrong revision changed")
	}
	if view.Revision.Title != "新的草稿标题" {
		t.Fatalf("title = %q", view.Revision.Title)
	}
	if view.Revision.Description != "草稿简介" {
		t.Fatalf("description = %q", view.Revision.Description)
	}
	published, err := client.ContentRevision.Get(ctx, publishedID)
	if err != nil {
		t.Fatal(err)
	}
	if published.Title != "在架标题" || published.Version != 3 {
		t.Fatalf("the published revision changed")
	}
	count, err := client.RevisionEpisode.Query().Where(revisionepisode.RevisionIDEQ(publishedID)).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("the published selection changed: %d err=%v", count, err)
	}
	if _, err := repo.UpdateRevision(ctx, seriesID, draftID, int(view.Revision.Version), false, "", true, ""); err != nil {
		t.Fatalf("clear the description: %v", err)
	}
	cleared, err := client.ContentRevision.Get(ctx, draftID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Description != "" || cleared.Title != "新的草稿标题" {
		t.Fatalf("the fields are wrong: %+v", cleared)
	}
}

// TestUpdateRevisionRejectsEveryOtherTarget proves the target has to be the
// working draft of the series.
func TestUpdateRevisionRejectsEveryOtherTarget(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, draftID, publishedID := contentFixture(t, client)

	if _, err := repo.UpdateRevision(ctx, seriesID, publishedID, 3, true, "x", false, ""); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("the published revision was edited: %v", err)
	}
	other, err := client.ContentRevision.Create().
		SetSeriesID(999).SetRevisionNo(1).SetState(biz.RevisionStateDraft).SetVersion(1).
		SetTitle("别的系列").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateRevision(ctx, seriesID, other.ID, 1, true, "x", false, ""); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("a revision of another series was edited: %v", err)
	}
	if _, err := repo.UpdateRevision(ctx, seriesID, draftID, 99, true, "x", false, ""); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("a stale version was accepted: %v", err)
	}
	// A draft of the same series that is not the working draft is refused.
	otherDraft, err := client.ContentRevision.Create().
		SetSeriesID(seriesID).SetRevisionNo(3).SetState(biz.RevisionStateDraft).SetVersion(1).
		SetTitle("另一个草稿").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateRevision(ctx, seriesID, otherDraft.ID, 1, true, "x", false, ""); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("a draft that is not the working draft was edited: %v", err)
	}
}

// TestUpdateRevisionConcurrentEditsKeepOneWinner proves two edits with the
// same version cannot both succeed.
func TestUpdateRevisionConcurrentEditsKeepOneWinner(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, draftID, _ := contentFixture(t, client)

	// The first edit wins and moves the version.
	first, err := repo.UpdateRevision(ctx, seriesID, draftID, 1, true, "先手", false, "")
	if err != nil {
		t.Fatalf("first edit: %v", err)
	}
	version := int(first.Revision.Version)
	// Two requests with the same version run at the same time: at most one of
	// them may succeed and the other has to answer a conflict.
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			_, err := repo.UpdateRevision(ctx, seriesID, draftID, version, true, fmt.Sprintf("并发 %d", index), false, "")
			results <- err
		}(index)
	}
	var ok, conflict int
	for index := 0; index < 2; index++ {
		err := <-results
		switch {
		case err == nil:
			ok++
		case errors.Is(err, biz.ErrRevisionConflict):
			conflict++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d, want exactly one winner", ok, conflict)
	}
	// The database keeps the value of the winner and the version grew once.
	stored, err := client.ContentRevision.Get(ctx, draftID)
	if err != nil {
		t.Fatal(err)
	}
	if int(stored.Version) != version+1 {
		t.Fatalf("version = %d, want %d", stored.Version, version+1)
	}
	if stored.Title != "先手" && stored.Title != "并发 0" && stored.Title != "并发 1" {
		t.Fatalf("the stored title is not the winner's: %q", stored.Title)
	}
	// A stale version always answers a conflict.
	if _, err := repo.UpdateRevision(ctx, seriesID, draftID, version, true, "落后", false, ""); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("a stale version was accepted: %v", err)
	}
}

// TestCreateRevisionInheritsAndProtects proves the new draft inherits the
// metadata, the selection and the frozen material, never overwrites a
// working draft without being asked, and keeps the history intact.
func TestCreateRevisionInheritsAndProtects(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, draftID, publishedID := contentFixture(t, client)
	// The draft of the series freezes one material.
	asset, err := client.MediaAsset.Create().
		SetProvider("local").SetBucket("objects").SetRegion("local").
		SetObjectKey("openduanju-media/episodes/" + fmt.Sprint(time.Now().UnixNano()) + ".mp4").SetObjectVersion("v1").
		SetContentType("video/mp4").SetKind("episode").SetState("ready").
		SetRevisionID(draftID).SetEpisodeID(1).SetDurationMs(2000).SetWidth(320).SetHeight(240).
		Save(ctx)
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	episode, err := client.RevisionEpisode.Query().Where(revisionepisode.RevisionIDEQ(publishedID)).Only(ctx)
	if err != nil {
		t.Fatalf("episode: %v", err)
	}
	if _, err := client.RevisionEpisode.UpdateOneID(episode.ID).
		SetAssetID(asset.ID).SetAssetState("ready").Save(ctx); err != nil {
		t.Fatalf("freeze the asset: %v", err)
	}

	// A series with a working draft does not get a second one silently.
	if _, err := repo.CreateRevision(ctx, seriesID, 0, 0, 0); !errors.Is(err, biz.ErrDraftExists) {
		t.Fatalf("the working draft was replaced silently: %v", err)
	}
	// The explicit replacement creates the new draft.
	view, err := repo.CreateRevision(ctx, seriesID, publishedID, draftID, 1)
	if err != nil {
		t.Fatalf("create the revision: %v", err)
	}
	if view.Revision == nil || view.Revision.ID == draftID {
		t.Fatalf("the draft was not replaced: %+v", view.Revision)
	}
	if view.Revision.State != biz.RevisionStateDraft {
		t.Fatalf("the new revision is not a draft: %s", view.Revision.State)
	}
	if view.Revision.Version != 1 {
		t.Fatalf("the new revision inherited a version: %d", view.Revision.Version)
	}
	if view.Revision.RevisionNo <= 1 {
		t.Fatalf("the revision number was reused: %d", view.Revision.RevisionNo)
	}
	// The metadata of the base revision is inherited.
	if view.Revision.Title != "在架标题" {
		t.Fatalf("the metadata was not inherited: %q", view.Revision.Title)
	}
	// The selection and the frozen material travel with the new draft.
	episodes, err := client.RevisionEpisode.Query().Where(revisionepisode.RevisionIDEQ(view.Revision.ID)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0].AssetID != asset.ID || !episodes[0].Selected {
		t.Fatalf("the selection or the frozen material was not inherited: %+v", episodes)
	}
	// The old revision and the published revision stay as they are.
	old, err := client.ContentRevision.Get(ctx, draftID)
	if err != nil {
		t.Fatal(err)
	}
	if old.ID != draftID || old.State != biz.RevisionStateDraft {
		t.Fatalf("the old draft changed: %+v", old)
	}
	published, err := client.ContentRevision.Get(ctx, publishedID)
	if err != nil {
		t.Fatal(err)
	}
	if published.State != biz.RevisionStateApproved || published.Version != 3 {
		t.Fatalf("the published revision changed: %+v", published)
	}
	// A new draft never inherits an approval or a publication.
	stored, err := client.ContentRevision.Get(ctx, view.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AuthorizationVersion != 0 || stored.AuthorizationHash != "" {
		t.Fatalf("the new draft inherited the authorization: %+v", stored)
	}
	if stored.State != biz.RevisionStateDraft {
		t.Fatalf("the new draft inherited an approval: %s", stored.State)
	}
}

// TestCreateRevisionConcurrentCreatesOneDraft proves two concurrent requests
// cannot leave two working drafts behind.
func TestCreateRevisionConcurrentCreatesOneDraft(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, _, _ := contentFixture(t, client)
	// The series has no working draft yet.
	if _, err := client.ContentSeries.UpdateOneID(seriesID).ClearCurrentDraftRevisionID().Save(ctx); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			_, err := repo.CreateRevision(ctx, seriesID, 0, 0, 0)
			results <- err
		}()
	}
	var ok, conflict int
	for index := 0; index < 2; index++ {
		err := <-results
		switch {
		case err == nil:
			ok++
		case errors.Is(err, biz.ErrDraftExists):
			conflict++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
	// The fixture already holds one draft row; the concurrent requests may
	// add exactly one more, and the series points at one of them.
	count, err := client.ContentRevision.Query().
		Where(contentrevision.SeriesIDEQ(seriesID), contentrevision.StateEQ(biz.RevisionStateDraft)).
		Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("the series has %d draft rows, want the old one and one new", count)
	}
	series, err := client.ContentSeries.Get(ctx, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if series.CurrentDraftRevisionID == nil {
		t.Fatal("the series has no working draft")
	}
}

// TestCreateRevisionChecksTheBaseAndTheReplacement proves the base revision
// has to belong to the series and that only an editable draft of the current
// version can be replaced.
func TestCreateRevisionChecksTheBaseAndTheReplacement(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, draftID, publishedID := contentFixture(t, client)

	// A base revision of another series is refused without any side effect.
	other, err := client.ContentSeries.Create().
		SetSourceCode("hongguo").SetExternalID("other-series").SetTitle("别的作品").
		SetStatus("draft").SetRevision(1).SetVersion(1).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	otherRevision, err := client.ContentRevision.Create().
		SetSeriesID(other.ID).SetRevisionNo(1).SetState(biz.RevisionStateDraft).SetVersion(1).
		SetTitle("别的修订").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRevision(ctx, seriesID, otherRevision.ID, 0, 0); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("a base revision of another series was accepted: %v", err)
	}
	unchanged, err := client.ContentSeries.Get(ctx, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.CurrentDraftRevisionID == nil || *unchanged.CurrentDraftRevisionID != draftID {
		t.Fatal("the draft pointer moved after a refused creation")
	}
	count, err := client.ContentRevision.Query().Where(contentrevision.SeriesIDEQ(seriesID)).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("the refused creation wrote %d revisions", count-2)
	}

	// A replacement with a stale version is refused.
	if _, err := repo.CreateRevision(ctx, seriesID, publishedID, draftID, 99); !errors.Is(err, biz.ErrRevisionConflict) {
		t.Fatalf("a stale replacement version was accepted: %v", err)
	}
	// A revision that is not an editable draft is not replaced as one.
	if _, err := client.ContentRevision.UpdateOneID(draftID).
		SetState(biz.RevisionStatePendingReview).Save(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := repo.CreateRevision(ctx, seriesID, publishedID, 0, 0)
	if err != nil {
		t.Fatalf("create a draft after a pending review: %v", err)
	}
	if view.Revision == nil || view.Revision.State != biz.RevisionStateDraft {
		t.Fatalf("the new revision is not a draft: %+v", view.Revision)
	}
	if view.Revision.ID == draftID {
		t.Fatal("the pending review was replaced")
	}
	// The old revision keeps its state.
	old, err := client.ContentRevision.Get(ctx, draftID)
	if err != nil {
		t.Fatal(err)
	}
	if old.State != biz.RevisionStatePendingReview {
		t.Fatalf("the old revision changed: %s", old.State)
	}
}

// TestCreateRevisionAfterAnApprovedRevision proves a pointer to an approved
// revision is history, not an editable draft: a new draft can be created
// without replacing anything.
func TestCreateRevisionAfterAnApprovedRevision(t *testing.T) {
	client := openCatalogClient(t)
	repo := data.NewSourceCatalogRepoWithDB(client)
	ctx := context.Background()
	seriesID, draftID, publishedID := contentFixture(t, client)
	// The pointer references the approved revision.
	if _, err := client.ContentSeries.UpdateOneID(seriesID).
		SetCurrentDraftRevisionID(publishedID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := repo.CreateRevision(ctx, seriesID, publishedID, 0, 0)
	if err != nil {
		t.Fatalf("create a draft after an approval: %v", err)
	}
	if view.Revision == nil || view.Revision.State != biz.RevisionStateDraft {
		t.Fatalf("the new revision is not a draft: %+v", view.Revision)
	}
	if view.Revision.ID == publishedID {
		t.Fatal("the approved revision was replaced")
	}
	// The approved revision and the old draft stay as they are.
	approved, err := client.ContentRevision.Get(ctx, publishedID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.State != biz.RevisionStateApproved {
		t.Fatalf("the approved revision changed: %s", approved.State)
	}
	old, err := client.ContentRevision.Get(ctx, draftID)
	if err != nil {
		t.Fatal(err)
	}
	if old.State != biz.RevisionStateDraft {
		t.Fatalf("the old draft changed: %s", old.State)
	}
}
