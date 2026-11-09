package i18n

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
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

// TestCatalogs_EveryLanguageWhole wants every language to say every text
// (IMP-220, F-20): a text missing in German showed the English one, in a
// page marked German. Each catalogue has the English keys, no other, none
// empty, with the same placeholders and as many plural forms.
func TestCatalogs_EveryLanguageWhole(t *testing.T) {
	for _, scope := range []string{"ui", "errors", "audit", "board"} {
		en := flatCatalog(t, scope, "en")
		for _, lang := range Languages() {
			if lang == "en" {
				continue
			}
			got := flatCatalog(t, scope, lang)
			for k, want := range en {
				text, ok := got[k]
				switch {
				case !ok:
					t.Errorf("%s.%s.json: %s is missing", scope, lang, k)
				case strings.TrimSpace(text) == "":
					t.Errorf("%s.%s.json: %s is empty", scope, lang, k)
				case !slices.Equal(placeholders(text), placeholders(want)):
					t.Errorf("%s.%s.json: %s has %v, English %v", scope, lang, k, placeholders(text), placeholders(want))
				case !samePlural(pluralForms(text), pluralForms(want)):
					t.Errorf("%s.%s.json: %s has %d plural forms, English %d", scope, lang, k, pluralForms(text), pluralForms(want))
				}
			}
			for k := range got {
				if _, ok := en[k]; !ok {
					t.Errorf("%s.%s.json: %s is not in English", scope, lang, k)
				}
			}
		}
	}
}

// flatCatalog reads <scope>.<lang>.json as key paths to texts.
func flatCatalog(t *testing.T, scope, lang string) map[string]string {
	t.Helper()
	data, err := fs.ReadFile(CatalogFS(), "catalogs/"+scope+"."+lang+".json")
	if err != nil {
		t.Fatalf("%s.%s.json: %v", scope, lang, err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("%s.%s.json: %v", scope, lang, err)
	}
	out := map[string]string{}
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for k, v := range node {
			switch v := v.(type) {
			case string:
				out[prefix+k] = v
			case map[string]any:
				walk(prefix+k+".", v)
			default:
				t.Errorf("%s.%s.json: %s%s is neither a text nor a group", scope, lang, prefix, k)
			}
		}
	}
	walk("", obj)
	return out
}

var placeholder = regexp.MustCompile(`\{[^}]*\}`)

// placeholders lists the {name} and {'…'} parts a text uses, sorted, each
// once: a plural form may leave {n} out ("one note | {n} notes").
func placeholders(text string) []string {
	out := placeholder.FindAllString(text, -1)
	slices.Sort(out)
	return slices.Compact(out)
}

// pluralForms counts the forms vue-i18n splits a text into at |.
func pluralForms(text string) int {
	return strings.Count(placeholder.ReplaceAllString(text, ""), "|") + 1
}

// samePlural: one form or two are each language's own ("{n} car." for
// both numbers; "non collocato | non collocati" where English has one),
// but three forms, the first for none, stay three.
func samePlural(got, want int) bool {
	return got == want || (got <= 2 && want <= 2)
}

func TestLanguages(t *testing.T) {
	if got := strings.Join(Languages(), ","); got != "de,en,es,fr,it" {
		t.Errorf("Languages() = %s, want de,en,es,fr,it (one per ui.<lang>.json)", got)
	}
	if !Supported("it") || Supported("xx") || Supported("") {
		t.Error("Supported must follow the ui catalogues")
	}
}

// TestCatalogs_AuditActions wants every action of the audit log named in
// words in every language (IMP-220, F-25 and F-20): the web UI shows the
// phrase, and the filter's menu lists the groups. An action in no group, or
// in two, or a phrase for an action the server no longer writes, fails.
// The phrases are a catalogue of their own, audit.<lang>.json, which only
// the audit's page loads; the first load's ui catalogue does not hold them.
func TestCatalogs_AuditActions(t *testing.T) {
	for _, lang := range Languages() {
		ui, err := fs.ReadFile(CatalogFS(), "catalogs/ui."+lang+".json")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(ui), `"action_groups"`) {
			t.Errorf("ui.%s.json holds the audit's actions, which audit.%s.json keeps out of the first load", lang, lang)
		}
		data, err := fs.ReadFile(CatalogFS(), "catalogs/audit."+lang+".json")
		if err != nil {
			t.Fatal(err)
		}
		var cat struct {
			Admin struct {
				Audit struct {
					Actions      map[string]map[string]string `json:"actions"`
					ActionGroups map[string]string            `json:"action_groups"`
				} `json:"audit"`
			} `json:"admin"`
		}
		if err := json.Unmarshal(data, &cat); err != nil {
			t.Fatal(err)
		}
		groups := cat.Admin.Audit.Actions
		seen := map[string]string{}
		for g, phrases := range groups {
			if cat.Admin.Audit.ActionGroups[g] == "" {
				t.Errorf("%s: group %q has no name in admin.audit.action_groups", lang, g)
			}
			for code, phrase := range phrases {
				if other, dup := seen[code]; dup {
					t.Errorf("%s: %q is in groups %q and %q", lang, code, other, g)
				}
				seen[code] = g
				if strings.TrimSpace(phrase) == "" {
					t.Errorf("%s: %q has an empty phrase", lang, code)
				}
			}
		}
		for _, a := range audit.Actions {
			if _, ok := seen[string(a)]; !ok {
				t.Errorf("%s: audit action %q has no phrase in admin.audit.actions", lang, a)
			}
			delete(seen, string(a))
		}
		for code := range seen {
			t.Errorf("%s: admin.audit.actions names %q, which the server does not write", lang, code)
		}
		for g := range cat.Admin.Audit.ActionGroups {
			if _, ok := groups[g]; !ok {
				t.Errorf("%s: group %q is named but holds no action", lang, g)
			}
		}
	}
}
