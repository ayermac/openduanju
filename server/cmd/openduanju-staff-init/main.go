// Command openduanju-staff-init creates the first administrator.
//
// It exists so a fresh installation has no built-in account and no default
// password. The command connects to MySQL, creates one enabled administrator
// and exits: it starts no HTTP server, no worker and no scheduler. Running it
// again refuses to act while an enabled administrator exists.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"openduanju-server/internal/biz"
	"openduanju-server/internal/bootstrap"
	"openduanju-server/internal/conf"
	"openduanju-server/internal/data"
	"openduanju-server/internal/data/ent"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
	_ "github.com/go-sql-driver/mysql"
)

var (
	flagConf        string
	flagUsername    string
	flagDisplayName string
	flagPassword    string
)

func init() {
	flag.StringVar(&flagConf, "conf", "../../configs", "config path")
	flag.StringVar(&flagUsername, "username", "", "administrator username")
	flag.StringVar(&flagDisplayName, "display-name", "", "display name")
	flag.StringVar(&flagPassword, "password", "", "password (prefer stdin; a literal leaks into the shell history)")
}

type output struct {
	OK       bool   `json:"ok"`
	Username string `json:"username,omitempty"`
	ID       int64  `json:"id,omitempty"`
	Error    string `json:"error,omitempty"`
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		writeOutput(output{OK: false, Error: err.Error()})
		os.Exit(1)
	}
}

func run() error {
	username := strings.TrimSpace(flagUsername)
	if username == "" {
		return fmt.Errorf("username is required")
	}
	password, err := readPassword()
	if err != nil {
		return err
	}
	if err := biz.ValidatePassword(password); err != nil {
		return fmt.Errorf("password is too weak: %w", err)
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := bootstrap.Check(cfg); err != nil {
		return err
	}
	source := cfg.GetData().GetDatabase().GetSource()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sqlDB, err := sql.Open(cfg.GetData().GetDatabase().GetDriver(), source)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	client, err := ent.Open(cfg.GetData().GetDatabase().GetDriver(), source)
	if err != nil {
		return fmt.Errorf("open ent client: %w", err)
	}
	defer func() { _ = client.Close() }()

	// The initialisation command has no console session, so it records no
	// audit event: the row it writes names the account, and the operator who
	// ran the command is not an authenticated identity this server knows.
	accounts := biz.NewStaffAccounts(data.NewStaffRepoWithDB(client), nil)
	staff, err := accounts.EnsureBootstrap(ctx, username, strings.TrimSpace(flagDisplayName), password)
	if err != nil {
		if err == biz.ErrAlreadyInitialised {
			return fmt.Errorf("an enabled administrator already exists; the initialisation command does not reset accounts")
		}
		return err
	}
	writeOutput(output{OK: true, Username: staff.Username, ID: staff.ID})
	return nil
}

// readPassword takes the secret from stdin by default, so it does not end up
// in the shell history or in the process list.
func readPassword() (string, error) {
	if flagPassword != "" {
		return flagPassword, nil
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// loadConfig reads the same configuration file and KRATOS_ environment as the
// server, without starting it.
func loadConfig() (*conf.Bootstrap, error) {
	c := config.New(config.WithSource(file.NewSource(flagConf), env.NewSource("KRATOS")))
	defer func() { _ = c.Close() }()
	if err := c.Load(); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	var cfg conf.Bootstrap
	if err := c.Scan(&cfg); err != nil {
		return nil, fmt.Errorf("scan config: %w", err)
	}
	return &cfg, nil
}

// writeOutput prints one JSON line so a script can read the result.
func writeOutput(value output) {
	body, _ := json.Marshal(value)
	fmt.Println(string(body))
}
