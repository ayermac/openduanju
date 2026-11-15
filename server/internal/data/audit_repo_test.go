package data_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"
)

// The audit trail is only worth its rows if it is committed with the change it
// describes, recorded once, and refused when it cannot be written. These tests
// drive real use cases against a real MySQL database: the promise is about
// transactions, not about a helper being called.

// auditClient opens the disposable database with an auditor wired to it, and
// starts from an empty trail.
func auditClient(t *testing.T) (*ent.Client, *sql.DB, *data.AuditRepo) {
	t.Helper()
	client := openCatalogClient(t)
	db := openAuditSQL(t)
	resetAudit(t, db, client)
	t.Cleanup(func() { resetAudit(t, db, client) })
	return client, db, data.NewAuditRepoWithDB(client)
}

// openAuditSQL opens the same DSN the ent client uses, so a test can install a
// trigger and read rows the way an operator does.
func openAuditSQL(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testDSN(t)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open audit database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func resetAudit(t *testing.T, db *sql.DB, client *ent.Client) {
	t.Helper()
	// The trigger is a per-test tool; it must never survive a test that
	// failed halfway.
	_, _ = db.Exec("DROP TRIGGER IF EXISTS audit_block_insert")
	if _, err := client.AuditEvent.Delete().Exec(context.Background()); err != nil {
		t.Fatalf("clean audit rows: %v", err)
	}
}

// hideAuditTable makes the audit trail unwritable without touching the
// business tables: the row exists in the schema, every insert into it fails.
// Restoring happens in a cleanup, so a failed test does not poison the next
// one.
func hideAuditTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("RENAME TABLE audit_event TO audit_event_blocked"); err != nil {
		t.Fatalf("hide the audit table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("RENAME TABLE audit_event_blocked TO audit_event") })
}

// restoreAuditTable puts the trail back, so a test can assert what it holds.
func restoreAuditTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("RENAME TABLE audit_event_blocked TO audit_event"); err != nil {
		t.Fatalf("restore the audit table: %v", err)
	}
}

// auditActor returns a context that carries an authenticated operator, the way
// the session middleware provides one.
func auditActor(actorID int64, username, traceID string) context.Context {
	ctx := biz.WithAuditActor(context.Background(), biz.Actor{ID: actorID, Username: username})
	return biz.WithTraceID(ctx, traceID)
}

// listAudit reads the trail through the repository.
func listAudit(t *testing.T, repo *data.AuditRepo, query biz.AuditQuery) ([]biz.AuditEvent, int) {
	t.Helper()
	events, total, err := repo.List(context.Background(), query)
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	return events, total
}

// TestAuditRecordsTheChangeWithItsOperator covers the ordinary case: one
// successful change leaves one row that names who did it, what it was, which
// version it produced and which request it belongs to.
func TestAuditRecordsTheChangeWithItsOperator(t *testing.T) {
	client, _, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditActor(7, "operator-a", "4bf92f3577b34da6a3ce929d0e0e4736")

	plan, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "audit-plan", Name: "审计计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5,
		FetchDetails: true, FetchEpisodes: true, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	events, total := listAudit(t, repo, biz.AuditQuery{Action: biz.AuditActionPlanCreate})
	if total != 1 || len(events) != 1 {
		t.Fatalf("audit rows for one plan = %d/%d", total, len(events))
	}
	row := events[0]
	if row.Result != biz.AuditResultSuccess {
		t.Fatalf("result = %q", row.Result)
	}
	if row.ActorID != 7 || row.ActorName != "operator-a" {
		t.Fatalf("actor = %d/%q", row.ActorID, row.ActorName)
	}
	if row.ResourceType != biz.AuditResourcePlan || row.ResourceID != fmt.Sprint(plan.ID) {
		t.Fatalf("resource = %q/%q, want plan %d", row.ResourceType, row.ResourceID, plan.ID)
	}
	if row.ResourceVersion != plan.Version {
		t.Fatalf("resource version = %d, want %d", row.ResourceVersion, plan.Version)
	}
	if row.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace = %q", row.TraceID)
	}
	if row.Reason != "" {
		t.Fatalf("a success carries a reason %q", row.Reason)
	}
}

// TestAuditRefusesTheChangeWhenItsRecordCannotBeWritten is the promise of the
// slice: an operation whose audit row cannot be committed is rolled back. The
// business row must not exist afterwards.
func TestAuditRefusesTheChangeWhenItsRecordCannotBeWritten(t *testing.T) {
	client, db, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	hideAuditTable(t, db)
	ctx := auditActor(1, "admin", "trace-audit-write-failure")

	_, err = uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "audit-blocked", Name: "无法留痕", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if !errors.Is(err, biz.ErrAuditWriteFailed) {
		t.Fatalf("error = %v, want ErrAuditWriteFailed", err)
	}
	// The plan must not exist: the write and its record are one unit.
	plans, err := catalog.ListPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.Code == "audit-blocked" {
			t.Fatalf("plan was written although its audit row failed: %+v", plan)
		}
	}
	// The trail is readable again: the refused change must not have left a
	// success row behind.
	restoreAuditTable(t, db)
	events, total := listAudit(t, repo, biz.AuditQuery{Action: biz.AuditActionPlanCreate})
	if total != 0 || len(events) != 0 {
		t.Fatalf("a refused change left %d audit rows", total)
	}
}

// TestAuditRecordsAVersionConflictAsAFailure covers the refusal path: a stale
// version changes nothing and is recorded as a failure, with a stable reason
// instead of an error body.
func TestAuditRecordsAVersionConflictAsAFailure(t *testing.T) {
	client, _, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditActor(3, "operator-b", "trace-conflict")
	plan, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "audit-conflict", Name: "冲突计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	_, _ = client.AuditEvent.Delete().Exec(context.Background())

	// A stale version is refused by the repository under the lock.
	_, err = uc.UpdatePlan(ctx, plan.ID, plan.Version+9, biz.IngestionPlan{
		Name: "冲突计划 改", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if !errors.Is(err, biz.ErrPlanVersion) {
		t.Fatalf("error = %v, want ErrPlanVersion", err)
	}
	events, total := listAudit(t, repo, biz.AuditQuery{Action: biz.AuditActionPlanUpdate})
	if total != 1 || len(events) != 1 {
		t.Fatalf("failure rows = %d/%d", total, len(events))
	}
	row := events[0]
	if row.Result != biz.AuditResultFailure {
		t.Fatalf("result = %q", row.Result)
	}
	if row.Reason != "VERSION_CONFLICT" {
		t.Fatalf("reason = %q", row.Reason)
	}
	if strings.Contains(row.Reason, "conflict") {
		t.Fatalf("the row kept the error message instead of a code: %q", row.Reason)
	}
	// No side effect: the stored name did not change.
	stored, err := catalog.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != plan.Name {
		t.Fatalf("the refused update changed the plan: %q", stored.Name)
	}
}

// TestAuditRecordsAPlanEditThatKeepsItsChannel covers the console edit: the
// request names the editable fields only, and the stored source and channel are
// kept. The row then names the new version of the plan.
func TestAuditRecordsAPlanEditThatKeepsItsChannel(t *testing.T) {
	client, _, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditActor(5, "operator-d", "trace-plan-edit")
	plan, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "audit-edit", Name: "可编辑计划", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	// The console sends no source or channel: they are not editable fields.
	updated, err := uc.UpdatePlan(ctx, plan.ID, plan.Version, biz.IngestionPlan{
		Name: "可编辑计划 改", PageStart: 1, PageEnd: 2, RequestBudget: 6, ItemBudget: 6, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdatePlan without source and channel: %v", err)
	}
	if updated.SourceCode != "hongguo" || updated.Channel != "hot-drama" {
		t.Fatalf("the stored channel was not kept: %+v", updated)
	}
	events, total := listAudit(t, repo, biz.AuditQuery{Action: biz.AuditActionPlanUpdate})
	if total != 1 || len(events) != 1 {
		t.Fatalf("edit rows = %d/%d", total, len(events))
	}
	if events[0].Result != biz.AuditResultSuccess || events[0].ResourceVersion != updated.Version {
		t.Fatalf("row = %+v, want success at version %d", events[0], updated.Version)
	}
}

// TestAuditRecordsAnIdempotentReplayOnce covers the repeated submission: the
// second call returns the work of the first and is not recorded as a second
// change.
func TestAuditRecordsAnIdempotentReplayOnce(t *testing.T) {
	client, _, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditActor(2, "operator-c", "trace-replay")
	input := biz.ManualSeriesInput{
		Title:       "重复提交的作品",
		Description: "同一份表单提交两次",
		Episodes:    []biz.ManualEpisodeInput{{Number: 1}, {Number: 2}},
		RequestID:   "req-replay-1",
	}
	first, err := uc.CreateManualSeries(ctx, input)
	if err != nil {
		t.Fatalf("CreateManualSeries: %v", err)
	}
	second, err := uc.CreateManualSeries(ctx, input)
	if err != nil {
		t.Fatalf("repeated CreateManualSeries: %v", err)
	}
	if first.Series.ID != second.Series.ID {
		t.Fatalf("the replay created another work: %d vs %d", first.Series.ID, second.Series.ID)
	}
	events, total := listAudit(t, repo, biz.AuditQuery{Action: biz.AuditActionSeriesCreate})
	if total != 1 || len(events) != 1 {
		t.Fatalf("audit rows after a replay = %d/%d, want exactly one change", total, len(events))
	}
	if events[0].ResourceID != fmt.Sprint(first.Series.ID) {
		t.Fatalf("resource = %q", events[0].ResourceID)
	}
}

// TestAuditListReportsADatabaseFailure is the read side: an unreadable list is
// an error, never an empty page.
func TestAuditListReportsADatabaseFailure(t *testing.T) {
	client, db, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditActor(1, "admin", "trace-read-failure")
	if _, err := uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "audit-read", Name: "读取失败", SourceCode: "hongguo", Channel: "hot-drama",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if _, err := db.Exec("RENAME TABLE audit_event TO audit_event_hidden"); err != nil {
		t.Fatalf("hide the audit table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("RENAME TABLE audit_event_hidden TO audit_event")
	})
	events, total, err := repo.List(context.Background(), biz.AuditQuery{})
	if err == nil {
		t.Fatalf("a broken table answered %d rows/%d total instead of an error", len(events), total)
	}
	if events != nil || total != 0 {
		t.Fatalf("a failed read returned data: %d/%d", total, len(events))
	}
}

// TestAuditListPagesInAStableOrder covers the paging promise: walking the pages
// visits every row exactly once, even when several rows share an instant.
func TestAuditListPagesInAStableOrder(t *testing.T) {
	client, _, repo := auditClient(t)
	ctx := context.Background()
	// Every row is written with the same created_at: the identity has to break
	// the tie, otherwise a page boundary would repeat or skip a row.
	for index := 0; index < 7; index++ {
		if _, err := client.AuditEvent.Create().
			SetTraceID("trace-page").
			SetActorID(1).
			SetActorName("admin").
			SetAction(biz.AuditActionPlanUpdate).
			SetResourceType(biz.AuditResourcePlan).
			SetResourceID(fmt.Sprint(index)).
			SetResult(biz.AuditResultSuccess).
			Save(ctx); err != nil {
			t.Fatalf("seed audit row: %v", err)
		}
	}
	seen := map[int64]bool{}
	for page := 1; page <= 4; page++ {
		events, total, err := repo.List(ctx, biz.AuditQuery{Page: page, PageSize: 2})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != 7 {
			t.Fatalf("page %d total = %d, want 7", page, total)
		}
		for _, event := range events {
			if seen[event.ID] {
				t.Fatalf("row %d appeared on two pages", event.ID)
			}
			seen[event.ID] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("paging visited %d of 7 rows", len(seen))
	}
}

// TestFailedAuditRecordKeepsTheOriginalError covers a broken trail on the
// failure path: the caller still sees the business error, and the lost record
// is a log problem, not a second error.
func TestFailedAuditRecordKeepsTheOriginalError(t *testing.T) {
	client, db, repo := auditClient(t)
	auditor := mustAuditor(t, repo)
	catalog := data.NewSourceCatalogRepoWithDB(client)
	uc, err := biz.NewSourceCatalog(catalog, &fakeWeb{}, auditor, nil)
	if err != nil {
		t.Fatal(err)
	}
	hideAuditTable(t, db)
	ctx := auditActor(1, "admin", "trace-failure-record")

	// A plan that names an unknown source entry is refused before any write.
	_, err = uc.CreatePlan(ctx, biz.IngestionPlan{
		Code: "audit-invalid-entry", Name: "非法入口", SourceCode: "hongguo", Channel: "not-a-channel",
		PageStart: 1, PageEnd: 1, RequestBudget: 5, ItemBudget: 5, Enabled: true,
	})
	if err == nil {
		t.Fatal("an unknown entry was accepted")
	}
	if errors.Is(err, biz.ErrAuditWriteFailed) {
		t.Fatalf("the audit failure replaced the business error: %v", err)
	}
	var sourceErr *biz.SourceError
	if !errors.As(err, &sourceErr) {
		t.Fatalf("error = %v, want the config error of the unknown entry", err)
	}
}

// testDSN returns the disposable database the integration tests run against.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set; the audit tests need a disposable MySQL database")
	}
	return dsn
}

// mustAuditor builds the recorder the use cases write through.
func mustAuditor(t *testing.T, repo *data.AuditRepo) *biz.Auditor {
	t.Helper()
	auditor, err := biz.NewAuditor(repo, nil)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	return auditor
}
