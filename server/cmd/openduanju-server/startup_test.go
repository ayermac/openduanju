package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openduanju-server/internal/migrate"
	"openduanju-server/migrations"

	_ "github.com/go-sql-driver/mysql"
)

// TestServerRefusesBrokenConfiguration starts the real server binary with
// configurations that must be rejected. The process has to exit before it
// opens a database connection, starts the worker or the scheduler, or
// listens on a port, and it must not write to the database.
func TestServerRefusesBrokenConfiguration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set; the startup test needs a disposable MySQL database")
	}
	db := openStartupDatabase(t, dsn)
	seedDuePlan(t, db)
	t.Cleanup(func() { cleanupStartupTables(t, db) })

	binary := buildServerBinary(t)
	cases := []struct {
		name string
		env  []string
		code string
	}{
		{
			name: "missing dsn",
			env:  []string{"KRATOS_DATABASE_SOURCE=", "KRATOS_ADMIN_AUTH_TOKEN=token"},
			code: "database_dsn_missing",
		},
		{
			name: "dsn without the kratos prefix",
			env:  []string{"DATABASE_SOURCE=" + dsn, "KRATOS_DATABASE_SOURCE=", "KRATOS_ADMIN_AUTH_TOKEN=token"},
			code: "database_dsn_missing",
		},
		{
			name: "unexpected database",
			env: []string{
				"KRATOS_DATABASE_SOURCE=" + dsn,
				"KRATOS_DATABASE_EXPECTED_NAME=openduanju_not_the_target",
				"KRATOS_ADMIN_AUTH_TOKEN=token",
			},
			code: "database_name_mismatch",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := runServerBinary(t, binary, testCase.env)
			if err == nil {
				t.Fatalf("the server started with a broken configuration: %s", output)
			}
			if !strings.Contains(output, testCase.code) {
				t.Fatalf("output does not report %q: %s", testCase.code, output)
			}
			if runs := countStartupRows(t, db, "ingestion_run"); runs != 0 {
				t.Fatalf("the rejected start wrote %d ingestion runs", runs)
			}
			if plans := countStartupRows(t, db, "ingestion_plan"); plans != 1 {
				t.Fatalf("the rejected start changed the plan count to %d", plans)
			}
		})
	}

	// A wrongly started scheduler would fire the seeded plan within a poll
	// interval; the rejected starts must leave the table untouched.
	time.Sleep(3 * time.Second)
	if runs := countStartupRows(t, db, "ingestion_run"); runs != 0 {
		t.Fatalf("a rejected start created %d ingestion runs", runs)
	}
}

func openStartupDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	if _, err := migrate.Up(ctx, db, migrations.FS, migrate.Options{}); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	cleanupStartupTables(t, db)
	return db
}

func cleanupStartupTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"ingestion_run_page", "ingestion_run", "ingestion_plan"} {
		if _, err := db.Exec("DELETE FROM `" + table + "`"); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
}

// seedDuePlan stores a plan whose cron slot is already due. A server that
// ignores the broken configuration creates a run for it.
func seedDuePlan(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO ingestion_plan (code, name, source_code, channel, page_start, page_end, request_budget, item_budget, rate_per_minute, fetch_details, fetch_episodes, enabled, version, cron, timezone, next_run_at) " +
			"VALUES ('startup-negative-plan', '启动拒绝用例', 'hongguo', 'hot-drama', 1, 1, 10, 20, 30, 1, 1, 1, 1, '* * * * *', 'UTC', UTC_TIMESTAMP(6) - INTERVAL 1 MINUTE)")
	if err != nil {
		t.Fatalf("seed plan: %v", err)
	}
}

func countStartupRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM `" + table + "`").Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func buildServerBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "openduanju-server")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, output)
	}
	return binary
}

// runServerBinary starts the server with the given environment and returns
// its output. A server that does not reject the configuration keeps running
// and is killed by the timeout, which fails the test.
func runServerBinary(t *testing.T, binary string, env []string) (string, error) {
	t.Helper()
	configDir, err := filepath.Abs("../../configs")
	if err != nil {
		t.Fatalf("resolve config dir: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-conf", configDir)
	command.Env = append(baseEnvironment(), env...)
	output, runErr := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), errors.New("the server did not exit")
	}
	return string(output), runErr
}

// baseEnvironment keeps the process environment but drops every KRATOS
// variable, so each case controls the configuration it is about.
func baseEnvironment() []string {
	kept := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "KRATOS_") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
