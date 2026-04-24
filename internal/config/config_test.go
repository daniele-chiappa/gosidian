package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_Missing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Git.Enabled {
		t.Error("default should have git disabled")
	}
	if cfg.Git.Branch != "main" {
		t.Errorf("default branch = %q", cfg.Git.Branch)
	}
	if cfg.Git.Debounce != 30*time.Second {
		t.Errorf("default debounce = %v", cfg.Git.Debounce)
	}
}

func TestSaveAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Git.Enabled = true
	cfg.Git.Remote = "https://git.example/vault.git"
	cfg.Git.Branch = "main"
	cfg.Git.Push = true
	cfg.Git.TokenEnv = "TOKEN"
	cfg.Git.Debounce = 45 * time.Second

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Git.Enabled || loaded.Git.Remote != cfg.Git.Remote || loaded.Git.Debounce != cfg.Git.Debounce {
		t.Errorf("round-trip lost data: %+v", loaded.Git)
	}
}

func TestApplyEnv(t *testing.T) {
	cfg := Default()

	t.Setenv("GOSIDIAN_GIT_ENABLED", "true")
	t.Setenv("GOSIDIAN_GIT_REMOTE", "https://g.example/v.git")
	t.Setenv("GOSIDIAN_GIT_BRANCH", "dev")
	t.Setenv("GOSIDIAN_GIT_AUTHOR_NAME", "Bot")
	t.Setenv("GOSIDIAN_GIT_AUTHOR_EMAIL", "bot@ex")
	t.Setenv("GOSIDIAN_GIT_DEBOUNCE", "12s")
	t.Setenv("GOSIDIAN_GIT_PUSH", "1")
	t.Setenv("GOSIDIAN_GIT_TOKEN_ENV", "MYTOK")

	if err := cfg.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Git.Enabled || cfg.Git.Remote != "https://g.example/v.git" ||
		cfg.Git.Branch != "dev" || cfg.Git.AuthorName != "Bot" ||
		cfg.Git.AuthorEmail != "bot@ex" || cfg.Git.Debounce != 12*time.Second ||
		!cfg.Git.Push || cfg.Git.TokenEnv != "MYTOK" {
		t.Errorf("env not applied: %+v", cfg.Git)
	}
}

func TestApplyEnv_EmptyDoesNotReset(t *testing.T) {
	cfg := Default()
	cfg.Git.Remote = "kept"
	// No env vars set — empty strings should not wipe existing values.
	if err := cfg.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	if cfg.Git.Remote != "kept" {
		t.Errorf("empty env overwrote existing: %q", cfg.Git.Remote)
	}
}

func TestApplyEnv_InvalidDuration(t *testing.T) {
	cfg := Default()
	t.Setenv("GOSIDIAN_GIT_DEBOUNCE", "not-a-duration")
	if err := cfg.ApplyEnv(); err == nil {
		t.Errorf("invalid duration should error")
	}
}

func TestDefault_Theme(t *testing.T) {
	cfg := Default()
	cases := map[string]string{
		"DeepSpace":    "#0B0C10",
		"Gunmetal":     "#1F2833",
		"SilverMist":   "#C5C6C7",
		"ElectricBlue": "#66FCF1",
		"GoldLeaf":     "#C5A021",
	}
	got := map[string]string{
		"DeepSpace":    cfg.Theme.DeepSpace,
		"Gunmetal":     cfg.Theme.Gunmetal,
		"SilverMist":   cfg.Theme.SilverMist,
		"ElectricBlue": cfg.Theme.ElectricBlue,
		"GoldLeaf":     cfg.Theme.GoldLeaf,
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("Theme.%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestValidHexColor(t *testing.T) {
	valid := []string{"#0B0C10", "#ffffff", "#000000", "#abcDEF", "#123456"}
	for _, s := range valid {
		if !ValidHexColor(s) {
			t.Errorf("ValidHexColor(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "#abc", "ffffff", "#gggggg", "#1234567", "0B0C10", "  #0B0C10  "}
	for _, s := range invalid {
		if ValidHexColor(s) {
			t.Errorf("ValidHexColor(%q) = true, want false", s)
		}
	}
}

func TestThemeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Theme.ElectricBlue = "#FF00AA"
	cfg.Theme.GoldLeaf = "#AABBCC"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Theme.ElectricBlue != "#FF00AA" {
		t.Errorf("ElectricBlue lost round-trip: %q", loaded.Theme.ElectricBlue)
	}
	if loaded.Theme.GoldLeaf != "#AABBCC" {
		t.Errorf("GoldLeaf lost round-trip: %q", loaded.Theme.GoldLeaf)
	}
	// Untouched fields stay at default.
	if loaded.Theme.DeepSpace != "#0B0C10" {
		t.Errorf("DeepSpace changed unexpectedly: %q", loaded.Theme.DeepSpace)
	}
}

func TestLoad_File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[git]
enabled = true
remote = "https://example.com/x.git"
branch = "trunk"
author_name = "Bot"
author_email = "bot@example.com"
commit_debounce = "5s"
push = true
token_env = "GITEA_TOKEN"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Git.Enabled {
		t.Error("expected enabled")
	}
	if cfg.Git.Branch != "trunk" {
		t.Errorf("branch = %q", cfg.Git.Branch)
	}
	if cfg.Git.Debounce != 5*time.Second {
		t.Errorf("debounce = %v", cfg.Git.Debounce)
	}
	if cfg.Git.TokenEnv != "GITEA_TOKEN" {
		t.Errorf("token_env = %q", cfg.Git.TokenEnv)
	}
}
