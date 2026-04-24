package server

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/config"
)

func TestHandleThemeCSS_Default(t *testing.T) {
	// Config path points to a non-existent file — handler should fall back to defaults.
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s, _ := setupServerWithConfig(t, cfgPath)

	w := doReq(t, s, "GET", "/theme.css", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := w.Body.String()

	wants := []string{
		"--bg-base:",
		"#0B0C10",
		"--bg-elev-1:",
		"#1F2833",
		"--text-secondary:",
		"#C5C6C7",
		"--accent-cool:",
		"#66FCF1",
		"--accent-gold:",
		"#C5A021",
		"rgba(102, 252, 241, 0.12)", // derived from Electric Blue
		"rgba(197, 160, 33, 0.15)",  // derived from Gold Leaf
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("theme.css missing %q. body:\n%s", w, body)
		}
	}
}

func TestHandleThemeCSS_Custom(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Default()
	cfg.Theme.Preset = config.CustomThemePreset // unlock individual color fields
	cfg.Theme.ElectricBlue = "#FF00AA"
	cfg.Theme.GoldLeaf = "#12AB56"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s, _ := setupServerWithConfig(t, cfgPath)

	w := doReq(t, s, "GET", "/theme.css", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "--accent-cool:      #FF00AA") {
		t.Errorf("missing custom ElectricBlue: %s", body)
	}
	if !strings.Contains(body, "rgba(255, 0, 170, 0.12)") {
		t.Errorf("missing derived cool-soft: %s", body)
	}
	if !strings.Contains(body, "--accent-gold:      #12AB56") {
		t.Errorf("missing custom GoldLeaf: %s", body)
	}
	if !strings.Contains(body, "rgba(18, 171, 86, 0.15)") {
		t.Errorf("missing derived gold-soft: %s", body)
	}
}

func TestHandleThemeCSS_LightCleanPreset(t *testing.T) {
	// Preset overrides individual color fields even when those are set to
	// something else (simulates user who previously customised and then
	// switched to a named preset via the dropdown).
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Default()
	cfg.Theme.Preset = "light-clean"
	cfg.Theme.DeepSpace = "#ABCDEF" // stale custom value, must be ignored
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s, _ := setupServerWithConfig(t, cfgPath)

	w := doReq(t, s, "GET", "/theme.css", "", false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "--bg-base:          #FAFAFA") {
		t.Errorf("light-clean preset not applied; stale custom leaked. body:\n%s", body)
	}
	if strings.Contains(body, "#ABCDEF") {
		t.Errorf("stale custom DeepSpace leaked into output: %s", body)
	}
}

func TestHandleThemeCSS_HighContrastPreset(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Default()
	cfg.Theme.Preset = "high-contrast"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s, _ := setupServerWithConfig(t, cfgPath)

	w := doReq(t, s, "GET", "/theme.css", "", false)
	body := w.Body.String()
	wants := []string{
		"--bg-base:          #000000",
		"--text-secondary:   #FFFFFF",
		"--accent-cool:      #00FFFF",
		"--accent-gold:      #FFFF00",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("high-contrast preset missing %q. body:\n%s", want, body)
		}
	}
}

func TestSettingsPostThemePresetOverridesColors(t *testing.T) {
	// POSTing theme_preset=light-clean should overwrite all 5 color fields
	// with the preset palette, ignoring any hex color payload sent alongside.
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s, _ := setupServerWithConfig(t, cfgPath)

	body := "theme_preset=light-clean&theme_deep_space=%23ABCDEF"
	w := doReq(t, s, "POST", "/settings", body, false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme.Preset != "light-clean" {
		t.Errorf("Preset = %q, want light-clean", loaded.Theme.Preset)
	}
	if loaded.Theme.DeepSpace != "#FAFAFA" {
		t.Errorf("DeepSpace = %q, want preset #FAFAFA (custom hex must be ignored)", loaded.Theme.DeepSpace)
	}
}

func TestSettingsPostThemePresetUnknownRejected(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s, _ := setupServerWithConfig(t, cfgPath)

	body := "theme_preset=not-a-real-preset"
	w := doReq(t, s, "POST", "/settings", body, false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "preset tema sconosciuto") {
		t.Errorf("expected unknown-preset error, got:\n%s", w.Body.String())
	}
}

func TestSettingsPostThemeValidation(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	s, _ := setupServerWithConfig(t, cfgPath)

	// Bad hex should re-render the form with the error message, not persist.
	body := "theme_electric_blue=not-a-color"
	w := doReq(t, s, "POST", "/settings", body, false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "theme_electric_blue") {
		t.Errorf("expected validation error, got:\n%s", w.Body.String())
	}

	// Good hex should persist.
	body = "theme_electric_blue=%23FF00AA"
	w = doReq(t, s, "POST", "/settings", body, false)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme.ElectricBlue != "#FF00AA" {
		t.Errorf("ElectricBlue not saved: %q", loaded.Theme.ElectricBlue)
	}
}
