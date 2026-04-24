// Package i18n loads language catalogues from embedded JSON files and exposes
// a single-entry point for translating message keys at request time.
//
// Layout convention: one JSON per (scope, lang) pair, named
// "<scope>.<lang>.json" (e.g. "ui.it.json", "mcp.en.json", "errors.it.json").
// Scopes are logical buckets that let contributors translate one surface at
// a time without navigating the whole catalogue.
//
// Fallback chain for T(lang, scope, key):
//   1. exact lang catalogue for scope
//   2. default-lang catalogue for scope
//   3. key literal (surfaces missing-translation bugs immediately in dev)
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

//go:embed catalogs/*.json
var catalogFS embed.FS

// Catalog is the in-memory representation of all loaded translation files.
// Keys are namespaced as "<scope>.<dotted.key>" so both the scope and the
// JSON tree walk are done at Load time once.
type Catalog struct {
	defaultLang string
	mu          sync.RWMutex
	// entries[lang][key] = translated string
	entries map[string]map[string]string
}

// Load reads every catalogs/*.json file from the embedded FS and builds the
// catalogue. It never fails on missing scope files (MVP scopes may be thin);
// only JSON parse errors are fatal.
func Load(defaultLang string) (*Catalog, error) {
	if defaultLang == "" {
		defaultLang = "en"
	}
	c := &Catalog{defaultLang: defaultLang, entries: map[string]map[string]string{}}
	files, err := fs.ReadDir(catalogFS, "catalogs")
	if err != nil {
		return nil, fmt.Errorf("read catalog dir: %w", err)
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// name pattern: <scope>.<lang>.json. The scope segment is a
		// filename convention for contributors only — keys inside the JSON
		// are used verbatim (no prefixing), so callers pass e.g.
		// "tokens.create_button" regardless of which file it lives in.
		// Duplicate keys across scopes collide; keep the convention that
		// each logical key lives in exactly one scope file.
		base := strings.TrimSuffix(name, ".json")
		dot := strings.IndexByte(base, '.')
		if dot < 0 {
			continue
		}
		lang := base[dot+1:]

		data, err := catalogFS.ReadFile("catalogs/" + name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var flat map[string]any
		if err := json.Unmarshal(data, &flat); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if _, ok := c.entries[lang]; !ok {
			c.entries[lang] = map[string]string{}
		}
		flattenInto(c.entries[lang], "", flat)
	}
	return c, nil
}

// flattenInto walks a nested JSON object and stores scalar string leaves at
// their dotted path. Non-string leaves are ignored. An empty prefix skips
// the leading "." separator.
func flattenInto(dst map[string]string, prefix string, src map[string]any) {
	for k, v := range src {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch vv := v.(type) {
		case string:
			dst[full] = vv
		case map[string]any:
			flattenInto(dst, full, vv)
		}
	}
}

// DefaultLang returns the language used as ultimate fallback.
func (c *Catalog) DefaultLang() string {
	if c == nil {
		return "en"
	}
	return c.defaultLang
}

// Has reports whether the catalogue has any entry for the given language
// (first-component match, e.g. "it" matches the "it" catalogue when caller
// passed "it-IT").
func (c *Catalog) Has(lang string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.entries[primaryTag(lang)]
	return ok
}

// T translates a scope + dotted key to the given language, falling back to
// default_lang and finally to the literal key when no translation exists.
func (c *Catalog) T(lang, scopedKey string, args ...any) string {
	if c == nil {
		return formatArgs(scopedKey, args)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	prim := primaryTag(lang)
	if m := c.entries[prim]; m != nil {
		if v, ok := m[scopedKey]; ok {
			return formatArgs(v, args)
		}
	}
	if prim != c.defaultLang {
		if m := c.entries[c.defaultLang]; m != nil {
			if v, ok := m[scopedKey]; ok {
				return formatArgs(v, args)
			}
		}
	}
	// Fallback: surface the key itself so missing translations are visible.
	return formatArgs(scopedKey, args)
}

// formatArgs applies fmt.Sprintf when args are provided. Catalog values use
// Go format verbs (%s, %d, ...) for interpolation — simple, no plural forms
// in v1.7 (add golang.org/x/text/message if plurals become needed).
func formatArgs(s string, args []any) string {
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// primaryTag returns the first language tag of an Accept-Language-style value
// (before ',' and before ';q='), lowercased and reduced to the base (e.g.
// "it-IT" → "it"). Empty input maps to empty.
func primaryTag(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ""
	}
	if i := strings.IndexAny(lang, ",;"); i >= 0 {
		lang = lang[:i]
	}
	lang = strings.TrimSpace(lang)
	if i := strings.IndexByte(lang, '-'); i >= 0 {
		lang = lang[:i]
	}
	return strings.ToLower(lang)
}
