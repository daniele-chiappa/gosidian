package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/config"
)

// A save checks the languages as the server will run them, and /version
// serves the new ones at once, without a restart (IMP-148).
func TestSettings_I18nValidatedAndLive(t *testing.T) {
	f := newAdminFixture(t)
	t.Cleanup(func() { SetI18n("en", []string{"en"}) })

	for _, c := range []struct{ body, want string }{
		{`{"i18n":{"default_lang":"it","enabled_langs":["it","xx"]}}`, `no catalogue for \"xx\"`},
		{`{"i18n":{"default_lang":"de","enabled_langs":["it","en"]}}`, "not among the enabled languages"},
		{`{"i18n":{"default_lang":"it","enabled_langs":[]}}`, "cannot be empty"},
	} {
		w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", c.body, nil)
		if w.code != http.StatusBadRequest || !strings.Contains(w.body, c.want) {
			t.Errorf("%s: %d %s, want 400 with %q", c.body, w.code, w.body, c.want)
		}
	}

	ok := `{"i18n":{"default_lang":"IT","enabled_langs":["it"," En","it"]}}`
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", ok, nil); w.code != http.StatusOK {
		t.Fatalf("valid save refused: %d %s", w.code, w.body)
	}
	cfg, _ := config.Load(f.configPath)
	if cfg.I18n.DefaultLang != "it" || strings.Join(cfg.I18n.EnabledLangs, ",") != "it,en" {
		t.Errorf("saved %+v, want it and it,en", cfg.I18n)
	}
	w := f.request(http.MethodGet, "/api/v1/version", "", nil)
	var v versionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.DefaultLang != "it" || strings.Join(v.EnabledLangs, ",") != "it,en" {
		t.Errorf("/version after the save = %s, %v; want it, it,en", v.DefaultLang, v.EnabledLangs)
	}
}

// The default a GOSIDIAN_I18N_DEFAULT_LANG sets must stay among the enabled
// languages a save chooses.
func TestSettings_I18nEnvDefault(t *testing.T) {
	f := newAdminFixture(t)
	t.Cleanup(func() { SetI18n("en", []string{"en"}) })
	t.Setenv("GOSIDIAN_I18N_DEFAULT_LANG", "de")

	w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", `{"i18n":{"enabled_langs":["it","en"]}}`, nil)
	if w.code != http.StatusBadRequest || !strings.Contains(w.body, "not among the enabled languages") {
		t.Errorf("enabled list without the env default: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", `{"i18n":{"enabled_langs":["de","en"]}}`, nil); w.code != http.StatusOK {
		t.Errorf("enabled list with the env default refused: %d %s", w.code, w.body)
	}
}
