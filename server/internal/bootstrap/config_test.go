package bootstrap_test

import (
	"testing"

	"openduanju-server/internal/bootstrap"
	"openduanju-server/internal/conf"

	"github.com/go-kratos/kratos/v3/config"
	"github.com/go-kratos/kratos/v3/config/env"
	"github.com/go-kratos/kratos/v3/config/file"
)

// loadConfig loads the shipped configuration the same way the server does:
// the file source plus the KRATOS environment source.
func loadConfig(t *testing.T) *conf.Bootstrap {
	t.Helper()
	c := config.New(config.WithSource(
		file.NewSource("../../configs"),
		env.NewSource("KRATOS"),
	))
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		t.Fatalf("scan config: %v", err)
	}
	return &bc
}

func TestConfigResolvesDatabaseFromKratosEnvironment(t *testing.T) {
	t.Setenv("KRATOS_DATABASE_SOURCE", "openduanju:secret@tcp(127.0.0.1:13380)/openduanju_dev?parseTime=true")
	t.Setenv("KRATOS_ADMIN_AUTH_TOKEN", "token")
	identity, err := bootstrap.Check(loadConfig(t))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if identity.Database != "openduanju_dev" || identity.Host != "127.0.0.1:13380" {
		t.Fatalf("identity = %+v", identity)
	}
}

// A DSN exported without the KRATOS prefix never reaches the placeholder:
// the environment source only exposes prefixed variables. The server must
// refuse to start instead of falling back to the driver default instance.
func TestConfigRejectsDSNWithoutKratosPrefix(t *testing.T) {
	t.Setenv("DATABASE_SOURCE", "openduanju:secret@tcp(127.0.0.1:13380)/openduanju_dev?parseTime=true")
	t.Setenv("KRATOS_DATABASE_SOURCE", "")
	t.Setenv("KRATOS_ADMIN_AUTH_TOKEN", "token")
	assertCode(t, check(loadConfig(t)), bootstrap.CodeDSNMissing)
}

// An exported but empty KRATOS variable resolves the placeholder to an
// empty string, which used to fall back to the default MySQL endpoint.
func TestConfigRejectsEmptyKratosDSN(t *testing.T) {
	t.Setenv("KRATOS_DATABASE_SOURCE", "")
	t.Setenv("KRATOS_ADMIN_AUTH_TOKEN", "token")
	assertCode(t, check(loadConfig(t)), bootstrap.CodeDSNMissing)
}

func TestConfigAcceptsAnEmptySharedToken(t *testing.T) {
	// The shared token is retired; an empty value must load and pass the
	// preflight, because authentication now uses staff sessions.
	t.Setenv("KRATOS_DATABASE_SOURCE", "openduanju:secret@tcp(127.0.0.1:13380)/openduanju_dev?parseTime=true")
	t.Setenv("KRATOS_ADMIN_AUTH_TOKEN", "")
	if err := check(loadConfig(t)); err != nil {
		t.Fatalf("empty shared token: %v", err)
	}
}

func TestConfigRejectsUnexpectedDatabaseName(t *testing.T) {
	t.Setenv("KRATOS_DATABASE_SOURCE", "openduanju:secret@tcp(127.0.0.1:13380)/openduanju_accept?parseTime=true")
	t.Setenv("KRATOS_DATABASE_EXPECTED_NAME", "openduanju_dev")
	t.Setenv("KRATOS_ADMIN_AUTH_TOKEN", "token")
	assertCode(t, check(loadConfig(t)), bootstrap.CodeNameMismatch)
}
