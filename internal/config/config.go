// Package config loads the TOML configuration file stored at
// <vault>/.gosidian/config.toml. Absent or empty file means "defaults"; no
// feature requires the file to exist.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level settings document.
type Config struct {
	Git     GitConfig     `toml:"git"`
	MCP     MCPConfig     `toml:"mcp"`
	Trash   TrashConfig   `toml:"trash"`
	Theme   ThemeConfig   `toml:"theme"`
	Webauth WebauthConfig `toml:"webauth"`
	Vault   VaultConfig   `toml:"vault"`
	I18n    I18nConfig    `toml:"i18n"`
}

// WebauthConfig tunes the web login behaviour. All fields have sane defaults
// and may be left at zero; env vars GOSIDIAN_LOGIN_* override.
type WebauthConfig struct {
	SessionTTL       time.Duration `toml:"session_ttl"`        // default 24h
	LoginWindow      time.Duration `toml:"login_window"`       // default 15m
	LoginMaxFailures int           `toml:"login_max_failures"` // default 5
}

// VaultConfig tunes the vault read cache.
type VaultConfig struct {
	CacheSize int `toml:"cache_size"` // LRU entries; 0 disables; default 128
}

// I18nConfig chooses the default UI language and the list of enabled ones.
// File-based catalogues ship embedded and are merged at startup.
type I18nConfig struct {
	DefaultLang  string   `toml:"default_lang"`  // default "en"
	EnabledLangs []string `toml:"enabled_langs"` // default ["it", "en"]
}

// ThemeConfig holds the 5 root colors of the active palette. Other design
// tokens in app.css are derived from these and do not need to be configured.
// Values are hex strings in "#RRGGBB" form, validated by ValidHexColor.
//
// Preset selects a named palette: when set to a known preset (see
// ThemePresets), EffectiveTheme overrides the 5 individual colors with the
// preset's values. When "custom" (or unknown), the 5 fields are used
// as-is — enabling the color picker to drive arbitrary palettes.
type ThemeConfig struct {
	Preset       string `toml:"preset"`        // "midnight-luxury" | "light-clean" | "high-contrast" | "custom"
	DeepSpace    string `toml:"deep_space"`    // --bg-base
	Gunmetal     string `toml:"gunmetal"`      // --bg-elev-1
	SilverMist   string `toml:"silver_mist"`   // --text-secondary
	ElectricBlue string `toml:"electric_blue"` // --accent-cool
	GoldLeaf     string `toml:"gold_leaf"`     // --accent-gold
}

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// ValidHexColor reports whether s is a valid "#RRGGBB" hex color.
func ValidHexColor(s string) bool {
	return hexColorRe.MatchString(s)
}

// TrashConfig opts the trash bin in. When enabled, deleting a note or a
// project moves the contents into <vault>/.gosidian/trash/<timestamp>/...
// instead of removing them from disk. RetentionDays prunes entries older
// than the cutoff at server startup. Zero retention disables auto-pruning.
type TrashConfig struct {
	Enabled       bool          `toml:"enabled"`
	Retention     time.Duration `toml:"retention"`
}

// MCPConfig caps how aggressively an MCP client may mutate the vault.
// Both fields are per-token. Zero values mean "use defaults".
type MCPConfig struct {
	WritePerMinute     int      `toml:"write_per_minute"`      // default 60
	MaxNoteBytes       int64    `toml:"max_note_bytes"`        // default 1 MiB
	AllowedUploadRoots []string `toml:"allowed_upload_roots"`  // fs roots for source_path uploads
}

// GitConfig controls the auto-sync of the vault to a git remote.
type GitConfig struct {
	Enabled     bool          `toml:"enabled"`
	Remote      string        `toml:"remote"`         // optional: only used to git init a fresh repo
	Branch      string        `toml:"branch"`         // default "main"
	AuthorName  string        `toml:"author_name"`    // default "Gosidian"
	AuthorEmail string        `toml:"author_email"`   // default "gosidian@localhost"
	Debounce    time.Duration `toml:"commit_debounce"` // default 30s
	Push        bool          `toml:"push"`           // default false: commit locally only unless enabled
	TokenEnv    string        `toml:"token_env"`      // env var name for push credentials
}

// Load reads the config file from path. Missing files return defaults with no
// error. Parse errors are returned.
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return cfg, nil
	}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

// Default returns a Config with sensible defaults (git sync disabled).
func Default() *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	return cfg
}

// Save serializes the config to path as TOML. Parent directories are created
// if missing. Writes atomically via a temp file + rename.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ApplyEnv overrides fields whose matching GOSIDIAN_* environment variable is
// set. It's meant to be called after Load so that env vars override the file
// contents (but not CLI flags, which are applied separately in main). Empty
// env vars are ignored — they do not reset a field to zero.
func (c *Config) ApplyEnv() error {
	if v := os.Getenv("GOSIDIAN_GIT_ENABLED"); v != "" {
		c.Git.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_GIT_REMOTE"); v != "" {
		c.Git.Remote = v
	}
	if v := os.Getenv("GOSIDIAN_GIT_BRANCH"); v != "" {
		c.Git.Branch = v
	}
	if v := os.Getenv("GOSIDIAN_GIT_AUTHOR_NAME"); v != "" {
		c.Git.AuthorName = v
	}
	if v := os.Getenv("GOSIDIAN_GIT_AUTHOR_EMAIL"); v != "" {
		c.Git.AuthorEmail = v
	}
	if v := os.Getenv("GOSIDIAN_GIT_DEBOUNCE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		c.Git.Debounce = d
	}
	if v := os.Getenv("GOSIDIAN_GIT_PUSH"); v != "" {
		c.Git.Push = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_GIT_TOKEN_ENV"); v != "" {
		c.Git.TokenEnv = v
	}
	if v := os.Getenv("GOSIDIAN_MCP_WRITE_PER_MINUTE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_MCP_WRITE_PER_MINUTE: %w", err)
		}
		c.MCP.WritePerMinute = n
	}
	if v := os.Getenv("GOSIDIAN_MCP_MAX_NOTE_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_MCP_MAX_NOTE_BYTES: %w", err)
		}
		c.MCP.MaxNoteBytes = n
	}
	if v := os.Getenv("GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS"); v != "" {
		var roots []string
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, r)
			}
		}
		c.MCP.AllowedUploadRoots = roots
	}
	if v := os.Getenv("GOSIDIAN_TRASH_ENABLED"); v != "" {
		c.Trash.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_TRASH_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_TRASH_RETENTION: %w", err)
		}
		c.Trash.Retention = d
	}
	for envVar, dst := range map[string]*string{
		"GOSIDIAN_THEME_PRESET":        &c.Theme.Preset,
		"GOSIDIAN_THEME_DEEP_SPACE":    &c.Theme.DeepSpace,
		"GOSIDIAN_THEME_GUNMETAL":      &c.Theme.Gunmetal,
		"GOSIDIAN_THEME_SILVER_MIST":   &c.Theme.SilverMist,
		"GOSIDIAN_THEME_ELECTRIC_BLUE": &c.Theme.ElectricBlue,
		"GOSIDIAN_THEME_GOLD_LEAF":     &c.Theme.GoldLeaf,
	} {
		if v := os.Getenv(envVar); v != "" {
			if !ValidHexColor(v) {
				return fmt.Errorf("%s: expected #RRGGBB, got %q", envVar, v)
			}
			*dst = v
		}
	}
	if v := os.Getenv("GOSIDIAN_LOGIN_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_LOGIN_SESSION_TTL: %w", err)
		}
		c.Webauth.SessionTTL = d
	}
	if v := os.Getenv("GOSIDIAN_LOGIN_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_LOGIN_WINDOW: %w", err)
		}
		c.Webauth.LoginWindow = d
	}
	if v := os.Getenv("GOSIDIAN_LOGIN_MAX_FAILURES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_LOGIN_MAX_FAILURES: %w", err)
		}
		c.Webauth.LoginMaxFailures = n
	}
	if v := os.Getenv("GOSIDIAN_VAULT_CACHE_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_VAULT_CACHE_SIZE: %w", err)
		}
		c.Vault.CacheSize = n
	}
	if v := os.Getenv("GOSIDIAN_I18N_DEFAULT_LANG"); v != "" {
		c.I18n.DefaultLang = v
	}
	return nil
}

func envBool(v string) bool {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}

func (c *Config) applyDefaults() {
	if c.Git.Branch == "" {
		c.Git.Branch = "main"
	}
	if c.Git.AuthorName == "" {
		c.Git.AuthorName = "Gosidian"
	}
	if c.Git.AuthorEmail == "" {
		c.Git.AuthorEmail = "gosidian@localhost"
	}
	if c.Git.Debounce == 0 {
		c.Git.Debounce = 30 * time.Second
	}
	if c.MCP.WritePerMinute == 0 {
		c.MCP.WritePerMinute = 60
	}
	if c.MCP.MaxNoteBytes == 0 {
		c.MCP.MaxNoteBytes = 1 << 20 // 1 MiB
	}
	if c.Trash.Retention == 0 {
		c.Trash.Retention = 30 * 24 * time.Hour
	}
	if c.Theme.Preset == "" {
		c.Theme.Preset = DefaultThemePreset
	}
	if c.Theme.DeepSpace == "" {
		c.Theme.DeepSpace = "#0B0C10"
	}
	if c.Theme.Gunmetal == "" {
		c.Theme.Gunmetal = "#1F2833"
	}
	if c.Theme.SilverMist == "" {
		c.Theme.SilverMist = "#C5C6C7"
	}
	if c.Theme.ElectricBlue == "" {
		c.Theme.ElectricBlue = "#66FCF1"
	}
	if c.Theme.GoldLeaf == "" {
		c.Theme.GoldLeaf = "#C5A021"
	}
	if c.Webauth.SessionTTL == 0 {
		c.Webauth.SessionTTL = 24 * time.Hour
	}
	if c.Webauth.LoginWindow == 0 {
		c.Webauth.LoginWindow = 15 * time.Minute
	}
	if c.Webauth.LoginMaxFailures == 0 {
		c.Webauth.LoginMaxFailures = 5
	}
	if c.Vault.CacheSize == 0 {
		c.Vault.CacheSize = 128
	}
	if c.I18n.DefaultLang == "" {
		c.I18n.DefaultLang = "en"
	}
	if len(c.I18n.EnabledLangs) == 0 {
		c.I18n.EnabledLangs = []string{"it", "en", "es", "fr", "de"}
	}
}
