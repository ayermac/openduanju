// Command openduanju-migrate applies the embedded, reviewed SQL migrations.
// It is the only supported upgrade path: the server itself never creates or
// alters business tables at startup.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"openduanju-server/internal/conf"
	"openduanju-server/internal/migrate"
	"openduanju-server/migrations"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	_ "github.com/go-sql-driver/mysql"
)

var (
	flagConf   string
	flagDir    string
	flagUp     bool
	flagStatus bool
)

func init() {
	flag.StringVar(&flagConf, "conf", "../../configs", "config path")
	flag.StringVar(&flagDir, "dir", "", "read migrations from this directory instead of the embedded set")
	flag.BoolVar(&flagUp, "up", false, "apply pending migrations")
	flag.BoolVar(&flagStatus, "status", false, "print applied and pending migrations")
}

type migrationOutput struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
}

type failureOutput struct {
	Version        int64  `json:"version"`
	Name           string `json:"name"`
	StatementIndex int    `json:"statement_index"`
	Error          string `json:"error"`
	FailedAt       string `json:"failed_at"`
}

type runOutput struct {
	OK             bool              `json:"ok"`
	Directory      string            `json:"directory"`
	AlreadyApplied int               `json:"already_applied"`
	Applied        []migrationOutput `json:"applied"`
	Pending        []migrationOutput `json:"pending"`
	Failures       []failureOutput   `json:"failures,omitempty"`
	Error          string            `json:"error,omitempty"`
}

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	if flagUp == flagStatus {
		fmt.Fprintln(os.Stderr, "choose exactly one of -up or -status")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := config.New(config.WithSource(file.NewSource(flagConf), env.NewSource("KRATOS")))
	defer c.Close()
	if err := c.Load(); err != nil {
		return reportError(fmt.Errorf("load config: %w", err), flagDir)
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		return reportError(fmt.Errorf("scan config: %w", err), flagDir)
	}
	dbCfg := bc.GetData().GetDatabase()
	if dbCfg.GetSource() == "" {
		return reportError(errors.New("database source is not configured"), flagDir)
	}
	driver := dbCfg.GetDriver()
	if driver == "" {
		driver = "mysql"
	}
	db, err := sql.Open(driver, dbCfg.GetSource())
	if err != nil {
		return reportError(fmt.Errorf("open database: %w", err), flagDir)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return reportError(fmt.Errorf("connect database: %w", err), flagDir)
	}

	var fsys fs.FS = migrations.FS
	directory := "embedded"
	if flagDir != "" {
		fsys = os.DirFS(flagDir)
		directory = flagDir
	}
	opts := migrate.Options{}
	var result *migrate.Report
	if flagUp {
		result, err = migrate.Up(ctx, db, fsys, opts)
	} else {
		result, err = migrate.Status(ctx, db, fsys, opts)
	}
	if err != nil {
		return reportError(err, directory)
	}
	output := runOutput{
		OK:             true,
		Directory:      directory,
		AlreadyApplied: result.AlreadyApplied,
		Applied:        toOutput(result.Applied),
		Pending:        toOutput(result.Pending),
		Failures:       toFailures(result.Failures),
	}
	emit(output)
	return 0
}

func toOutput(list []migrate.Migration) []migrationOutput {
	out := make([]migrationOutput, 0, len(list))
	for _, migration := range list {
		out = append(out, migrationOutput{Version: migration.Version, Name: migration.Name})
	}
	return out
}

func toFailures(list []migrate.Failure) []failureOutput {
	out := make([]failureOutput, 0, len(list))
	for _, failure := range list {
		out = append(out, failureOutput{
			Version:        failure.Version,
			Name:           failure.Name,
			StatementIndex: failure.StatementIndex,
			Error:          failure.Error,
			FailedAt:       failure.FailedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func emit(output runOutput) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(output)
}

func reportError(err error, directory string) int {
	emit(runOutput{OK: false, Directory: directory, Error: err.Error()})
	fmt.Fprintln(os.Stderr, "migration failed:", err)
	return 1
}
