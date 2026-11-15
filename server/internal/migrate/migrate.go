// Package migrate applies reviewed SQL files exactly once and records a
// checksum per version. It serializes runners with a MySQL advisory lock and
// refuses to run when an already-applied file changed.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTable       = "schema_migrations"
	defaultLockTimeout = 30 * time.Second
	lockSuffix         = "_schema_migrate"
)

var (
	fileNamePattern = regexp.MustCompile(`^(\d{4,})_([a-z0-9_]+)\.sql$`)
	tablePattern    = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)
)

// Options tune one migration run.
type Options struct {
	// Table defaults to schema_migrations.
	Table string
	// LockTimeout bounds the wait for the advisory lock.
	LockTimeout time.Duration
}

// Migration is one reviewed SQL file.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

// Failure is one recorded statement failure. MySQL DDL commits implicitly, so
// a failure is not a rollback: earlier statements may already be applied.
type Failure struct {
	Version        int64
	Name           string
	StatementIndex int
	Error          string
	FailedAt       time.Time
}

// Report separates already-applied versions from newly applied ones.
type Report struct {
	Applied        []Migration
	Pending        []Migration
	AlreadyApplied int
	// Failures is failure history, newest first; status reports it so a
	// partially applied migration is visible instead of silently retried.
	Failures []Failure
}

// Load reads and validates every *.sql file in fsys.
func Load(fsys fs.FS) ([]Migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	migrations := make([]Migration, 0, len(names))
	seen := make(map[int64]string, len(names))
	for _, name := range names {
		matches := fileNamePattern.FindStringSubmatch(path.Base(name))
		if matches == nil {
			return nil, fmt.Errorf("migration %q must match NNNN_description.sql", name)
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q has an invalid version", name)
		}
		if previous, ok := seen[version]; ok {
			return nil, fmt.Errorf("migration version %d is used by both %q and %q", version, previous, name)
		}
		seen[version] = name
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		body := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("migration %q is empty", name)
		}
		sum := sha256.Sum256([]byte(body))
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     matches[2],
			SQL:      body,
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	return migrations, nil
}

// Up applies pending migrations in version order.
func Up(ctx context.Context, db *sql.DB, fsys fs.FS, opts Options) (*Report, error) {
	opts = normalizeOptions(opts)
	migrations, err := Load(fsys)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}
	defer conn.Close()
	if err := ensureTables(ctx, conn, opts.Table); err != nil {
		return nil, err
	}
	if err := acquireLock(ctx, conn, opts.Table, opts.LockTimeout); err != nil {
		return nil, err
	}
	defer releaseLock(conn, opts.Table)

	applied, err := readApplied(ctx, conn, opts.Table)
	if err != nil {
		return nil, err
	}
	if err := validateApplied(applied, migrations); err != nil {
		return nil, err
	}
	report := &Report{AlreadyApplied: len(applied)}
	appliedSet := make(map[int64]struct{}, len(applied))
	for _, row := range applied {
		appliedSet[row.Version] = struct{}{}
	}
	for _, migration := range migrations {
		if _, ok := appliedSet[migration.Version]; !ok {
			report.Pending = append(report.Pending, migration)
		}
	}
	for _, migration := range report.Pending {
		statements, err := SplitStatements(migration.SQL)
		if err != nil {
			return nil, fmt.Errorf("migration %04d_%s: %w", migration.Version, migration.Name, err)
		}
		for index, statement := range statements {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				recordFailure(ctx, conn, opts.Table, migration, index+1, err)
				return nil, fmt.Errorf("migration %04d_%s statement %d failed (earlier statements are not rolled back): %w", migration.Version, migration.Name, index+1, err)
			}
		}
		if _, err := conn.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO `%s` (version, name, checksum, applied_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP(6))", opts.Table),
			migration.Version, migration.Name, migration.Checksum,
		); err != nil {
			return nil, fmt.Errorf("record migration %04d_%s: %w", migration.Version, migration.Name, err)
		}
		report.Applied = append(report.Applied, migration)
	}
	// Everything selected earlier has been applied; nothing remains pending.
	report.Pending = nil
	return report, nil
}

// Status reports applied and pending migrations without applying anything.
func Status(ctx context.Context, db *sql.DB, fsys fs.FS, opts Options) (*Report, error) {
	opts = normalizeOptions(opts)
	migrations, err := Load(fsys)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}
	defer conn.Close()
	if err := ensureTables(ctx, conn, opts.Table); err != nil {
		return nil, err
	}
	applied, err := readApplied(ctx, conn, opts.Table)
	if err != nil {
		return nil, err
	}
	if err := validateApplied(applied, migrations); err != nil {
		return nil, err
	}
	failures, err := readFailures(ctx, conn, opts.Table)
	if err != nil {
		return nil, err
	}
	report := &Report{AlreadyApplied: len(applied), Applied: applied, Failures: failures}
	appliedSet := make(map[int64]struct{}, len(applied))
	for _, row := range applied {
		appliedSet[row.Version] = struct{}{}
	}
	for _, migration := range migrations {
		if _, ok := appliedSet[migration.Version]; !ok {
			report.Pending = append(report.Pending, migration)
		}
	}
	return report, nil
}

func normalizeOptions(opts Options) Options {
	if opts.Table == "" {
		opts.Table = defaultTable
	}
	if opts.LockTimeout <= 0 {
		opts.LockTimeout = defaultLockTimeout
	}
	return opts
}

func ensureTables(ctx context.Context, conn *sql.Conn, table string) error {
	if !tablePattern.MatchString(table) {
		return fmt.Errorf("migration table %q is not a safe identifier", table)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS `%s` ("+
			"version BIGINT NOT NULL PRIMARY KEY, "+
			"name VARCHAR(255) NOT NULL, "+
			"checksum CHAR(64) NOT NULL, "+
			"applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)"+
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4", table)); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS `%s` ("+
			"id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY, "+
			"version BIGINT NOT NULL, "+
			"name VARCHAR(255) NOT NULL, "+
			"statement_index INT NOT NULL, "+
			"error VARCHAR(1000) NOT NULL, "+
			"failed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), "+
			"KEY idx_version (version)"+
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4", failuresTable(table))); err != nil {
		return fmt.Errorf("create migration failure table: %w", err)
	}
	return nil
}

func failuresTable(table string) string { return table + "_failures" }

func recordFailure(ctx context.Context, conn *sql.Conn, table string, migration Migration, index int, execErr error) {
	message := execErr.Error()
	if runes := []rune(message); len(runes) > 900 {
		message = string(runes[:900])
	}
	_, _ = conn.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO `%s` (version, name, statement_index, error) VALUES (?, ?, ?, ?)",
		failuresTable(table)), migration.Version, migration.Name, index, message)
}

func readFailures(ctx context.Context, conn *sql.Conn, table string) ([]Failure, error) {
	rows, err := conn.QueryContext(ctx, fmt.Sprintf(
		"SELECT version, name, statement_index, error, failed_at FROM `%s` ORDER BY id DESC LIMIT 20",
		failuresTable(table)))
	if err != nil {
		return nil, fmt.Errorf("read migration failures: %w", err)
	}
	defer rows.Close()
	var failures []Failure
	for rows.Next() {
		var failure Failure
		if err := rows.Scan(&failure.Version, &failure.Name, &failure.StatementIndex, &failure.Error, &failure.FailedAt); err != nil {
			return nil, fmt.Errorf("scan migration failure: %w", err)
		}
		failures = append(failures, failure)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration failures: %w", err)
	}
	return failures, nil
}

func acquireLock(ctx context.Context, conn *sql.Conn, table string, timeout time.Duration) error {
	seconds := int(timeout / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", table+lockSuffix, seconds).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return fmt.Errorf("another migration process holds lock %q", table+lockSuffix)
	}
	return nil
}

func releaseLock(conn *sql.Conn, table string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
	defer cancel()
	_, _ = conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", table+lockSuffix)
}

func readApplied(ctx context.Context, conn *sql.Conn, table string) ([]Migration, error) {
	rows, err := conn.QueryContext(ctx, fmt.Sprintf(
		"SELECT version, name, checksum FROM `%s` ORDER BY version", table))
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	var applied []Migration
	for rows.Next() {
		var row Migration
		if err := rows.Scan(&row.Version, &row.Name, &row.Checksum); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied = append(applied, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func validateApplied(applied, files []Migration) error {
	byVersion := make(map[int64]Migration, len(files))
	for _, file := range files {
		byVersion[file.Version] = file
	}
	for _, row := range applied {
		file, ok := byVersion[row.Version]
		if !ok {
			return fmt.Errorf("applied migration %d (%s) is missing from the migrations directory", row.Version, row.Name)
		}
		if !strings.EqualFold(row.Checksum, file.Checksum) {
			return fmt.Errorf("migration %04d_%s changed after it was applied (checksum mismatch)", row.Version, file.Name)
		}
	}
	return nil
}

// SplitStatements splits a reviewed SQL file into individual statements while
// respecting strings, identifiers and comments. It intentionally rejects
// unterminated constructs instead of guessing.
func SplitStatements(script string) ([]string, error) {
	var statements []string
	var current strings.Builder
	var quote rune
	var inLineComment bool
	var inBlockComment bool
	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch {
		case inLineComment:
			if char == '\n' {
				inLineComment = false
				current.WriteRune(char)
			}
			continue
		case inBlockComment:
			if char == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		case quote != 0:
			if char == '\\' && quote != '`' && i+1 < len(runes) {
				current.WriteRune(char)
				current.WriteRune(next)
				i++
				continue
			}
			current.WriteRune(char)
			if char == quote {
				if next == quote {
					current.WriteRune(next)
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch {
		case char == '-' && next == '-' && (i+2 >= len(runes) || runes[i+2] == ' ' || runes[i+2] == '\t' || runes[i+2] == '\n'):
			inLineComment = true
			i++
		case char == '#':
			inLineComment = true
		case char == '/' && next == '*':
			inBlockComment = true
			current.WriteRune(' ')
			i++
		case char == '\'' || char == '"' || char == '`':
			quote = char
			current.WriteRune(char)
		case char == ';':
			if statement := strings.TrimSpace(current.String()); statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
		default:
			current.WriteRune(char)
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quoted string")
	}
	if inBlockComment {
		return nil, errors.New("unterminated block comment")
	}
	if statement := strings.TrimSpace(current.String()); statement != "" {
		statements = append(statements, statement)
	}
	return statements, nil
}
