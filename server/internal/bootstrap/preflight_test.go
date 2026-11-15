package bootstrap_test

import (
	"errors"
	"testing"

	"openduanju-server/internal/bootstrap"
	"openduanju-server/internal/conf"
)

func validConfig() *conf.Bootstrap {
	return &conf.Bootstrap{
		Data: &conf.Data{
			Database: &conf.Data_Database{
				Driver:       "mysql",
				Source:       "openduanju:secret@tcp(127.0.0.1:13380)/openduanju_dev?parseTime=true",
				ExpectedName: "",
			},
		},
		Admin: &conf.Admin{AuthToken: "token"},
	}
}

func TestCheckAcceptsCompleteConfiguration(t *testing.T) {
	identity, err := bootstrap.Check(validConfig())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if identity.Database != "openduanju_dev" {
		t.Fatalf("database = %q", identity.Database)
	}
	if identity.Host != "127.0.0.1:13380" {
		t.Fatalf("host = %q", identity.Host)
	}
}

func TestCheckRejectsMissingDSN(t *testing.T) {
	cfg := validConfig()
	cfg.Data.Database.Source = "   "
	_, err := bootstrap.Check(cfg)
	assertCode(t, err, bootstrap.CodeDSNMissing)
}

func TestCheckRejectsDSNWithoutDatabase(t *testing.T) {
	cfg := validConfig()
	cfg.Data.Database.Source = "openduanju:secret@tcp(127.0.0.1:13380)/"
	assertCode(t, check(cfg), bootstrap.CodeDSNInvalid)
}

func TestCheckRejectsDSNWithoutUser(t *testing.T) {
	cfg := validConfig()
	cfg.Data.Database.Source = "tcp(127.0.0.1:13380)/openduanju_dev"
	assertCode(t, check(cfg), bootstrap.CodeDSNInvalid)
}

func TestCheckRejectsUnexpectedDriver(t *testing.T) {
	cfg := validConfig()
	cfg.Data.Database.Driver = "postgres"
	assertCode(t, check(cfg), bootstrap.CodeDriverUnsupported)
}

func TestCheckRejectsUnexpectedDatabaseName(t *testing.T) {
	cfg := validConfig()
	cfg.Data.Database.ExpectedName = "openduanju_accept"
	assertCode(t, check(cfg), bootstrap.CodeNameMismatch)
}

func TestCheckNoLongerRequiresTheSharedToken(t *testing.T) {
	// The shared bearer token is retired: an empty value must not block a
	// server that authenticates with staff sessions.
	cfg := validConfig()
	cfg.Admin.AuthToken = ""
	if err := check(cfg); err != nil {
		t.Fatalf("an empty shared token blocked startup: %v", err)
	}
}

func TestParseDSNRejectsSilentFallbacks(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no database":      "openduanju:secret@tcp(127.0.0.1:13380)",
		"empty database":   "openduanju:secret@tcp(127.0.0.1:13380)/",
		"no user":          "tcp(127.0.0.1:13380)/openduanju_dev",
		"empty user":       ":secret@tcp(127.0.0.1:13380)/openduanju_dev",
		"broken address":   "openduanju:secret@tcp(127.0.0.1:13380/openduanju_dev",
		"no user no slash": "openduanju",
	}
	for name, dsn := range cases {
		if _, err := bootstrap.ParseDSN(dsn); err == nil {
			t.Fatalf("%s: expected an error for %q", name, dsn)
		}
	}
}

func check(cfg *conf.Bootstrap) error {
	_, err := bootstrap.Check(cfg)
	return err
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *bootstrap.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v, want a bootstrap error", err)
	}
	if typed.Code != code {
		t.Fatalf("code = %q, want %q (%v)", typed.Code, code, err)
	}
}
