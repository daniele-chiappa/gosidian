package v1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/config"
)

// TestProjectFlags_UseAnchorsGlobalsPersist covers the M5 toggle surface:
// PUT /projects/{slug} sets use_anchors / use_globals, the view echoes them,
// and the per-project store persists them (independently of the other flags).
func TestProjectFlags_UseAnchorsGlobalsPersist(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")

	// Enable both flags in one PUT.
	w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha",
		`{"use_anchors":true,"use_globals":true}`, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	for _, want := range []string{`"use_anchors":true`, `"use_globals":true`} {
		if !strings.Contains(w.body, want) {
			t.Errorf("response missing %q: %s", want, w.body)
		}
	}
	if got := f.projects.Get("Alpha"); !got.UseAnchors || !got.UseGlobals {
		t.Fatalf("store not persisted: %+v", got)
	}

	// Toggling one off must leave the other intact (read-modify-write).
	w = f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha",
		`{"use_anchors":false}`, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if got := f.projects.Get("Alpha"); got.UseAnchors || !got.UseGlobals {
		t.Errorf("partial update wrong: %+v (want UseAnchors=false, UseGlobals=true)", got)
	}
	if !strings.Contains(w.body, `"use_anchors":false`) || !strings.Contains(w.body, `"use_globals":true`) {
		t.Errorf("view out of sync: %s", w.body)
	}
}

// TestProjectFlags_UseTagVocabularyPersist covers the IMP-075 toggle: PUT
// /projects/{slug} sets use_tag_vocabulary, the view echoes it, the store
// persists it, and a partial update of another flag leaves it intact.
func TestProjectFlags_UseTagVocabularyPersist(t *testing.T) {
	f := newAdminFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")

	w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha",
		`{"use_tag_vocabulary":true}`, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"use_tag_vocabulary":true`) {
		t.Errorf("response missing use_tag_vocabulary: %s", w.body)
	}
	if !f.projects.Get("Alpha").UseTagVocabulary {
		t.Fatalf("store not persisted: %+v", f.projects.Get("Alpha"))
	}
	if !f.projects.UsesTagVocabulary("Alpha") {
		t.Error("UsesTagVocabulary helper out of sync")
	}

	// Partial update of an unrelated flag must not clear it.
	w = f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha",
		`{"public":true}`, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !f.projects.Get("Alpha").UseTagVocabulary {
		t.Errorf("partial update cleared use_tag_vocabulary: %+v", f.projects.Get("Alpha"))
	}
}

// TestSettings_ExposesMasterSwitches asserts the read-only master switches
// (GOSIDIAN_ANCHORS_ENABLED / GOSIDIAN_GLOBAL_ENABLED) reach the SPA so it can
// tell whether a project's use_anchors/use_globals flag has any effect.
func TestSettings_ExposesMasterSwitches(t *testing.T) {
	f := newAdminFixture(t)
	cfg := config.Default()
	cfg.AgentAnchors.Enabled = true
	cfg.Global.Enabled = false
	if err := config.Save(f.configPath, cfg); err != nil {
		t.Fatal(err)
	}

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/settings", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"anchors_enabled":true`) {
		t.Errorf("anchors_enabled not exposed: %s", w.body)
	}
	if !strings.Contains(w.body, `"globals_enabled":false`) {
		t.Errorf("globals_enabled not exposed: %s", w.body)
	}
}
