// Package i18n embeds the language catalogues the SPA loads into vue-i18n.
//
// Layout convention: one JSON per (scope, lang) pair, named
// "<scope>.<lang>.json" (e.g. "ui.it.json", "mcp.en.json", "errors.it.json").
// Scopes are logical buckets that let contributors translate one surface at
// a time without navigating the whole catalogue. /api/v1/i18n serves the
// files as they are; the Go side does no lookups of its own.
package i18n

import "embed"

//go:embed catalogs/*.json
var catalogFS embed.FS

// CatalogFS returns the embedded catalogs filesystem so other packages
// (e.g. internal/api/v1) can serve raw JSON files to clients. Files are
// named `<scope>.<lang>.json`. Read-only.
func CatalogFS() embed.FS { return catalogFS }
