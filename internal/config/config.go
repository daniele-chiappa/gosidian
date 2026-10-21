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
	Pipelines    PipelinesConfig    `toml:"pipelines"`
	Web          WebConfig          `toml:"web"`

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
	// Durability is when writes reach the disk (IMP-185): "batch" (default)
	// syncs each write on its own but a pipeline step's once at its end,
	// "always" every write, "none" never (vault.ParseDurability).
	Durability string `toml:"durability"`
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

// PipelinesConfig runs the pipelines of the vault (IMP-166/167, ADR-035):
// notes `type: pipeline` executed by a job runner in the server. Off by
// default: with it off there is no runner, no route and no tool at work.
// A project also needs its use_pipelines flag.
type PipelinesConfig struct {
	Enabled bool `toml:"enabled"`
	// Workers is how many steps run at once; default 2.
	Workers int `toml:"workers"`
	// InteractiveWorkers run only the steps of the interactive runs (a
	// chat's messages, priority: interactive), on top of Workers, which
	// take those first too; default 1, 0 for none of their own.
	InteractiveWorkers int `toml:"interactive_workers"`
	// MaxSyncWait is the longest a start waits for its run to end, over
	// HTTP (wait) or MCP (memory_pipeline_run's wait); default 60s.
	MaxSyncWait time.Duration `toml:"max_sync_wait"`
	// RunTokenRate is how many runs a run token starts in a minute, and how
	// many resumes a run's key makes; default 60. A chat's widget starts a
	// run per message of everyone who chats.
	RunTokenRate int `toml:"run_token_rate"`
	// Retention is how long the runs that ended are kept in jobs.db;
	// default 30 days, 0 keeps them.
	Retention time.Duration `toml:"retention"`
	// StepTimeout is the longest a step may run (its lease); default 15m.
	StepTimeout time.Duration `toml:"step_timeout"`
	// MaxInputBytes caps a run's input; default 256 KiB.
	MaxInputBytes int `toml:"max_input_bytes"`
	// MaxOutputBytes caps a step's output; default 1 MiB.
	MaxOutputBytes int `toml:"max_output_bytes"`
	// MaxLLMTokens caps the LLM tokens of one run; 0: no cap.
	MaxLLMTokens int `toml:"max_llm_tokens"`
	// MaxWait is the longest a wait.call may wait; default 7 days.
	MaxWait time.Duration `toml:"max_wait"`
	// MaxRunDepth is how many runs may start each other in a chain
	// through pipeline.run; default 4.
	MaxRunDepth int `toml:"max_run_depth"`
	// MaxRunChildren is how many runs one run may start through
	// pipeline.run, in an each too; default 1000.
	MaxRunChildren int `toml:"max_run_children"`
	// MaxListItems caps what a node lists or is asked for in one call:
	// a page of note.list, note.sections' only; default 5000.
	MaxListItems int `toml:"max_list_items"`
	// MaxChunkMeta caps the meta of a chunk note.sections gives, in bytes
	// of JSON; default 16384, the database plugin's limits.max_meta.
	MaxChunkMeta int `toml:"max_chunk_meta"`
	// The marks note.mark puts on a note (IMP-188): the sets of a note,
	// the marks of a set, and the characters of a mark's label and text;
	// default 20, 500, 60 and 500.
	MaxMarkSets  int `toml:"max_mark_sets"`
	MaxMarks     int `toml:"max_marks"`
	MaxMarkLabel int `toml:"max_mark_label"`
	MaxMarkText  int `toml:"max_mark_text"`
	// MaxPluginFiles caps the files of one call of a plugin, a node's or
	// an agent's tool; default 100. MaxPluginFilesOut caps the files one
	// result gives back; default 200.
	MaxPluginFiles    int `toml:"max_plugin_files"`
	MaxPluginFilesOut int `toml:"max_plugin_files_out"`
	// MaxPluginFilesBytes caps the bytes of the files a plugin gives back
	// in one result; default 100 MiB.
	MaxPluginFilesBytes int64 `toml:"max_plugin_files_bytes"`
	// MaxPluginGraphBytes caps a plugin's graph; default 8 MiB.
	MaxPluginGraphBytes int `toml:"max_plugin_graph_bytes"`
	// StarlarkSteps caps the steps of a starlark script; default 10
	// million.
	StarlarkSteps uint64 `toml:"starlark_steps"`
	// HTTPAllowlist holds the URL prefixes http.request and the callbacks
	// may call; empty: none.
	HTTPAllowlist []string `toml:"http_allowlist"`
	// PublicURL is the server's address in the resume URLs; default the
	// OAuth issuer, else a path.
	PublicURL string `toml:"public_url"`
	// FilesURL is the server's address as the plugins reach it, for the
	// files they fetch by reference (IMP-172): http://gosidian:8080 on the
	// network of a compose file. Default PublicURL, which can go through a
	// proxy that caps the size of an answer. A plugin's own files_url wins.
	FilesURL string `toml:"files_url"`
	// MCPURL is gosidian's MCP endpoint as the plugins reach it, for the
	// nodes that ask for it with a token of the call (IMP-193); default
	// FilesURL (else PublicURL) with /mcp. A plugin's own mcp_url wins.
	MCPURL string `toml:"mcp_url"`
	// Timezone is the zone of the schedule triggers; default the
	// automations' one.
	Timezone string `toml:"timezone"`
	// LLM lists the language model services ([[pipelines.llm]]).
	LLM []LLMProvider `toml:"llm"`
	// Plugins lists the plugins, MCP servers whose nodes the pipelines
	// use ([[pipelines.plugins]], IMP-168, docs/plugins.md).
	Plugins []PluginConfig `toml:"plugins"`
}

// PluginConfig registers a plugin: an MCP server reached over Streamable
// HTTP. Its nodes are <name>.<node>, usable once the owner accepted its
// manifest (gosidian plugin accept).
type PluginConfig struct {
	Name string `toml:"name"`
	URL  string `toml:"url"` // the MCP endpoint, http://host:port/mcp
	// TokenSecret names the secret with the plugin's bearer token
	// (gosidian secret set); empty for a plugin without one.
	TokenSecret string `toml:"token_secret"`
	// Timeout is the longest a call to the plugin may take; default the
	// step timeout.
	Timeout time.Duration `toml:"timeout"`
	// FilesURL is the server's address as this plugin reaches it, for the
	// files it fetches by reference; default [pipelines] files_url.
	FilesURL string `toml:"files_url"`
	// MCPURL is gosidian's MCP endpoint as this plugin reaches it; default
	// [pipelines] mcp_url.
	MCPURL string `toml:"mcp_url"`
}

// LLMProvider is a service with an OpenAI-compatible API.
type LLMProvider struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"` // up to /v1
	// APIKeySecret names the secret with the key (gosidian secret set);
	// empty for a service without keys (Ollama).
	APIKeySecret string `toml:"api_key_secret"`
	Model        string `toml:"model"` // the default model
}

// MCPConfig caps how aggressively an MCP client may mutate the vault.
// The write rate counts per MCP session, the note size per write. Zero
// values mean "use defaults".
type MCPConfig struct {
	WritePerMinute int   `toml:"write_per_minute"` // default 60, per MCP session; a token gets 5× (IMP-141)
	MaxNoteBytes   int64 `toml:"max_note_bytes"`   // default 1 MiB
	// MaxPluginResult caps the bytes of a plugin tool's result an agent
	// gets; default 1 MiB.
	MaxPluginResult int64 `toml:"max_plugin_result"`
	// PluginToolTimeout caps an agent's call of a plugin's tool, within
	// the plugin's own timeout; default 2m.
	PluginToolTimeout time.Duration `toml:"plugin_tool_timeout"`
	// MaxStepOutputShown is how many bytes of a step's output
	// memory_pipelines shows; default 4096.
	MaxStepOutputShown int `toml:"max_step_output_shown"`
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

// WebConfig holds the limits of the web UI's views.
type WebConfig struct {
	// GraphDefaultNodes is how many of the most linked nodes a graph asked
	// without a limit keeps; default 2000. GraphMaxNodes is the most a
	// graph, or a graph block, may ask for; default 5000.
	GraphDefaultNodes int `toml:"graph_default_nodes"`
	GraphMaxNodes     int `toml:"graph_max_nodes"`
	// GraphMaxEdges is the most edges a plugin's graph may have; default
	// 50000.
	GraphMaxEdges int `toml:"graph_max_edges"`
	// CutMaxChunks is the most chunks of a note the cut view reads from
	// an archive; default 5000.
	CutMaxChunks int `toml:"cut_max_chunks"`
	// PluginTimeout caps a plugin's answer the web UI waits for (a graph,
	// the cut view), within the plugin's own timeout; default 2m.
	PluginTimeout time.Duration `toml:"plugin_timeout"`
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
	cfg.Pipelines.Retention = 30 * 24 * time.Hour
	// And here, as 0 means none of their own (IMP-191).
	cfg.Pipelines.InteractiveWorkers = 1
	// And here, as 0 turns the cache off: applyDefaults put it back to 128
	// (IMP-163).
	cfg.Vault.CacheSize = 128
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
	if v := os.Getenv("GOSIDIAN_MCP_MAX_PLUGIN_RESULT"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("GOSIDIAN_MCP_MAX_PLUGIN_RESULT: %q is no number of bytes", v)
		}
		c.MCP.MaxPluginResult = n
	}
	if v := os.Getenv("GOSIDIAN_MCP_PLUGIN_TOOL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("GOSIDIAN_MCP_PLUGIN_TOOL_TIMEOUT: %q is no duration", v)
		}
		c.MCP.PluginToolTimeout = d
	}
	if err := envPositiveInts(map[string]*int{"GOSIDIAN_MCP_MAX_STEP_OUTPUT_SHOWN": &c.MCP.MaxStepOutputShown}); err != nil {
		return err
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
	if err := c.applyPipelinesEnv(); err != nil {
		return err
	}
	if err := c.applyWebEnv(); err != nil {
		return err
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
	if v := os.Getenv("GOSIDIAN_VAULT_DURABILITY"); v != "" {
		c.Vault.Durability = v
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
	if c.MCP.MaxPluginResult == 0 {
		c.MCP.MaxPluginResult = 1 << 20 // 1 MiB
	}
	if c.MCP.PluginToolTimeout <= 0 {
		c.MCP.PluginToolTimeout = 2 * time.Minute
	}
	if c.MCP.MaxStepOutputShown <= 0 {
		c.MCP.MaxStepOutputShown = 4 << 10
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
	if c.Pipelines.Workers <= 0 {
		c.Pipelines.Workers = 2
	}
	if c.Pipelines.StepTimeout <= 0 {
		c.Pipelines.StepTimeout = 15 * time.Minute
	}
	if c.Pipelines.InteractiveWorkers < 0 {
		c.Pipelines.InteractiveWorkers = 0
	}
	if c.Pipelines.MaxSyncWait <= 0 {
		c.Pipelines.MaxSyncWait = time.Minute
	}
	if c.Pipelines.MaxInputBytes <= 0 {
		c.Pipelines.MaxInputBytes = 256 << 10
	}
	if c.Pipelines.MaxOutputBytes <= 0 {
		c.Pipelines.MaxOutputBytes = 1 << 20
	}
	if c.Pipelines.MaxWait <= 0 {
		c.Pipelines.MaxWait = 7 * 24 * time.Hour
	}
	for _, n := range []struct {
		dst *int
		def int
	}{
		{&c.Pipelines.MaxRunDepth, 4},
		{&c.Pipelines.MaxRunChildren, 1000},
		{&c.Pipelines.RunTokenRate, 60},
		{&c.Pipelines.MaxListItems, 5000},
		{&c.Pipelines.MaxChunkMeta, 16 << 10},
		{&c.Pipelines.MaxMarkSets, 20},
		{&c.Pipelines.MaxMarks, 500},
		{&c.Pipelines.MaxMarkLabel, 60},
		{&c.Pipelines.MaxMarkText, 500},
		{&c.Pipelines.MaxPluginFiles, 100},
		{&c.Pipelines.MaxPluginFilesOut, 200},
		{&c.Pipelines.MaxPluginGraphBytes, 8 << 20},
		{&c.Web.GraphDefaultNodes, 2000},
		{&c.Web.GraphMaxNodes, 5000},
		{&c.Web.GraphMaxEdges, 50000},
		{&c.Web.CutMaxChunks, 5000},
	} {
		if *n.dst <= 0 {
			*n.dst = n.def
		}
	}
	if c.Pipelines.MaxPluginFilesBytes <= 0 {
		c.Pipelines.MaxPluginFilesBytes = 100 << 20
	}
	if c.Pipelines.StarlarkSteps == 0 {
		c.Pipelines.StarlarkSteps = 10_000_000
	}
	if c.Web.PluginTimeout <= 0 {
		c.Web.PluginTimeout = 2 * time.Minute
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

// parsePluginsEnv reads GOSIDIAN_PIPELINES_PLUGINS: name=url entries,
// comma-separated, for plugins without a token (a token needs
// [[pipelines.plugins]] token_secret in config.toml).
func parsePluginsEnv(v string) ([]PluginConfig, error) {
	var out []PluginConfig
	for _, e := range strings.Split(v, ",") {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		name, url, ok := strings.Cut(e, "=")
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if !ok || name == "" || url == "" {
			return nil, fmt.Errorf("%q is not name=url", e)
		}
		out = append(out, PluginConfig{Name: name, URL: url})
	}
	return out, nil
}

// mergePlugins adds env to file, an entry of env replacing the URL of the
// plugin of the same name (its token and timeout stay).
func mergePlugins(file, env []PluginConfig) []PluginConfig {
	for _, p := range env {
		found := false
		for i := range file {
			if file[i].Name == p.Name {
				file[i].URL, found = p.URL, true
			}
		}
		if !found {
			file = append(file, p)
		}
	}
	return file
}

// applyPipelinesEnv reads the GOSIDIAN_PIPELINES_* variables. One LLM
// provider can come from the environment too, named "default", for a
// compose file that has no config.toml.
// applyWebEnv reads the limits of the web UI's views (IMP-190).
func (c *Config) applyWebEnv() error {
	if err := envPositiveInts(map[string]*int{
		"GOSIDIAN_WEB_GRAPH_DEFAULT_NODES": &c.Web.GraphDefaultNodes,
		"GOSIDIAN_WEB_GRAPH_MAX_NODES":     &c.Web.GraphMaxNodes,
		"GOSIDIAN_WEB_GRAPH_MAX_EDGES":     &c.Web.GraphMaxEdges,
		"GOSIDIAN_WEB_CUT_MAX_CHUNKS":      &c.Web.CutMaxChunks,
	}); err != nil {
		return err
	}
	if v := os.Getenv("GOSIDIAN_WEB_PLUGIN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("GOSIDIAN_WEB_PLUGIN_TIMEOUT: %q is no duration", v)
		}
		c.Web.PluginTimeout = d
	}
	return nil
}

// envPositiveInts sets each setting whose variable is set, to a number
// above 0: a cap of 0 would refuse everything.
func envPositiveInts(vars map[string]*int) error {
	for env, dst := range vars {
		v := os.Getenv(env)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("%s: %q is no number above 0", env, v)
		}
		*dst = n
	}
	return nil
}

func (c *Config) applyPipelinesEnv() error {
	if v := os.Getenv("GOSIDIAN_PIPELINES_ENABLED"); v != "" {
		c.Pipelines.Enabled = envBool(v)
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_PIPELINES_WORKERS: %w", err)
		}
		c.Pipelines.Workers = n
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_INTERACTIVE_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("GOSIDIAN_PIPELINES_INTERACTIVE_WORKERS: %q, 0 or more", v)
		}
		c.Pipelines.InteractiveWorkers = n
	}
	for _, d := range []struct {
		env string
		dst *time.Duration
	}{
		{"GOSIDIAN_PIPELINES_RETENTION", &c.Pipelines.Retention},
		{"GOSIDIAN_PIPELINES_STEP_TIMEOUT", &c.Pipelines.StepTimeout},
		{"GOSIDIAN_PIPELINES_MAX_WAIT", &c.Pipelines.MaxWait},
		{"GOSIDIAN_PIPELINES_MAX_SYNC_WAIT", &c.Pipelines.MaxSyncWait},
	} {
		if v := os.Getenv(d.env); v != "" {
			dur, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%s: %w", d.env, err)
			}
			*d.dst = dur
		}
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_MAX_LLM_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_PIPELINES_MAX_LLM_TOKENS: %w", err)
		}
		c.Pipelines.MaxLLMTokens = n
	}
	// The caps of a run's input and of a step's output are the
	// installer's to raise: a test that keeps hundreds of results in a
	// step needs more than the 1 MiB default.
	for _, n := range []struct {
		env string
		dst *int
	}{
		{"GOSIDIAN_PIPELINES_MAX_INPUT_BYTES", &c.Pipelines.MaxInputBytes},
		{"GOSIDIAN_PIPELINES_MAX_OUTPUT_BYTES", &c.Pipelines.MaxOutputBytes},
	} {
		if v := os.Getenv(n.env); v != "" {
			b, err := strconv.Atoi(v)
			if err != nil || b <= 0 {
				return fmt.Errorf("%s: %q is no number of bytes", n.env, v)
			}
			*n.dst = b
		}
	}
	// The other caps of the pipelines (IMP-190), each the installer's to
	// raise.
	if err := envPositiveInts(map[string]*int{
		"GOSIDIAN_PIPELINES_MAX_RUN_DEPTH":          &c.Pipelines.MaxRunDepth,
		"GOSIDIAN_PIPELINES_MAX_RUN_CHILDREN":       &c.Pipelines.MaxRunChildren,
		"GOSIDIAN_PIPELINES_RUN_TOKEN_RATE":         &c.Pipelines.RunTokenRate,
		"GOSIDIAN_PIPELINES_MAX_LIST_ITEMS":         &c.Pipelines.MaxListItems,
		"GOSIDIAN_PIPELINES_MAX_CHUNK_META":         &c.Pipelines.MaxChunkMeta,
		"GOSIDIAN_PIPELINES_MAX_MARK_SETS":          &c.Pipelines.MaxMarkSets,
		"GOSIDIAN_PIPELINES_MAX_MARKS":              &c.Pipelines.MaxMarks,
		"GOSIDIAN_PIPELINES_MAX_MARK_LABEL":         &c.Pipelines.MaxMarkLabel,
		"GOSIDIAN_PIPELINES_MAX_MARK_TEXT":          &c.Pipelines.MaxMarkText,
		"GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES":       &c.Pipelines.MaxPluginFiles,
		"GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES_OUT":   &c.Pipelines.MaxPluginFilesOut,
		"GOSIDIAN_PIPELINES_MAX_PLUGIN_GRAPH_BYTES": &c.Pipelines.MaxPluginGraphBytes,
	}); err != nil {
		return err
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES_BYTES"); v != "" {
		b, err := strconv.ParseInt(v, 10, 64)
		if err != nil || b <= 0 {
			return fmt.Errorf("GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES_BYTES: %q is no number of bytes", v)
		}
		c.Pipelines.MaxPluginFilesBytes = b
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_STARLARK_STEPS"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == 0 {
			return fmt.Errorf("GOSIDIAN_PIPELINES_STARLARK_STEPS: %q is no number of steps", v)
		}
		c.Pipelines.StarlarkSteps = n
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_HTTP_ALLOWLIST"); v != "" {
		var prefixes []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				prefixes = append(prefixes, p)
			}
		}
		c.Pipelines.HTTPAllowlist = prefixes
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_PUBLIC_URL"); v != "" {
		c.Pipelines.PublicURL = v
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_FILES_URL"); v != "" {
		c.Pipelines.FilesURL = v
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_MCP_URL"); v != "" {
		c.Pipelines.MCPURL = v
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_TIMEZONE"); v != "" {
		c.Pipelines.Timezone = v
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_PLUGINS"); v != "" {
		plugins, err := parsePluginsEnv(v)
		if err != nil {
			return fmt.Errorf("GOSIDIAN_PIPELINES_PLUGINS: %w", err)
		}
		c.Pipelines.Plugins = mergePlugins(c.Pipelines.Plugins, plugins)
	}
	if v := os.Getenv("GOSIDIAN_PIPELINES_LLM_URL"); v != "" {
		p := LLMProvider{Name: "default", BaseURL: v, Model: os.Getenv("GOSIDIAN_PIPELINES_LLM_MODEL"), APIKeySecret: os.Getenv("GOSIDIAN_PIPELINES_LLM_KEY_SECRET")}
		replaced := false
		for i := range c.Pipelines.LLM {
			if c.Pipelines.LLM[i].Name == "default" {
				c.Pipelines.LLM[i], replaced = p, true
			}
		}
		if !replaced {
			c.Pipelines.LLM = append([]LLMProvider{p}, c.Pipelines.LLM...)
		}
	}
	return nil
}
