// Package migrations provides deployment-owned schema changes; the server never applies them.
package migrations

import "embed"

// Files contains explicit, ordered up/down migrations.
//
//go:embed *.sql
var Files embed.FS
