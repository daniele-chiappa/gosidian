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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gosidian/gosidian/internal/i18n"
)

// Config is the top-level settings document.
type Config struct {
	Git          GitConfig          `toml:"git"`
	MCP          MCPConfig          `toml:"mcp"`
	Trash        TrashConfig        `toml:"trash"`
	Automations  AutomationsConfig  `toml:"automations"`
	Webauth      WebauthConfig      `toml:"webauth"`
	Vault        VaultConfig        `toml:"vault"`
	I18n         I18nConfig         `toml:"i18n"`
	Lint         LintConfig         `toml:"lint"`
	LDAP         LDAPConfig         `toml:"ldap"`
	SelfImprove  SelfImproveConfig  `toml:"self_improve"`
	Global       GlobalConfig       `toml:"global"`
	AgentAnchors AgentAnchorsConfig `toml:"agent_anchors"`
	OAuth        OAuthConfig        `toml:"oauth"`
	Uploads      UploadsConfig      `toml:"uploads"`

	// totpWarning says why the configured TOTP mode was read otherwise
	// (TOTPModeOf); "" when it was read as written.
	totpWarning string
}

// TOTPModeWarning says why the configured TOTP mode was read otherwise than
// written, for the startup log; "" when it was not.
func (c *Config) TOTPModeWarning() string { return c.totpWarning }

// TOTPModeOf reads a configured two-factor policy: case and spaces aside,
// "require" for "required", and empty for "off". A value still unknown
// reads "required", with the reason: "Required" or a typo turned two-factor
// off without a word (BUG-115, S5-11); closed, every account without a
// secret enrols at its next sign-in, and the log says why.
func TOTPModeOf(raw string) (mode, warning string) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "off":
		return "off", ""
	case "optional", "required":
		return m, ""
	case "require":
		return "required", ""
	}
	return "required", fmt.Sprintf("totp_mode %q is not off, optional or required: read as required", raw)
}

// UploadsConfig caps what an account uploads (IMP-034).
type UploadsConfig struct {
	// QuotaBytes is how many bytes of attachments an account may upload in
	// QuotaWindow, its MCP tokens and its web sessions together. 0 (the
	// default) means no limit. Counted in memory: a restart starts again.
	QuotaBytes int64 `toml:"quota_bytes"`
	// QuotaWindow is the sliding window of the quota; default 24h.
	QuotaWindow time.Duration `toml:"quota_window"`
}

// OAuthConfig enables the embedded OAuth 2.1 authorization server that lets
// MCP clients (claude.ai, ChatGPT, Claude Code…) obtain gosidian credentials
// through a browser consent instead of a pasted bearer token (IMP-092).
// Off by default. Issuer is the public origin clients reach gosidian at; it
// must be served over HTTPS for the hosted clients to accept it.
type OAuthConfig struct {
	Enabled bool   `toml:"enabled"`
	Issuer  string `toml:"issuer"` // e.g. https://notes.example.com; required when enabled
	// AccessTTL bounds the in-memory access tokens (default 1h); RefreshTTL
	// is the grant lifetime, renewed by refresh-token rotation (default 720h).
	AccessTTL  time.Duration `toml:"access_ttl"`
	RefreshTTL time.Duration `toml:"refresh_ttl"`
	// ClientMax caps dynamically registered clients (default 1000);
	// ClientIdleTTL lets unused ones be evicted at the cap (default 2160h).
	ClientMax     int           `toml:"client_max"`
	ClientIdleTTL time.Duration `toml:"client_idle_ttl"`
	// AllowedRedirectHosts restricts non-loopback redirect URIs to these
	// hosts; empty allows any HTTPS host (claude.ai, chatgpt.com, …).
	AllowedRedirectHosts []string `toml:"allowed_redirect_hosts"`
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
	// 0 or absent keeps the built-in default (8 KiB).
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
	// TrustedProxies lists the reverse proxies (IPs or CIDRs) whose
	// X-Forwarded-For header the login rate limiter may believe. Empty
	// (default) means the header is ignored and the peer address is used:
	// honouring XFF from an untrusted peer lets a brute-forcer choose its
	// own rate-limit bucket on every attempt.
	TrustedProxies []string `toml:"trusted_proxies"`
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
	// AutoOwner creates the owner "admin" at the first start, when there is
	// no account at all, with a random password written to
	// <state-dir>/initial-admin-password and to the log, to be changed at
	// the first sign-in (IMP-044). Default true; off, the owner comes from
	// `gosidian user setup`.
	AutoOwner bool `toml:"auto_owner"`
}

// VaultConfig tunes the vault read cache.
type VaultConfig struct {
	CacheSize  int  `toml:"cache_size"`  // LRU entries; 0 disables; default 128
	HTMLNotes  bool `toml:"html_notes"`  // treat single-file .html as first-class notes; default false (ADR-011)
	MediaNotes bool `toml:"media_notes"` // resolve image media notes (type: image + media: pointer); default false (ADR-013)
	TableNotes bool `toml:"table_notes"` // resolve CSV table notes (type: table + media: pointer); default false (ADR-016)
}

// I18nConfig chooses the web UI's default language and the languages its
// selector offers. The catalogues ship embedded (internal/i18n); a language
// is available when it has a `ui` catalogue (i18n.Languages).
type I18nConfig struct {
	DefaultLang  string   `toml:"default_lang"`  // default "en"
	EnabledLangs []string `toml:"enabled_langs"` // default: every available language
}

// Validate checks the pair as a settings save must: every enabled code and
// the default have a catalogue, the list is not empty, and the default is
// one of the enabled languages.
func (c I18nConfig) Validate() error {
	langs := NormalizeLangs(c.EnabledLangs)
	if len(langs) == 0 {
		return errors.New("i18n.enabled_langs cannot be empty")
	}
	available := strings.Join(i18n.Languages(), ", ")
	for _, l := range langs {
		if !i18n.Supported(l) {
			return fmt.Errorf("i18n.enabled_langs: no catalogue for %q (available: %s)", l, available)
		}
	}
	d := strings.ToLower(strings.TrimSpace(c.DefaultLang))
	if !i18n.Supported(d) {
		return fmt.Errorf("i18n.default_lang: no catalogue for %q (available: %s)", d, available)
	}
	if !slices.Contains(langs, d) {
		return fmt.Errorf("i18n.default_lang %q is not among the enabled languages", d)
	}
	return nil
}

// Effective returns the values the server runs with, and a warning for each
// one it had to correct: a wrong config.toml or environment never stops the
// server. Codes are trimmed, lowercased and deduplicated; a code without a
// catalogue is dropped, and an empty list means every language. A default
// without a catalogue becomes "en"; a default left out of the list is added
// to it, since the web UI starts in that language.
func (c I18nConfig) Effective() (I18nConfig, []string) {
	var out I18nConfig
	var warns []string
	for _, l := range NormalizeLangs(c.EnabledLangs) {
		if !i18n.Supported(l) {
			warns = append(warns, fmt.Sprintf("i18n.enabled_langs: no catalogue for %q, ignored", l))
			continue
		}
		out.EnabledLangs = append(out.EnabledLangs, l)
	}
	if len(out.EnabledLangs) == 0 {
		out.EnabledLangs = i18n.Languages()
	}
	out.DefaultLang = strings.ToLower(strings.TrimSpace(c.DefaultLang))
	if !i18n.Supported(out.DefaultLang) {
		if out.DefaultLang != "" {
			warns = append(warns, fmt.Sprintf("i18n.default_lang: no catalogue for %q, using en", out.DefaultLang))
		}
		out.DefaultLang = "en"
	}
	if !slices.Contains(out.EnabledLangs, out.DefaultLang) {
		warns = append(warns, fmt.Sprintf("i18n.default_lang %q is not among i18n.enabled_langs: enabled too", out.DefaultLang))
		out.EnabledLangs = append([]string{out.DefaultLang}, out.EnabledLangs...)
	}
	return out, warns
}

// NormalizeLangs trims and lowercases language codes, dropping empty ones
// and repeats; the order is kept.
func NormalizeLangs(langs []string) []string {
	var out []string
	for _, l := range langs {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// TrashConfig opts the trash bin in. When enabled, deleting a note or a
// project moves the contents into <vault>/.gosidian/trash/<timestamp>/...
// instead of removing them from disk. Retention prunes entries older than
// the cutoff at server startup: 30 days when unset, and zero keeps every
// entry until it is purged by hand.
type TrashConfig struct {
	Enabled   bool          `toml:"enabled"`
	Retention time.Duration `toml:"retention"`
}

// AutomationsConfig runs the rules database notes declare under
// `automations:` (IMP-127 iteration 3): handoffs when a row's date comes
// near, snapshots and handoffs at a time of the week or day. On by default:
// a rule acts only where a database note declares it.
type AutomationsConfig struct {
	Enabled bool `toml:"enabled"`
	// Interval is how often the rules are run; default 5m.
	Interval time.Duration `toml:"interval"`
	// Timezone is the IANA zone of the days and times of the rules
	// ("Europe/Rome"); default the server's local zone.
	Timezone string `toml:"timezone"`
}

// MCPConfig caps how aggressively an MCP client may mutate the vault.
// The write rate counts per MCP session, the note size per write. Zero
// values mean "use defaults".
type MCPConfig struct {
	WritePerMinute int   `toml:"write_per_minute"` // default 60, per MCP session; a token gets 5× (IMP-141)
	MaxNoteBytes   int64 `toml:"max_note_bytes"`   // default 1 MiB
	// PackageMaxFiles and PackageMaxBytes cap a memory_ingest package
	// (as: package, IMP-116): its files, and its size unpacked.
	PackageMaxFiles    int      `toml:"package_max_files"`    // default 500
	PackageMaxBytes    int64    `toml:"package_max_bytes"`    // default 20 MiB
	AllowedUploadRoots []string `toml:"allowed_upload_roots"` // fs roots for source_path uploads
	// Open lets MCP answer without a token, as admin, while the token store
	// holds none (IMP-146). Default false: every MCP request needs a token.
	// For local use only: whoever reaches the port gets full access.
	Open               bool     `toml:"open"`
	BridgeDir          string   `toml:"bridge_dir"`           // staging dir for bridge_filename uploads (auto-allowed root; IMP-059)
	IngestURLAllowlist []string `toml:"ingest_url_allowlist"` // URL prefixes memory_ingest may fetch from; empty disables the url source (ADR-018)
	// DisableDNSRebindingProtection turns off the MCP transports' built-in
	// guard (403 for a loopback-bound connection whose Host header is not a
	// localhost value). Off by default; only a same-host reverse proxy
	// forwarding over 127.0.0.1 with the public Host header needs it.
	DisableDNSRebindingProtection bool `toml:"disable_dns_rebinding_protection"`
	// ShrinkGuardPercent and ShrinkGuardMinBytes guard a note against a
	// rewrite that empties it (IMP-147): memory_update, or memory_ingest
	// with overwrite, refuses a content under ShrinkGuardPercent % of a note
	// of at least ShrinkGuardMinBytes, unless the call passes allow_shrink.
	// Default 10 and 1024, set in Default so that an explicit 0 in the file
	// stays 0: a percent of 0 turns the guard off.
	ShrinkGuardPercent  int   `toml:"shrink_guard_percent"`
	ShrinkGuardMinBytes int64 `toml:"shrink_guard_min_bytes"`
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
	// On unless the file or the environment turns them off: applyDefaults
	// runs after the file is read, so it cannot tell false from unset.
	cfg.Automations.Enabled = true
	cfg.Webauth.AutoOwner = true
	cfg.MCP.ShrinkGuardPercent = 10
	cfg.MCP.ShrinkGuardMinBytes = 1024
	// Here too, as 0 means "forever": applyDefaults turned a saved 0 into
	// 30 days at the next start, and the prune emptied the trash (BUG-105).
	cfg.Trash.Retention = 30 * 24 * time.Hour
	cfg.applyDefaults()
	return cfg
}

// Save serializes the config to path as TOML. Parent directories are created
// if missing. Writes atomically via a temp file + rename.
//
// The file keeps its permissions, and a new one is 0600: it may hold the
// LDAP bind password, and a file set to 0600 came back as 0644 at every
// save. Saves run one at a time, through a temporary file of their own,
// synced (BUG-115, S5-14). Comments and keys this version does not know are
// not kept.
func Save(path string, cfg *Config) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	perm := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(buf.Bytes())
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// saveMu serializes Save within the process.
var saveMu sync.Mutex

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
	if v := os.Getenv("GOSIDIAN_MCP_PACKAGE_MAX_FILES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_MCP_PACKAGE_MAX_FILES: %w", err)
		}
		c.MCP.PackageMaxFiles = n
	}
	if v := os.Getenv("GOSIDIAN_MCP_PACKAGE_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_MCP_PACKAGE_MAX_BYTES: %w", err)
		}
		c.MCP.PackageMaxBytes = n
	}
	if v := os.Getenv("GOSIDIAN_MCP_SHRINK_GUARD_PERCENT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_MCP_SHRINK_GUARD_PERCENT: %w", err)
		}
		c.MCP.ShrinkGuardPercent = n
	}
	if v := os.Getenv("GOSIDIAN_MCP_SHRINK_GUARD_MIN_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_MCP_SHRINK_GUARD_MIN_BYTES: %w", err)
		}
		c.MCP.ShrinkGuardMinBytes = n
	}
	if v := os.Getenv("GOSIDIAN_UPLOAD_QUOTA_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_UPLOAD_QUOTA_BYTES: %w", err)
		}
		c.Uploads.QuotaBytes = n
	}
	if v := os.Getenv("GOSIDIAN_UPLOAD_QUOTA_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_UPLOAD_QUOTA_WINDOW: %w", err)
		}
		c.Uploads.QuotaWindow = d
	}
	if v := os.Getenv("GOSIDIAN_MCP_OPEN"); v != "" {
		c.MCP.Open = envBool(v)
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
	if v := os.Getenv("GOSIDIAN_MCP_DISABLE_DNS_REBINDING_PROTECTION"); v != "" {
		c.MCP.DisableDNSRebindingProtection = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_OAUTH_ENABLED"); v != "" {
		c.OAuth.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_OAUTH_ISSUER"); v != "" {
		c.OAuth.Issuer = strings.TrimSpace(v)
	}
	if v := os.Getenv("GOSIDIAN_OAUTH_ACCESS_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_OAUTH_ACCESS_TTL: %w", err)
		}
		c.OAuth.AccessTTL = d
	}
	if v := os.Getenv("GOSIDIAN_OAUTH_REFRESH_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_OAUTH_REFRESH_TTL: %w", err)
		}
		c.OAuth.RefreshTTL = d
	}
	if v := os.Getenv("GOSIDIAN_OAUTH_ALLOWED_REDIRECT_HOSTS"); v != "" {
		var hosts []string
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		c.OAuth.AllowedRedirectHosts = hosts
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
	if v := os.Getenv("GOSIDIAN_AUTOMATIONS_ENABLED"); v != "" {
		c.Automations.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_AUTOMATIONS_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_AUTOMATIONS_INTERVAL: %w", err)
		}
		c.Automations.Interval = d
	}
	if v := os.Getenv("GOSIDIAN_AUTOMATIONS_TIMEZONE"); v != "" {
		c.Automations.Timezone = v
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
		c.Webauth.TOTPMode, c.totpWarning = TOTPModeOf(v)
	}
	if v := os.Getenv("GOSIDIAN_TRUSTED_PROXIES"); v != "" {
		var proxies []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				proxies = append(proxies, p)
			}
		}
		c.Webauth.TrustedProxies = proxies
	}
	if v := os.Getenv("GOSIDIAN_OPEN_MODE"); v != "" {
		switch v {
		case "off", "readonly":
			c.Webauth.OpenMode = v
		default:
			return fmt.Errorf("GOSIDIAN_OPEN_MODE: %q not supported (use \"off\" or \"readonly\")", v)
		}
	}
	if v := os.Getenv("GOSIDIAN_AUTO_OWNER"); v != "" {
		c.Webauth.AutoOwner = envBool(v)
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
	if v := os.Getenv("GOSIDIAN_I18N_ENABLED_LANGS"); v != "" {
		if langs := NormalizeLangs(strings.Split(v, ",")); len(langs) > 0 {
			c.I18n.EnabledLangs = langs
		}
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
	if c.MCP.PackageMaxFiles == 0 {
		c.MCP.PackageMaxFiles = 500
	}
	if c.MCP.PackageMaxBytes == 0 {
		c.MCP.PackageMaxBytes = 20 << 20 // 20 MiB
	}
	if c.MCP.MaxNoteBytes == 0 {
		c.MCP.MaxNoteBytes = 1 << 20 // 1 MiB
	}
	if c.OAuth.AccessTTL == 0 {
		c.OAuth.AccessTTL = time.Hour
	}
	if c.OAuth.RefreshTTL == 0 {
		c.OAuth.RefreshTTL = 30 * 24 * time.Hour
	}
	if c.OAuth.ClientMax == 0 {
		c.OAuth.ClientMax = 1000
	}
	if c.OAuth.ClientIdleTTL == 0 {
		c.OAuth.ClientIdleTTL = 90 * 24 * time.Hour
	}
	if c.Automations.Interval <= 0 {
		c.Automations.Interval = 5 * time.Minute
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
	c.Webauth.TOTPMode, c.totpWarning = TOTPModeOf(c.Webauth.TOTPMode)
	if c.Vault.CacheSize == 0 {
		c.Vault.CacheSize = 128
	}
	if c.I18n.DefaultLang == "" {
		c.I18n.DefaultLang = "en"
	}
	if len(c.I18n.EnabledLangs) == 0 {
		c.I18n.EnabledLangs = i18n.Languages()
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
