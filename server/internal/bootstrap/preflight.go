package bootstrap

import (
	"fmt"
	"strings"

	"openduanju-server/internal/conf"
)

// Error is a startup validation failure. The code is stable so operators
// and scripts can react to it without parsing the message.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// Startup rejection codes.
const (
	CodeDriverUnsupported = "database_driver_unsupported"
	CodeDSNMissing        = "database_dsn_missing"
	CodeDSNInvalid        = "database_dsn_invalid"
	CodeNameMismatch      = "database_name_mismatch"
	// CodeAdminTokenMissing is retained for scripts that still expect the
	// retired shared-token check; nothing emits it any more.
	CodeAdminTokenMissing = "admin_auth_token_missing"
)

// Check validates the configuration the server needs before it opens a
// database connection, starts the worker or the scheduler, or listens on a
// port. It performs no I/O, so a rejected configuration leaves every system
// untouched. The returned identity describes the database that would be
// used; it never contains credentials.
func Check(cfg *conf.Bootstrap) (Identity, error) {
	database := cfg.GetData().GetDatabase()
	switch driver := database.GetDriver(); driver {
	case "mysql":
	case "":
		return Identity{}, &Error{Code: CodeDriverUnsupported, Message: "database driver is not configured"}
	default:
		return Identity{}, &Error{
			Code:    CodeDriverUnsupported,
			Message: fmt.Sprintf("unsupported database driver %q", driver),
		}
	}

	source := strings.TrimSpace(database.GetSource())
	if source == "" {
		return Identity{}, &Error{
			Code:    CodeDSNMissing,
			Message: "database dsn is empty; set KRATOS_DATABASE_SOURCE",
		}
	}
	identity, err := ParseDSN(source)
	if err != nil {
		return Identity{}, &Error{Code: CodeDSNInvalid, Message: err.Error()}
	}

	if expected := strings.TrimSpace(database.GetExpectedName()); expected != "" && expected != identity.Database {
		return Identity{}, &Error{
			Code: CodeNameMismatch,
			Message: fmt.Sprintf("database %q does not match the expected %q",
				identity.Database, expected),
		}
	}

	// The shared bearer token is retired, so it is no longer required. Admin
	// calls authenticate with a staff session; the first administrator is
	// created by openduanju-staff-init while the server is stopped.
	return identity, nil
}
