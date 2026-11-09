package i18n

import (
	"encoding/json"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"sync"
)

// The texts the server writes in the notes itself (IMP-216 phase 2: the
// review of an iteration) come from the same catalogues as the web UI's,
// in the instance's language ([i18n] default_lang).

var (
	langMu   sync.RWMutex
	instance = "en"

	flatMu sync.Mutex
	flat   = map[string]map[string]string{} // by <scope>.<lang>
)

// SetLang sets the instance's language, [i18n] default_lang; the server
// calls it at start and when the setting changes.
func SetLang(lang string) {
	if !Supported(lang) {
		return
	}
	langMu.Lock()
	instance = lang
	langMu.Unlock()
}

// Lang is the instance's language.
func Lang() string {
	langMu.RLock()
	defer langMu.RUnlock()
	return instance
}

// Text is the text at key (a dotted path) of scope's catalogue in lang,
// English's when lang has none, the key itself when English has none
// either; each {name} of args filled in.
func Text(scope, lang, key string, args map[string]string) string {
	s, ok := catalog(scope, lang)[key]
	if !ok {
		if s, ok = catalog(scope, "en")[key]; !ok {
			s = key
		}
	}
	for k, v := range args {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// Decimal writes f with at most one decimal, with a comma in the
// languages that write one (all of ours but English).
func Decimal(lang string, f float64) string {
	s := strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// catalog is <scope>.<lang>.json as dotted keys to texts, read once;
// empty when there is none.
func catalog(scope, lang string) map[string]string {
	name := scope + "." + lang
	flatMu.Lock()
	defer flatMu.Unlock()
	if m, ok := flat[name]; ok {
		return m
	}
	m := map[string]string{}
	var obj map[string]any
	if data, err := fs.ReadFile(catalogFS, "catalogs/"+name+".json"); err == nil && json.Unmarshal(data, &obj) == nil {
		var walk func(prefix string, node map[string]any)
		walk = func(prefix string, node map[string]any) {
			for k, v := range node {
				switch v := v.(type) {
				case string:
					m[prefix+k] = v
				case map[string]any:
					walk(prefix+k+".", v)
				}
			}
		}
		walk("", obj)
	}
	flat[name] = m
	return m
}
