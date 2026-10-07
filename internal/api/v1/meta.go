package v1

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/gosidian/gosidian/internal/i18n"
)

// versionResponse is the JSON returned by /api/v1/version. The struct
// is filled at build time via -ldflags injection (see cmd/gosidian/main.go
// for the `version` package var) plus the static `api` discriminator
// the OpenAPI spec advertises.
type versionResponse struct {
	Version      string   `json:"version"`
	API          string   `json:"api"`
	BuildTime    string   `json:"build_time,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	DefaultLang  string   `json:"default_lang,omitempty"`
	EnabledLangs []string `json:"enabled_langs,omitempty"`
	// OpenMode is true when the server runs read-only anonymous access
	// (GOSIDIAN_OPEN_MODE=readonly). The SPA reads it at boot to allow a
	// token-less guest session instead of forcing /login. See BUG-018.
	OpenMode bool `json:"open_mode,omitempty"`
}

// Version is set by cmd/gosidian/main.go before constructing the
// router. Keeping the wiring as a package var (rather than a Deps
// field) avoids threading a string through every handler that doesn't
// need it.
var Version = "dev"

// OpenMode mirrors AuthDeps.OpenMode for the public /version endpoint, set
// by cmd/gosidian/main.go. Public so the SPA can read it before /login.
var OpenMode = false

// i18nState is the language config /version serves: the SPA starts in the
// default language and offers only the enabled ones in its selector. main
// sets it at boot and a settings save replaces it, hence the lock.
var i18nState = struct {
	sync.RWMutex
	defaultLang string
	enabled     []string
}{defaultLang: "en", enabled: []string{"en"}}

// SetI18n sets the default language and the enabled ones, already checked
// (config.I18nConfig.Effective).
func SetI18n(defaultLang string, enabled []string) {
	i18nState.Lock()
	defer i18nState.Unlock()
	i18nState.defaultLang = defaultLang
	i18nState.enabled = append([]string(nil), enabled...)
}

func currentI18n() (string, []string) {
	i18nState.RLock()
	defer i18nState.RUnlock()
	return i18nState.defaultLang, append([]string(nil), i18nState.enabled...)
}

func (r *Router) handleVersion(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	defaultLang, enabled := currentI18n()
	WriteJSON(w, http.StatusOK, versionResponse{
		Version:      Version,
		API:          "v1",
		DefaultLang:  defaultLang,
		EnabledLangs: enabled,
		OpenMode:     OpenMode,
	})
}

// handleI18nCatalog serves the raw JSON catalog for the requested
// language. The SPA loads it directly into vue-i18n. Parameter `lang`
// defaults to "en" when missing or unknown — the SPA's fallback chain
// then takes over.
//
// Scope handling: the on-disk catalogs are split by scope
// (`ui.<lang>.json`, `errors.<lang>.json`). A client can request a
// specific scope via `?scope=ui` (default) or `?scope=all` to receive the
// merged tree. An unknown scope is a 404.
func (r *Router) handleI18nCatalog(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	lang := strings.TrimSpace(req.URL.Query().Get("lang"))
	if lang == "" {
		lang = "en"
	}
	scope := strings.TrimSpace(req.URL.Query().Get("scope"))
	if scope == "" {
		scope = "ui"
	}

	cfs := i18n.CatalogFS()
	if scope == "all" {
		// Merge ui + errors into a single object keyed by scope so the
		// SPA can keep them logically separated.
		merged := map[string]any{}
		for _, s := range []string{"ui", "errors"} {
			b, err := readCatalog(cfs, s, lang)
			if err != nil {
				continue
			}
			merged[s] = rawMessage(b)
		}
		if len(merged) == 0 {
			WriteError(w, http.StatusNotFound, CodeNotFound, "no catalogs for lang "+lang)
			return
		}
		WriteJSON(w, http.StatusOK, merged)
		return
	}

	b, err := readCatalog(cfs, scope, lang)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(b)
}

// readCatalog returns the raw bytes of `catalogs/<scope>.<lang>.json`.
// Returns an error when the file is absent — the caller decides
// whether that's a 404 or a soft fallback.
func readCatalog(cfs fs.FS, scope, lang string) ([]byte, error) {
	if scope == "" || lang == "" {
		return nil, errors.New("scope and lang required")
	}
	name := path.Join("catalogs", scope+"."+lang+".json")
	return fs.ReadFile(cfs, name)
}

// rawMessage is a tiny adapter so the merged map can carry untouched
// JSON bytes for each scope without re-parsing them.
type rawMessage []byte

// MarshalJSON implements json.Marshaler so the merged "all" response
// keeps each scope's bytes verbatim.
func (m rawMessage) MarshalJSON() ([]byte, error) {
	if len(m) == 0 {
		return []byte("null"), nil
	}
	return m, nil
}
