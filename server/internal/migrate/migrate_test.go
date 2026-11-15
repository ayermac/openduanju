package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"openduanju-server/internal/migrate"

	_ "github.com/go-sql-driver/mysql"
)

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "simple",
			script: "CREATE TABLE a (id INT); INSERT INTO a VALUES (1);",
			want:   []string{"CREATE TABLE a (id INT)", "INSERT INTO a VALUES (1)"},
		},
		{
			name:   "semicolon in string",
			script: "INSERT INTO a VALUES ('x;y');",
			want:   []string{"INSERT INTO a VALUES ('x;y')"},
		},
		{
			name:   "escaped quote and doubled quote",
			script: "INSERT INTO a VALUES ('it\\'s'); INSERT INTO a VALUES ('it''s');",
			want:   []string{"INSERT INTO a VALUES ('it\\'s')", "INSERT INTO a VALUES ('it''s')"},
		},
		{
			name:   "comments",
			script: "-- leading comment\nCREATE TABLE a (id INT); # trailing\n/* block ; */ INSERT INTO a VALUES (1);",
			want:   []string{"CREATE TABLE a (id INT)", "INSERT INTO a VALUES (1)"},
		},
		{
			name:   "backtick identifier with semicolon",
			script: "CREATE TABLE `a;b` (id INT);",
			want:   []string{"CREATE TABLE `a;b` (id INT)"},
		},
		{
			name:   "block comment keeps token separation",
			script: "SELECT a/*comment*/b;",
			want:   []string{"SELECT a b"},
		},
		{
			name:   "last statement without semicolon",
			script: "SELECT 1",
			want:   []string{"SELECT 1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := migrate.SplitStatements(tc.script)
			if err != nil {
				t.Fatalf("SplitStatements: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("statements = %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("statement %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSplitStatementsRejectsUnterminated(t *testing.T) {
	if _, err := migrate.SplitStatements("SELECT 'open"); err == nil {
		t.Fatal("unterminated string must fail")
	}
	if _, err := migrate.SplitStatements("/* open"); err == nil {
		t.Fatal("unterminated block comment must fail")
	}
}

func TestLoadValidatesFileNamesAndVersions(t *testing.T) {
	if _, err := migrate.Load(fstest.MapFS{
		"bad.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}); err == nil {
		t.Fatal("file without a version must fail")
	}
	if _, err := migrate.Load(fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"0001_b.sql": &fstest.MapFile{Data: []byte("SELECT 2;")},
	}); err == nil {
		t.Fatal("duplicate version must fail")
	}
	if _, err := migrate.Load(fstest.MapFS{
		"0001_empty.sql": &fstest.MapFile{Data: []byte("\n  \n")},
	}); err == nil {
		t.Fatal("empty migration must fail")
	}
}

func TestLoadNormalizesLineEndings(t *testing.T) {
	unix, err := migrate.Load(fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE a (id INT);\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	windows, err := migrate.Load(fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE a (id INT);\r\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unix[0].Checksum != windows[0].Checksum {
		t.Fatal("CRLF and LF files must produce the same checksum")
	}
}

func TestUpAgainstRealMySQL(t *testing.T) {
	db := openTestDB(t)
	table := "schema_migrations_test"
	dropTables(t, db, table, "migrate_test_a", "migrate_test_b")
	t.Cleanup(func() { dropTables(t, db, table, "migrate_test_a", "migrate_test_b") })

	fsys := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE IF NOT EXISTS migrate_test_a (id BIGINT PRIMARY KEY) ENGINE=InnoDB;")},
		"0002_b.sql": &fstest.MapFile{Data: []byte("CREATE TABLE IF NOT EXISTS migrate_test_b (id BIGINT PRIMARY KEY) ENGINE=InnoDB;")},
	}
	ctx := context.Background()
	report, err := migrate.Up(ctx, db, fsys, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(report.Applied) != 2 || report.AlreadyApplied != 0 || len(report.Pending) != 0 {
		t.Fatalf("first run = %+v", report)
	}
	report, err = migrate.Up(ctx, db, fsys, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(report.Applied) != 0 || report.AlreadyApplied != 2 {
		t.Fatalf("idempotent run = %+v", report)
	}

	fsys["0003_c.sql"] = &fstest.MapFile{Data: []byte("ALTER TABLE migrate_test_a ADD COLUMN name VARCHAR(32) NULL;")}
	report, err = migrate.Up(ctx, db, fsys, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("incremental Up: %v", err)
	}
	if len(report.Applied) != 1 || report.Applied[0].Version != 3 {
		t.Fatalf("incremental run = %+v", report)
	}
	status, err := migrate.Status(ctx, db, fsys, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Pending) != 0 || status.AlreadyApplied != 3 {
		t.Fatalf("status = %+v", status)
	}
}

func TestUpRejectsChangedAppliedMigration(t *testing.T) {
	db := openTestDB(t)
	table := "schema_migrations_changed"
	dropTables(t, db, table, "migrate_changed")
	t.Cleanup(func() { dropTables(t, db, table, "migrate_changed") })

	original := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE IF NOT EXISTS migrate_changed (id BIGINT PRIMARY KEY) ENGINE=InnoDB;")},
	}
	ctx := context.Background()
	if _, err := migrate.Up(ctx, db, original, migrate.Options{Table: table}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	changed := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE IF NOT EXISTS migrate_changed (id BIGINT PRIMARY KEY, extra INT NULL) ENGINE=InnoDB;")},
	}
	_, err := migrate.Up(ctx, db, changed, migrate.Options{Table: table})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("changed migration must fail with a checksum error, got %v", err)
	}
}

func TestFailedMigrationIsNotRecorded(t *testing.T) {
	db := openTestDB(t)
	table := "schema_migrations_failed"
	dropTables(t, db, table, "migrate_failed")
	t.Cleanup(func() { dropTables(t, db, table, "migrate_failed") })

	broken := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE IF NOT EXISTS migrate_failed (id BIGINT PRIMARY KEY) ENGINE=InnoDB;")},
		"0002_b.sql": &fstest.MapFile{Data: []byte("THIS IS NOT SQL;")},
	}
	ctx := context.Background()
	if _, err := migrate.Up(ctx, db, broken, migrate.Options{Table: table}); err == nil {
		t.Fatal("broken migration must fail")
	}
	if count := countRows(t, db, table); count != 1 {
		t.Fatalf("recorded versions = %d, want only version 1", count)
	}
	fixed := fstest.MapFS{
		"0001_a.sql": broken["0001_a.sql"],
		"0002_b.sql": &fstest.MapFile{Data: []byte("ALTER TABLE migrate_failed ADD COLUMN name VARCHAR(32) NULL;")},
	}
	report, err := migrate.Up(ctx, db, fixed, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("fixed Up: %v", err)
	}
	if len(report.Applied) != 1 || report.Applied[0].Version != 2 {
		t.Fatalf("fixed run = %+v", report)
	}
}

func TestPartialDDLFailureLeavesTraceAndRecovers(t *testing.T) {
	db := openTestDB(t)
	table := "schema_migrations_partial"
	dropTables(t, db, table, table+"_failures", "migrate_partial", "migrate_partial_extra")
	t.Cleanup(func() {
		dropTables(t, db, table, table+"_failures", "migrate_partial", "migrate_partial_extra")
	})

	broken := fstest.MapFS{
		"0001_partial.sql": &fstest.MapFile{Data: []byte("CREATE TABLE migrate_partial (id BIGINT PRIMARY KEY) ENGINE=InnoDB;")},
		"0002_multi.sql": &fstest.MapFile{Data: []byte(
			"CREATE TABLE IF NOT EXISTS migrate_partial_extra (id BIGINT PRIMARY KEY) ENGINE=InnoDB; THIS IS NOT SQL;")},
	}
	ctx := context.Background()
	_, err := migrate.Up(ctx, db, broken, migrate.Options{Table: table})
	if err == nil {
		t.Fatal("second statement must fail")
	}
	if !tableExists(t, db, "migrate_partial") || !tableExists(t, db, "migrate_partial_extra") {
		t.Fatal("earlier DDL in the failed file must remain applied, not rolled back")
	}
	if count := countRows(t, db, table); count != 1 {
		t.Fatalf("recorded versions = %d, want only 0001", count)
	}
	failures := countFailures(t, db, table)
	if failures != 1 {
		t.Fatalf("failure rows = %d, want 1", failures)
	}
	version, statement := latestFailure(t, db, table)
	if version != 2 || statement != 2 {
		t.Fatalf("failure = version %d statement %d, want 2/2", version, statement)
	}

	// Re-running the same broken file repeats the whole file; the idempotent
	// first statement succeeds again, and a new failure row is appended.
	if _, err := migrate.Up(ctx, db, broken, migrate.Options{Table: table}); err == nil {
		t.Fatal("repeat run must fail until the migration is repaired")
	}
	if count := countFailures(t, db, table); count != 2 {
		t.Fatalf("failure history = %d, want 2", count)
	}
	if count := countRows(t, db, table); count != 1 {
		t.Fatalf("repeat run recorded a failed version: %d", count)
	}

	fixed := fstest.MapFS{
		"0001_partial.sql": broken["0001_partial.sql"],
		"0002_multi.sql": &fstest.MapFile{Data: []byte(
			"CREATE TABLE IF NOT EXISTS migrate_partial_extra (id BIGINT PRIMARY KEY) ENGINE=InnoDB; " +
				"ALTER TABLE migrate_partial_extra ADD COLUMN note VARCHAR(16) NULL;")},
	}
	report, err := migrate.Up(ctx, db, fixed, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("repaired Up: %v", err)
	}
	if len(report.Applied) != 1 || report.Applied[0].Version != 2 {
		t.Fatalf("repaired run = %+v", report)
	}
	if !columnExists(t, db, "migrate_partial_extra", "note") {
		t.Fatal("second statement of the repaired migration was not applied")
	}
	if count := countFailures(t, db, table); count != 2 {
		t.Fatalf("failure history must be retained, got %d", count)
	}
	status, err := migrate.Status(ctx, db, fixed, migrate.Options{Table: table})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Pending) != 0 || status.AlreadyApplied != 2 || len(status.Failures) != 2 {
		t.Fatalf("status = %+v", status)
	}
}

func TestUpHonorsAdvisoryLock(t *testing.T) {
	db := openTestDB(t)
	table := "schema_migrations_lock"
	dropTables(t, db, table, "migrate_locked")
	t.Cleanup(func() { dropTables(t, db, table, "migrate_locked") })

	holder, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	var acquired sql.NullInt64
	if err := holder.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 1)", table+"_schema_migrate").Scan(&acquired); err != nil || acquired.Int64 != 1 {
		t.Fatalf("hold lock: acquired=%v err=%v", acquired, err)
	}
	defer func() {
		_, _ = holder.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", table+"_schema_migrate")
	}()

	fsys := fstest.MapFS{
		"0001_a.sql": &fstest.MapFile{Data: []byte("CREATE TABLE IF NOT EXISTS migrate_locked (id BIGINT PRIMARY KEY) ENGINE=InnoDB;")},
	}
	_, err = migrate.Up(context.Background(), db, fsys, migrate.Options{Table: table, LockTimeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("a held lock must stop the second runner, got %v", err)
	}
}

func TestOptionsRejectUnsafeTableName(t *testing.T) {
	db := openTestDB(t)
	_, err := migrate.Status(context.Background(), db, fstest.MapFS{}, migrate.Options{Table: "bad table; DROP"})
	if err == nil {
		t.Fatal("unsafe table name must fail")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set; migration integration tests need a disposable MySQL database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func dropTables(t *testing.T, db *sql.DB, tables ...string) {
	t.Helper()
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS=0"); err != nil {
		t.Fatalf("disable fk checks: %v", err)
	}
	for _, table := range tables {
		for _, name := range []string{table, table + "_failures"} {
			if _, err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", name)); err != nil {
				t.Fatalf("drop %s: %v", name, err)
			}
		}
	}
	if _, err := db.Exec("SET FOREIGN_KEY_CHECKS=1"); err != nil {
		t.Fatalf("enable fk checks: %v", err)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", table)).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func countFailures(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", table+"_failures")).Scan(&count); err != nil {
		t.Fatalf("count failures: %v", err)
	}
	return count
}

func latestFailure(t *testing.T, db *sql.DB, table string) (int64, int) {
	t.Helper()
	var version int64
	var statement int
	if err := db.QueryRow(fmt.Sprintf("SELECT version, statement_index FROM `%s` ORDER BY id DESC LIMIT 1", table+"_failures")).
		Scan(&version, &statement); err != nil {
		t.Fatalf("read latest failure: %v", err)
	}
	return version, statement
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", name).Scan(&count); err != nil {
		t.Fatalf("table exists %s: %v", name, err)
	}
	return count > 0
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count); err != nil {
		t.Fatalf("column exists %s.%s: %v", table, column, err)
	}
	return count > 0
}
