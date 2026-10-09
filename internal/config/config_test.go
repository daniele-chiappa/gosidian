package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestApplyEnv_MediaNotes(t *testing.T) {
	cfg := Default()
	if cfg.Vault.MediaNotes {
		t.Error("media_notes must default to false (ADR-013, opt-in)")
	}
	t.Setenv("GOSIDIAN_VAULT_MEDIA_NOTES", "true")
	if err := cfg.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Vault.MediaNotes {
		t.Error("GOSIDIAN_VAULT_MEDIA_NOTES=true should enable media notes")
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

// A config.toml written before the server-side themes went away (IMP-054)
// still loads: the [theme] section is ignored, the rest is read.
func TestLoad_OldThemeSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[theme]\npreset = \"midnight-luxury\"\ndeep_space = \"#0B0C10\"\n\n[mcp]\nwrite_per_minute = 120\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("a config with [theme] does not load: %v", err)
	}
	if cfg.MCP.WritePerMinute != 120 {
		t.Errorf("write_per_minute = %d, want 120", cfg.MCP.WritePerMinute)
	}
	// Saved again, the section is gone.
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "theme") {
		t.Errorf("the saved config still has a theme section:\n%s", b)
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

func TestSelfImprove_Defaults(t *testing.T) {
	cfg := Default()
	if cfg.SelfImprove.Enabled {
		t.Error("self-improve should default disabled")
	}
	if cfg.SelfImprove.TargetProject != "insights" {
		t.Errorf("default target_project = %q", cfg.SelfImprove.TargetProject)
	}
	if cfg.SelfImprove.EveryNCalls != 25 {
		t.Errorf("default every_n_calls = %d", cfg.SelfImprove.EveryNCalls)
	}
	if cfg.SelfImprove.CooldownMinutes != 120 {
		t.Errorf("default cooldown_minutes = %d", cfg.SelfImprove.CooldownMinutes)
	}
	if cfg.SelfImprove.MaxNudgesPerSession != 1 {
		t.Errorf("default max_nudges_per_session = %d", cfg.SelfImprove.MaxNudgesPerSession)
	}
}

func TestSelfImprove_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.SelfImprove.Enabled = true
	cfg.SelfImprove.TargetProject = "dogfood"
	cfg.SelfImprove.EveryNCalls = 50
	cfg.SelfImprove.NotifyEmail = "me@example.com"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	si := loaded.SelfImprove
	if !si.Enabled || si.TargetProject != "dogfood" || si.EveryNCalls != 50 || si.NotifyEmail != "me@example.com" {
		t.Errorf("round-trip lost data: %+v", si)
	}
	// Untouched fields keep their defaults.
	if si.CooldownMinutes != 120 || si.MaxNudgesPerSession != 1 {
		t.Errorf("defaults not preserved: %+v", si)
	}
}

func TestSelfImprove_ApplyEnv(t *testing.T) {
	cfg := Default()
	t.Setenv("GOSIDIAN_SELF_IMPROVE_ENABLED", "true")
	t.Setenv("GOSIDIAN_SELF_IMPROVE_TARGET_PROJECT", "insights-dev")
	t.Setenv("GOSIDIAN_SELF_IMPROVE_EVERY_N_CALLS", "10")
	t.Setenv("GOSIDIAN_SELF_IMPROVE_COOLDOWN_MINUTES", "30")
	t.Setenv("GOSIDIAN_SELF_IMPROVE_MAX_NUDGES_PER_SESSION", "2")
	t.Setenv("GOSIDIAN_SELF_IMPROVE_NOTIFY_EMAIL", "ops@example.com")
	if err := cfg.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	si := cfg.SelfImprove
	if !si.Enabled || si.TargetProject != "insights-dev" || si.EveryNCalls != 10 ||
		si.CooldownMinutes != 30 || si.MaxNudgesPerSession != 2 || si.NotifyEmail != "ops@example.com" {
		t.Errorf("env not applied: %+v", si)
	}
}

func TestSelfImprove_ApplyEnv_Invalid(t *testing.T) {
	cfg := Default()
	t.Setenv("GOSIDIAN_SELF_IMPROVE_EVERY_N_CALLS", "not-a-number")
	if err := cfg.ApplyEnv(); err == nil {
		t.Error("invalid every_n_calls should error")
	}
}

func TestGlobal_Defaults(t *testing.T) {
	cfg := Default()
	if cfg.Global.Enabled {
		t.Error("global should default disabled")
	}
	if cfg.Global.PublicProject != "global" {
		t.Errorf("default public_project = %q", cfg.Global.PublicProject)
	}
	if cfg.Global.PrivateProject != "global-private" {
		t.Errorf("default private_project = %q", cfg.Global.PrivateProject)
	}
}

func TestGlobal_ApplyEnv(t *testing.T) {
	cfg := Default()
	t.Setenv("GOSIDIAN_GLOBAL_ENABLED", "true")
	t.Setenv("GOSIDIAN_GLOBAL_PUBLIC_PROJECT", "shared")
	t.Setenv("GOSIDIAN_GLOBAL_PRIVATE_PROJECT", "shared-priv")
	if err := cfg.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Global.Enabled || cfg.Global.PublicProject != "shared" || cfg.Global.PrivateProject != "shared-priv" {
		t.Errorf("env not applied: %+v", cfg.Global)
	}
}

// Automations are on unless the file or the environment turns them off.
func TestAutomations(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.toml"))
	if err != nil || !cfg.Automations.Enabled || cfg.Automations.Interval != 5*time.Minute || cfg.Automations.Timezone != "" {
		t.Fatalf("defaults = %+v, %v", cfg.Automations, err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[automations]\nenabled = false\ninterval = \"1m\"\ntimezone = \"Europe/Rome\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.Automations.Enabled || cfg.Automations.Interval != time.Minute || cfg.Automations.Timezone != "Europe/Rome" {
		t.Fatalf("file = %+v, %v", cfg.Automations, err)
	}
	t.Setenv("GOSIDIAN_AUTOMATIONS_ENABLED", "true")
	t.Setenv("GOSIDIAN_AUTOMATIONS_INTERVAL", "30s")
	if err := cfg.ApplyEnv(); err != nil || !cfg.Automations.Enabled || cfg.Automations.Interval != 30*time.Second {
		t.Errorf("env = %+v, %v", cfg.Automations, err)
	}
}

// The owner of the first start is created unless the file or the
// environment turns it off (IMP-044).
func TestAutoOwner(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.toml"))
	if err != nil || !cfg.Webauth.AutoOwner {
		t.Fatalf("default = %v, %v", cfg.Webauth.AutoOwner, err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[webauth]\nauto_owner = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || cfg.Webauth.AutoOwner {
		t.Fatalf("file = %v, %v", cfg.Webauth.AutoOwner, err)
	}
	t.Setenv("GOSIDIAN_AUTO_OWNER", "true")
	if err := cfg.ApplyEnv(); err != nil || !cfg.Webauth.AutoOwner {
		t.Errorf("env on = %v, %v", cfg.Webauth.AutoOwner, err)
	}
	t.Setenv("GOSIDIAN_AUTO_OWNER", "false")
	if err := cfg.ApplyEnv(); err != nil || cfg.Webauth.AutoOwner {
		t.Errorf("env off = %v, %v", cfg.Webauth.AutoOwner, err)
	}
}

// MCP wants a token unless the operator opts in to token-less access
// (IMP-146).
func TestMCPOpen(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || cfg.MCP.Open {
		t.Fatalf("default = %v, %v", cfg.MCP.Open, err)
	}
	t.Setenv("GOSIDIAN_MCP_OPEN", "true")
	if err := cfg.ApplyEnv(); err != nil || !cfg.MCP.Open {
		t.Errorf("env = %v, %v", cfg.MCP.Open, err)
	}
}

// The upload quota is off unless set, in the file or the environment
// (IMP-034).
func TestUploadsQuota(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.toml"))
	if err != nil || cfg.Uploads.QuotaBytes != 0 {
		t.Fatalf("default = %+v, %v", cfg.Uploads, err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[uploads]\nquota_bytes = 1048576\nquota_window = \"6h\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || cfg.Uploads.QuotaBytes != 1<<20 || cfg.Uploads.QuotaWindow != 6*time.Hour {
		t.Fatalf("file = %+v, %v", cfg.Uploads, err)
	}
	t.Setenv("GOSIDIAN_UPLOAD_QUOTA_BYTES", "500")
	t.Setenv("GOSIDIAN_UPLOAD_QUOTA_WINDOW", "30m")
	if err := cfg.ApplyEnv(); err != nil || cfg.Uploads.QuotaBytes != 500 || cfg.Uploads.QuotaWindow != 30*time.Minute {
		t.Errorf("env = %+v, %v", cfg.Uploads, err)
	}
	t.Setenv("GOSIDIAN_UPLOAD_QUOTA_BYTES", "lots")
	if err := cfg.ApplyEnv(); err == nil {
		t.Error("a quota that is not a number was accepted")
	}
}

// The shrink guard is on by default; an explicit 0 in the file turns it
// off, and the environment overrides both (IMP-147).
func TestShrinkGuard(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.toml"))
	if err != nil || cfg.MCP.ShrinkGuardPercent != 10 || cfg.MCP.ShrinkGuardMinBytes != 1024 {
		t.Fatalf("default = %d %d, %v", cfg.MCP.ShrinkGuardPercent, cfg.MCP.ShrinkGuardMinBytes, err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[mcp]\nwrite_per_minute = 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || cfg.MCP.ShrinkGuardPercent != 10 || cfg.MCP.ShrinkGuardMinBytes != 1024 {
		t.Fatalf("a file without the keys = %d %d, %v", cfg.MCP.ShrinkGuardPercent, cfg.MCP.ShrinkGuardMinBytes, err)
	}
	if err := os.WriteFile(path, []byte("[mcp]\nshrink_guard_percent = 0\nshrink_guard_min_bytes = 4096\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || cfg.MCP.ShrinkGuardPercent != 0 || cfg.MCP.ShrinkGuardMinBytes != 4096 {
		t.Fatalf("file = %d %d, %v", cfg.MCP.ShrinkGuardPercent, cfg.MCP.ShrinkGuardMinBytes, err)
	}
	t.Setenv("GOSIDIAN_MCP_SHRINK_GUARD_PERCENT", "25")
	t.Setenv("GOSIDIAN_MCP_SHRINK_GUARD_MIN_BYTES", "512")
	if err := cfg.ApplyEnv(); err != nil || cfg.MCP.ShrinkGuardPercent != 25 || cfg.MCP.ShrinkGuardMinBytes != 512 {
		t.Errorf("env = %d %d, %v", cfg.MCP.ShrinkGuardPercent, cfg.MCP.ShrinkGuardMinBytes, err)
	}
	t.Setenv("GOSIDIAN_MCP_SHRINK_GUARD_PERCENT", "ten")
	if err := cfg.ApplyEnv(); err == nil {
		t.Error("a percent that is not a number was accepted")
	}
}

// A retention of 0 keeps the trash for ever: saved, it stays 0 at the next
// start instead of becoming the 30-day default (BUG-105).
func TestTrashRetention(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "nope.toml"))
	if err != nil || cfg.Trash.Retention != 30*24*time.Hour {
		t.Fatalf("default = %v, %v", cfg.Trash.Retention, err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[trash]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || cfg.Trash.Retention != 30*24*time.Hour {
		t.Fatalf("a file without the key = %v, %v", cfg.Trash.Retention, err)
	}
	cfg.Trash.Retention = 0
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || cfg.Trash.Retention != 0 {
		t.Errorf("a saved 0 reloads as %v, %v", cfg.Trash.Retention, err)
	}
}

// A TOTP mode is read whatever its case, "require" included; a value still
// unknown closes, as required, and says so: it opened, as off (BUG-115,
// S5-11).
func TestTOTPModeOf(t *testing.T) {
	for raw, want := range map[string]string{"": "off", "off": "off", "Optional": "optional", " required ": "required", "Require": "required", "requird": "required", "yes": "required"} {
		got, warn := TOTPModeOf(raw)
		if got != want {
			t.Errorf("TOTPModeOf(%q) = %q, want %q", raw, got, want)
		}
		if unknown := raw == "requird" || raw == "yes"; unknown != (warn != "") {
			t.Errorf("TOTPModeOf(%q) warning = %q", raw, warn)
		}
	}
	t.Setenv("GOSIDIAN_TOTP_MODE", "Required")
	cfg := Default()
	if err := cfg.ApplyEnv(); err != nil || cfg.Webauth.TOTPMode != "required" || cfg.TOTPModeWarning() != "" {
		t.Errorf("env Required = %q (%q), %v", cfg.Webauth.TOTPMode, cfg.TOTPModeWarning(), err)
	}
}

// A save keeps the file's permissions, a new file is 0600, and no
// temporary file is left (BUG-115, S5-14).
func TestSave_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("a new config.toml is %v, want 0600", st.Mode().Perm())
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Default()); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("after a save the file is %v, want the 0640 it had", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".config-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// cache_size = 0 turns the vault cache off, from the file and from the
// environment; unset, it is 128 (IMP-163).
func TestCacheSize_ZeroDisables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[vault]\ncache_size = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Vault.CacheSize != 0 {
		t.Errorf("cache_size = 0 in the file: %d, %v", cfg.Vault.CacheSize, err)
	}
	if err := os.WriteFile(path, []byte("[vault]\nhtml_notes = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(path); cfg.Vault.CacheSize != 128 {
		t.Errorf("cache_size unset: %d, want 128", cfg.Vault.CacheSize)
	}
	t.Setenv("GOSIDIAN_VAULT_CACHE_SIZE", "0")
	cfg = Default()
	if err := cfg.ApplyEnv(); err != nil || cfg.Vault.CacheSize != 0 {
		t.Errorf("GOSIDIAN_VAULT_CACHE_SIZE=0: %d, %v", cfg.Vault.CacheSize, err)
	}
}
