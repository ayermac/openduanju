// Package bootstrap validates the runtime configuration of the server before
// it reaches any external system. The checks here are pure: they read the
// configuration and never open a connection, so a rejected configuration
// leaves the database, the worker and the scheduler untouched.
package bootstrap

import (
	"fmt"
	"strings"
)

// Identity describes the database a MySQL DSN points at. Credentials are
// deliberately not part of it so the value can be logged or shown to
// operators without leaking a secret.
type Identity struct {
	// Host is the "host:port" of the target, or the socket path for a
	// unix socket DSN.
	Host string
	// Database is the schema the DSN selects.
	Database string
}

// ParseDSN extracts the target of a MySQL DSN. A DSN without a user or
// without a database name is rejected: those two mistakes do not fail
// loudly, they silently fall back to another server or schema.
func ParseDSN(dsn string) (Identity, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return Identity{}, fmt.Errorf("dsn is empty")
	}
	authority := trimmed
	if index := strings.Index(authority, "?"); index >= 0 {
		authority = authority[:index]
	}
	slash := strings.LastIndex(authority, "/")
	if slash < 0 {
		return Identity{}, fmt.Errorf("dsn has no database name")
	}
	database := authority[slash+1:]
	if database == "" {
		return Identity{}, fmt.Errorf("dsn has no database name")
	}
	credentials := authority[:slash]
	at := strings.LastIndex(credentials, "@")
	if at < 0 {
		return Identity{}, fmt.Errorf("dsn has no user")
	}
	user := credentials[:at]
	if colon := strings.Index(user, ":"); colon >= 0 {
		user = user[:colon]
	}
	if user == "" {
		return Identity{}, fmt.Errorf("dsn has no user")
	}
	address := credentials[at+1:]
	if open := strings.Index(address, "("); open >= 0 {
		close := strings.LastIndex(address, ")")
		if close <= open {
			return Identity{}, fmt.Errorf("dsn has an unterminated address")
		}
		address = address[open+1 : close]
	}
	if address == "" {
		address = "127.0.0.1:3306"
	}
	return Identity{Host: address, Database: database}, nil
}
