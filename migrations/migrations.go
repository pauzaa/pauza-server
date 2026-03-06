package migrations

import "embed"

// FS embeds the SQL migration files so that the binary does not depend on
// the working directory at runtime.
//
//go:embed *.sql
var FS embed.FS
