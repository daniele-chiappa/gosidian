package i18n

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
)

// TestCatalogs_Parse guards what the SPA receives: every embedded catalogue
// is a JSON object named <scope>.<lang>.json, and both languages exist.
func TestCatalogs_Parse(t *testing.T) {
	files, err := fs.ReadDir(CatalogFS(), "catalogs")
	if err != nil {
		t.Fatal(err)
	}
	langs := map[string]bool{}
	for _, f := range files {
		name := f.Name()
		base, ok := strings.CutSuffix(name, ".json")
		if !ok {
			continue
		}
		_, lang, ok := strings.Cut(base, ".")
		if !ok {
			t.Errorf("%s: name is not <scope>.<lang>.json", name)
			continue
		}
		data, err := fs.ReadFile(CatalogFS(), "catalogs/"+name)
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		langs[lang] = true
	}
	for _, want := range []string{"it", "en"} {
		if !langs[want] {
			t.Errorf("no %s catalogue embedded", want)
		}
	}
}
