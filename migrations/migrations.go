// Package migrations embeds the SQL schema files used by the migrate command.
package migrations

import "embed"

// FS contains all schema migrations in lexical order.
//
//go:embed *.sql
var FS embed.FS
