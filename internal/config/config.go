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
	Git          GitConfig          `toml:"git"`
	MCP          MCPConfig          `toml:"mcp"`
	Trash        TrashConfig        `toml:"trash"`
	Theme        ThemeConfig        `toml:"theme"`
	Webauth      WebauthConfig      `toml:"webauth"`
	Vault        VaultConfig        `toml:"vault"`
	I18n         I18nConfig         `toml:"i18n"`
	Lint         LintConfig         `toml:"lint"`
	LDAP         LDAPConfig         `toml:"ldap"`
	SelfImprove  SelfImproveConfig  `toml:"self_improve"`
	Global       GlobalConfig       `toml:"global"`
	AgentAnchors AgentAnchorsConfig `toml:"agent_anchors"`
}

// SelfImproveConfig enables the agent-sourced self-improvement loop: opted-in
// MCP tokens get a periodic nudge inviting the agent to record a structured
// insight (via memory_self_improve) about real-usage friction. Disabled by
// default — with enabled=false gosidian behaves exactly as before. Opt-in is
// per-token (auth.Token.SelfImproveOptIn), NOT global: this section holds only
// the master switch and global tuning. See plan 20260608-self-improve-feedback-loop.
type SelfImproveConfig struct {
	Enabled             bool          `toml:"enabled"`                // master switch; default false
	TargetProject       string        `toml:"target_project"`         // vault project for raw insights; default "insights"
	EveryNCalls         int           `toml:"every_n_calls"`          // nudge cadence per session; default 25
	CooldownMinutes     int           `toml:"cooldown_minutes"`       // min minutes between nudges per token; default 120
	MaxNudgesPerSession int           `toml:"max_nudges_per_session"` // hard cap per session; default 1
	NotifyEmail         string        `toml:"notify_email"`           // scheduled-digest recipient; empty = no email
	DigestInterval      time.Duration `toml:"digest_interval"`        // how often to compile a digest; 0 disables the scheduler
	SMTPHost            string        `toml:"smtp_host"`              // SMTP server host; empty = email off (digest note still written)
	SMTPPort            int           `toml:"smtp_port"`              // default 587
	SMTPFrom            string        `toml:"smtp_from"`              // From address
	SMTPUsername        string        `toml:"smtp_username"`          // SMTP auth username
	SMTPPassword        string        `toml:"smtp_password"`          // SMTP auth password (prefer env GOSIDIAN_SELF_IMPROVE_SMTP_PASSWORD)
}

// GlobalConfig enables the shared "global" projects that hold reusable skills,
// agents and init templates other projects can reference (per-project opt-in
// via projects.Flags.UseGlobals). Two projects: PublicProject (RBAC public,
// shared with everyone) and PrivateProject (private, owner-only). Disabled by
// default. See plan 20260608-global-project-shared-skills.
type GlobalConfig struct {
	Enabled        bool   `toml:"enabled"`         // master switch; default false
	PublicProject  string `toml:"public_project"`  // default "global"
	PrivateProject string `toml:"private_project"` // default "global-private"
}

// AgentAnchorsConfig is the master switch for local agent-anchor
// materialisation: when enabled, projects that opted in
// (projects.Flags.UseAnchors) get their vault agents surfaced at bootstrap as
// anchor files to write in the agent's cwd, for CLI profiles with native
// subagents. Disabled by default — with enabled=false gosidian behaves exactly
// as before. See plan 20260630-agent-anchors.
type AgentAnchorsConfig struct {
	Enabled bool `toml:"enabled"` // master switch; default false
}

// LDAPConfig enables web-login against an external directory via a
// search-then-bind flow. Disabled by default. bind_password is a secret —
// prefer GOSIDIAN_LDAP_BIND_PASSWORD over storing it in the file. It is never
// surfaced through /api/v1/settings.
type LDAPConfig struct {
	Enabled      bool   `toml:"enabled"`
	URL          string `toml:"url"`                  // ldap://host:389 | ldaps://host:636
	StartTLS     bool   `toml:"start_tls"`            // upgrade a plaintext ldap:// to TLS
	SkipVerify   bool   `toml:"insecure_skip_verify"` // dev only; do not use in prod
	BindDN       string `toml:"bind_dn"`              // service account DN used to search
	BindPassword string `toml:"bind_password"`        // service account password (prefer env)
	UserBaseDN   string `toml:"user_base_dn"`         // search base for user entries
	UserFilter   string `toml:"user_filter"`          // one %s for username; OpenLDAP "(uid=%s)", AD "(sAMAccountName=%s)"
}

// LintConfig tunes the structural health checks. All fields default to the
// built-in behaviour — gosidian works the same with no [lint] section in
// .gosidian/config.toml.
type LintConfig struct {
	FrontmatterTagVocabulary FrontmatterTagVocabulary `toml:"frontmatter_tag_vocabulary"`
	// HotOversizeBytes overrides the hot-oversize rule threshold (bytes).
	// 0 or absent keeps the built-in default (16 KiB).
	HotOversizeBytes int64 `toml:"hot_oversize_bytes"`
}

// FrontmatterTagVocabulary lets a vault add tags to the closed vocabulary
// the frontmatter-tag-unknown rule checks against, instance-wide (every
// project). Built-in namespaces (type/topic/status, plus the bare "pinned"
// tag and the project name) are always allowed; ExtraAllowed is purely
// additive — a vault never weakens its own discipline by setting this.
// For a single project's domain taxonomy prefer the per-project vocabulary
// instead: use_tag_vocabulary flag + `tag_vocabulary:` in that project's
// memory/conventions.md frontmatter (IMP-075).
//
// Format of each entry: "<namespace>:<value>" (e.g. "status:reference"),
// the bare tag name, or a namespace wildcard "<namespace>:*". Malformed
// entries are skipped silently at load time.
type FrontmatterTagVocabulary struct {
	ExtraAllowed []string `toml:"extra_allowed"`
}

// WebauthConfig tunes the web login behaviour. All fields have sane defaults
// and may be left at zero; env vars GOSIDIAN_LOGIN_* override.
type WebauthConfig struct {
	SessionTTL       time.Duration `toml:"session_ttl"`        // default 24h
	LoginWindow      time.Duration `toml:"login_window"`       // default 15m
	LoginMaxFailures int           `toml:"login_max_failures"` // default 5
	// TOTPMode is the global two-factor policy: "off" (default; no TOTP, the
	// login field is hidden), "optional" (users may enrol; enforced for those
	// who have a secret), or "required" (every non-exempt user must enrol).
	// Per-user overrides live on the webauth account (TOTPPolicy).
	TOTPMode string `toml:"totp_mode"`
	// OpenMode controls anonymous (token-less) web access. "off" (default)
	// requires a Bearer token for every data route. "readonly" maps token-less
	// requests onto the guest role: read-only, public projects only (the
	// existing RBAC governs the rest). Opt-in and read-only by design — anyone
	// who can reach the server can read public projects, so enable it knowingly
	// (e.g. a public showcase). Does not affect MCP (token-only).
	OpenMode string `toml:"open_mode"`
}

// VaultConfig tunes the vault read cache.
type VaultConfig struct {
	CacheSize  int  `toml:"cache_size"`  // LRU entries; 0 disables; default 128
	HTMLNotes  bool `toml:"html_notes"`  // treat single-file .html as first-class notes; default false (ADR-011)
	MediaNotes bool `toml:"media_notes"` // resolve image media notes (type: image + media: pointer); default false (ADR-013)
	TableNotes bool `toml:"table_notes"` // resolve CSV table notes (type: table + media: pointer); default false (ADR-016)
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
	Enabled   bool          `toml:"enabled"`
	Retention time.Duration `toml:"retention"`
}

// MCPConfig caps how aggressively an MCP client may mutate the vault.
// Both fields are per-token. Zero values mean "use defaults".
type MCPConfig struct {
	WritePerMinute     int      `toml:"write_per_minute"`     // default 60
	MaxNoteBytes       int64    `toml:"max_note_bytes"`       // default 1 MiB
	AllowedUploadRoots []string `toml:"allowed_upload_roots"` // fs roots for source_path uploads
	BridgeDir          string   `toml:"bridge_dir"`           // staging dir for bridge_filename uploads (auto-allowed root; IMP-059)
	IngestURLAllowlist []string `toml:"ingest_url_allowlist"` // URL prefixes memory_ingest may fetch from; empty disables the url source (ADR-018)
}

// GitConfig controls the auto-sync of the vault to a git remote.
type GitConfig struct {
	Enabled     bool          `toml:"enabled"`
	Remote      string        `toml:"remote"`          // optional: only used to git init a fresh repo
	Branch      string        `toml:"branch"`          // default "main"
	AuthorName  string        `toml:"author_name"`     // default "Gosidian"
	AuthorEmail string        `toml:"author_email"`    // default "gosidian@localhost"
	Debounce    time.Duration `toml:"commit_debounce"` // default 30s
	Push        bool          `toml:"push"`            // default false: commit locally only unless enabled
	TokenEnv    string        `toml:"token_env"`       // env var name for push credentials
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
	if v := os.Getenv("GOSIDIAN_MCP_BRIDGE_DIR"); v != "" {
		c.MCP.BridgeDir = strings.TrimSpace(v)
	}
	if v := os.Getenv("GOSIDIAN_INGEST_URL_ALLOWLIST"); v != "" {
		var prefixes []string
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				prefixes = append(prefixes, p)
			}
		}
		c.MCP.IngestURLAllowlist = prefixes
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
	if v := os.Getenv("GOSIDIAN_TOTP_MODE"); v != "" {
		c.Webauth.TOTPMode = v
	}
	if v := os.Getenv("GOSIDIAN_OPEN_MODE"); v != "" {
		switch v {
		case "off", "readonly":
			c.Webauth.OpenMode = v
		default:
			return fmt.Errorf("GOSIDIAN_OPEN_MODE: %q not supported (use \"off\" or \"readonly\")", v)
		}
	}
	if v := os.Getenv("GOSIDIAN_VAULT_CACHE_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_VAULT_CACHE_SIZE: %w", err)
		}
		c.Vault.CacheSize = n
	}
	if v := os.Getenv("GOSIDIAN_VAULT_HTML_NOTES"); v != "" {
		c.Vault.HTMLNotes = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_VAULT_MEDIA_NOTES"); v != "" {
		c.Vault.MediaNotes = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_VAULT_TABLE_NOTES"); v != "" {
		c.Vault.TableNotes = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_I18N_DEFAULT_LANG"); v != "" {
		c.I18n.DefaultLang = v
	}
	if v := os.Getenv("GOSIDIAN_LDAP_ENABLED"); v != "" {
		c.LDAP.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_LDAP_URL"); v != "" {
		c.LDAP.URL = v
	}
	if v := os.Getenv("GOSIDIAN_LDAP_BIND_DN"); v != "" {
		c.LDAP.BindDN = v
	}
	if v := os.Getenv("GOSIDIAN_LDAP_BIND_PASSWORD"); v != "" {
		c.LDAP.BindPassword = v
	}
	if v := os.Getenv("GOSIDIAN_LDAP_USER_BASE_DN"); v != "" {
		c.LDAP.UserBaseDN = v
	}
	if v := os.Getenv("GOSIDIAN_LDAP_USER_FILTER"); v != "" {
		c.LDAP.UserFilter = v
	}
	if v := os.Getenv("GOSIDIAN_LDAP_START_TLS"); v != "" {
		c.LDAP.StartTLS = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_LDAP_INSECURE_SKIP_VERIFY"); v != "" {
		c.LDAP.SkipVerify = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_ENABLED"); v != "" {
		c.SelfImprove.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_TARGET_PROJECT"); v != "" {
		c.SelfImprove.TargetProject = v
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_EVERY_N_CALLS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_SELF_IMPROVE_EVERY_N_CALLS: %w", err)
		}
		c.SelfImprove.EveryNCalls = n
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_COOLDOWN_MINUTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_SELF_IMPROVE_COOLDOWN_MINUTES: %w", err)
		}
		c.SelfImprove.CooldownMinutes = n
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_MAX_NUDGES_PER_SESSION"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_SELF_IMPROVE_MAX_NUDGES_PER_SESSION: %w", err)
		}
		c.SelfImprove.MaxNudgesPerSession = n
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_NOTIFY_EMAIL"); v != "" {
		c.SelfImprove.NotifyEmail = v
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_DIGEST_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_SELF_IMPROVE_DIGEST_INTERVAL: %w", err)
		}
		c.SelfImprove.DigestInterval = d
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_SMTP_HOST"); v != "" {
		c.SelfImprove.SMTPHost = v
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_SELF_IMPROVE_SMTP_PORT: %w", err)
		}
		c.SelfImprove.SMTPPort = n
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_SMTP_FROM"); v != "" {
		c.SelfImprove.SMTPFrom = v
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_SMTP_USERNAME"); v != "" {
		c.SelfImprove.SMTPUsername = v
	}
	if v := os.Getenv("GOSIDIAN_SELF_IMPROVE_SMTP_PASSWORD"); v != "" {
		c.SelfImprove.SMTPPassword = v
	}
	if v := os.Getenv("GOSIDIAN_GLOBAL_ENABLED"); v != "" {
		c.Global.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_GLOBAL_PUBLIC_PROJECT"); v != "" {
		c.Global.PublicProject = v
	}
	if v := os.Getenv("GOSIDIAN_GLOBAL_PRIVATE_PROJECT"); v != "" {
		c.Global.PrivateProject = v
	}
	if v := os.Getenv("GOSIDIAN_ANCHORS_ENABLED"); v != "" {
		c.AgentAnchors.Enabled = envBool(v)
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
	if c.Webauth.TOTPMode != "optional" && c.Webauth.TOTPMode != "required" {
		c.Webauth.TOTPMode = "off"
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
	if c.LDAP.UserFilter == "" {
		c.LDAP.UserFilter = "(uid=%s)"
	}
	if c.SelfImprove.TargetProject == "" {
		c.SelfImprove.TargetProject = "insights"
	}
	if c.SelfImprove.EveryNCalls == 0 {
		c.SelfImprove.EveryNCalls = 25
	}
	if c.SelfImprove.CooldownMinutes == 0 {
		c.SelfImprove.CooldownMinutes = 120
	}
	if c.SelfImprove.MaxNudgesPerSession == 0 {
		c.SelfImprove.MaxNudgesPerSession = 1
	}
	if c.SelfImprove.SMTPPort == 0 {
		c.SelfImprove.SMTPPort = 587
	}
	if c.Global.PublicProject == "" {
		c.Global.PublicProject = "global"
	}
	if c.Global.PrivateProject == "" {
		c.Global.PrivateProject = "global-private"
	}
}
