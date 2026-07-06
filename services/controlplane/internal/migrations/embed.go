// Package migrations embeds the SQL migration files so they can be applied by
// the migration runner without relying on the filesystem at runtime.
package migrations

import "embed"

// FS holds every *.sql migration in this directory, applied in lexical order.
//
//go:embed *.sql
var FS embed.FS
