// Package assets contains the reviewed, immutable game catalog bundled at build time.
package assets

import "embed"

// Europe is an embedded read-only filesystem, independent of the working directory.
//
//go:embed europe-core-v1/catalog.json europe-core-v1/catalog-manifest.json
var Europe embed.FS
