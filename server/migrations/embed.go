// Package migrations embeds the reviewed, versioned SQL migrations that ship
// with the server binary. Files are applied by cmd/openduanju-migrate; they are
// never created or altered at runtime.
package migrations

import "embed"

// FS contains the ordered *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
