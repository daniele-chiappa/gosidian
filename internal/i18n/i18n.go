// Package i18n embeds the language catalogues the SPA loads into vue-i18n.
//
// Layout convention: one JSON per (scope, lang) pair, named
// "<scope>.<lang>.json" (e.g. "ui.it.json", "errors.en.json"). Scopes are
// logical buckets that let contributors translate one surface at a time
// without navigating the whole catalogue. /api/v1/i18n serves the files as
// they are; the Go side does no lookups of its own.
package i18n

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed catalogs/*.json
var catalogFS embed.FS

// CatalogFS returns the embedded catalogs filesystem so other packages
// (e.g. internal/api/v1) can serve raw JSON files to clients. Files are
// named `<scope>.<lang>.json`. Read-only.
func CatalogFS() embed.FS { return catalogFS }

// Languages returns the codes that have a `ui` catalogue, sorted: the
// languages the web UI can show, since the SPA bundles exactly these files.
// It is the list `[i18n] enabled_langs` chooses from.
func Languages() []string {
	files, _ := fs.ReadDir(catalogFS, "catalogs")
	var out []string
	for _, f := range files {
		if lang, ok := strings.CutPrefix(f.Name(), "ui."); ok {
			if lang, ok = strings.CutSuffix(lang, ".json"); ok && lang != "" {
				out = append(out, lang)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Supported reports whether lang has a `ui` catalogue.
func Supported(lang string) bool {
	for _, l := range Languages() {
		if l == lang {
			return true
		}
	}
	return false
}
