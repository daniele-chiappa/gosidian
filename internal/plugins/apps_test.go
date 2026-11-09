package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A plugin's apps (IMP-226): the pages gosidian opens in a window of its
// own, for the notes of a type, framed from the address of the manifest.
func TestManifest_Apps(t *testing.T) {
	m := func(apps ...map[string]any) []byte {
		list := []any{}
		for _, a := range apps {
			list = append(list, a)
		}
		b, _ := json.Marshal(map[string]any{"name": "layout", "version": "0.1.0", "gosidian": 1, "apps": list,
			"permissions": map[string]any{"files": false, "network": []string{}}})
		return b
	}
	app := func(change map[string]any) map[string]any {
		a := map[string]any{"name": "layout", "title": "Impaginazione", "url": "https://layout.example.com/app/",
			"note_type": "layout", "bridge": "layout/1", "engine": "designcraft v0.17.0"}
		for k, v := range change {
			a[k] = v
		}
		return a
	}
	man, err := ParseManifest(m(app(nil)), "layout")
	if err != nil {
		t.Fatalf("a manifest with an app only: %v", err)
	}
	a, found := man.App("layout")
	if !found || a.URL != "https://layout.example.com/app/" || a.NoteType != "layout" || a.Bridge != "layout/1" || a.Engine != "designcraft v0.17.0" {
		t.Errorf("App(layout) = %+v, %v", a, found)
	}
	if got := strings.Join(man.AppOrigins(), " "); got != "https://layout.example.com" {
		t.Errorf("AppOrigins = %q", got)
	}
	for why, bad := range map[string]map[string]any{
		"a bad name":                  {"name": "Layout"},
		"no title":                    {"title": ""},
		"a relative address":          {"url": "/app/"},
		"another scheme":              {"url": "ftp://layout.example.com/app/"},
		"credentials":                 {"url": "https://u:p@layout.example.com/app/"},
		"a query":                     {"url": "https://layout.example.com/app/?a=1"},
		"a fragment":                  {"url": "https://layout.example.com/app/#x"},
		"a host that adds a CSP":      {"url": "https://layout.example.com;script-src/app/"},
		"a bad note type":             {"note_type": "Layout!"},
		"no note type":                {"note_type": ""},
		"a bridge without a version":  {"bridge": "layout"},
		"a relative other address":    {"alt_urls": []string{"/app/"}},
		"an other address with a CSP": {"alt_urls": []string{"https://l.example.com;script-src/app/"}},
		"too many other addresses":    {"alt_urls": []string{"http://a/", "http://b/", "http://c/", "http://d/", "http://e/"}},
	} {
		if _, err := ParseManifest(m(app(bad)), "layout"); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	if _, err := ParseManifest(m(app(nil), app(nil)), "layout"); err == nil {
		t.Error("an app twice")
	}
	if _, err := ParseManifest(m(app(nil), app(map[string]any{"name": "other"})), "layout"); err == nil {
		t.Error("two apps for one note type")
	}
	many := []map[string]any{}
	for i := 0; i <= maxApps; i++ {
		many = append(many, app(map[string]any{"name": "a" + string(rune('a'+i)), "note_type": "t" + string(rune('a'+i))}))
	}
	if _, err := ParseManifest(m(many...), "layout"); err == nil {
		t.Errorf("%d apps accepted", len(many))
	}

	other, _ := ParseManifest(m(app(map[string]any{"url": "https://elsewhere.example.com/app/"})), "layout")
	if other.SHA() == man.SHA() {
		t.Error("an app's address is not in the sha: the owner would not see it move")
	}
	alt, err := ParseManifest(m(app(map[string]any{"alt_urls": []string{"http://127.0.0.1:8099/app/", "http://localhost:8099/app/"}})), "layout")
	if err != nil {
		t.Fatalf("other addresses: %v", err)
	}
	if got := strings.Join(alt.AppOrigins(), " "); got != "https://layout.example.com http://127.0.0.1:8099 http://localhost:8099" {
		t.Errorf("AppOrigins with other addresses = %q", got)
	}
	if alt.SHA() == man.SHA() {
		t.Error("an app's other addresses are not in the sha: the owner would not read what the SPA may frame")
	}
	plain := `{"name":"agent","version":"1.0","gosidian":1,"nodes":[{"name":"run","tool":"run"}],"permissions":{"files":false,"network":[]}}`
	p, _ := ParseManifest([]byte(plain), "agent")
	if b, _ := json.Marshal(p); strings.Contains(string(b), "apps") {
		t.Errorf("a manifest without apps marshals them, and its sha moves: %s", b)
	}
}

// The apps of the accepted manifests only: one served and not accepted
// is not framed, nor shown.
func TestRegistry_Apps(t *testing.T) {
	ts := toolPlugin(t, map[string]any{"name": "layout", "version": "0.1.0", "gosidian": 1,
		"apps": []any{map[string]any{"name": "layout", "title": "Impaginazione", "url": "http://127.0.0.1:8097/app/",
			"note_type": "layout", "bridge": "layout/1"}},
		"permissions": map[string]any{"files": false, "network": []string{}}})
	reg, err := New([]Config{{Name: "layout", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	if apps, origins := reg.Apps(), reg.AppOrigins(); len(apps) != 0 || len(origins) != 0 {
		t.Errorf("before accept: %+v %v", apps, origins)
	}
	st, _ := reg.State("layout")
	if _, err := reg.Accept(ctx, "layout", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	apps := reg.Apps()
	if len(apps) != 1 || apps[0].Plugin != "layout" || apps[0].Spec.Name != "layout" || apps[0].Spec.NoteType != "layout" {
		t.Fatalf("apps = %+v", apps)
	}
	if got := strings.Join(reg.AppOrigins(), " "); got != "http://127.0.0.1:8097" {
		t.Errorf("origins %q", got)
	}
}
