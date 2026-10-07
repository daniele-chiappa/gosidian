package v1

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/projects"
)

// settingsView is the JSON shape the SPA reads. Sensitive fields
// (git push tokens, bcrypt-hashed user records, MCP token plaintexts)
// are NEVER surfaced — the SPA gets enough to render the settings
// page and the gitsync status badge, nothing more.
//
// The shape is intentionally a subset of internal/config.Config: we
// reserve the right to evolve config.toml independently and add a
// translation layer here when fields diverge. Today the mapping is
// 1:1 minus secrets.
type settingsView struct {
	Git      gitSettings   `json:"git"`
	Trash    trashSettings `json:"trash"`
	I18n     i18nSettings  `json:"i18n"`
	MCP      mcpSettings   `json:"mcp"`
	TOTPMode string        `json:"totp_mode"` // off | optional | required (global two-factor policy)
	// DefaultVisibility is the visibility (public | internal | private) given
	// to projects created from now on and to folders that appear on disk
	// without an entry; existing projects keep their own value.
	DefaultVisibility string `json:"default_visibility"`
	// PersonalProjects: a new account gets a private project named after it
	// where it is admin (default on).
	PersonalProjects bool `json:"personal_projects"`
	// AnchorsEnabled / GlobalsEnabled are the read-only server master switches
	// (GOSIDIAN_ANCHORS_ENABLED / GOSIDIAN_GLOBAL_ENABLED). The SPA reads them
	// to tell whether a project's use_anchors/use_globals flag has any effect.
	// Never settable via PUT — they live in config/env, not the SPA.
	AnchorsEnabled bool `json:"anchors_enabled"`
	GlobalsEnabled bool `json:"globals_enabled"`
	// EnvOverrides lists the settings (dotted paths of this view, e.g.
	// "git.author_email") that a GOSIDIAN_* environment variable sets. The
	// values above are the effective ones — file, then env, then defaults —
	// and these cannot be changed from here: a PUT would save them to
	// config.toml only to lose to the env again.
	EnvOverrides []string `json:"env_overrides"`
}

type gitSettings struct {
	Enabled     bool   `json:"enabled"`
	Remote      string `json:"remote"`
	Branch      string `json:"branch"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	DebounceMS  int64  `json:"debounce_ms"`
	Push        bool   `json:"push"`
	TokenEnv    string `json:"token_env"` // env var name only; never the value
}

type trashSettings struct {
	Enabled     bool  `json:"enabled"`
	RetentionMS int64 `json:"retention_ms"`
}

type i18nSettings struct {
	DefaultLang  string   `json:"default_lang"`
	EnabledLangs []string `json:"enabled_langs"`
}

type mcpSettings struct {
	WritePerMinute int   `json:"write_per_minute"`
	MaxNoteBytes   int64 `json:"max_note_bytes"`
}

// updateSettingsRequest covers PATCH-style updates. Pointer fields
// distinguish "not present" (no change) from "explicitly cleared".
// Only owners may PUT.
type updateSettingsRequest struct {
	Git *struct {
		Enabled     *bool   `json:"enabled,omitempty"`
		Remote      *string `json:"remote,omitempty"`
		Branch      *string `json:"branch,omitempty"`
		AuthorName  *string `json:"author_name,omitempty"`
		AuthorEmail *string `json:"author_email,omitempty"`
		DebounceMS  *int64  `json:"debounce_ms,omitempty"`
		Push        *bool   `json:"push,omitempty"`
		TokenEnv    *string `json:"token_env,omitempty"`
	} `json:"git,omitempty"`
	Trash *struct {
		Enabled     *bool  `json:"enabled,omitempty"`
		RetentionMS *int64 `json:"retention_ms,omitempty"`
	} `json:"trash,omitempty"`
	I18n *struct {
		DefaultLang  *string  `json:"default_lang,omitempty"`
		EnabledLangs []string `json:"enabled_langs,omitempty"`
	} `json:"i18n,omitempty"`
	MCP *struct {
		WritePerMinute *int   `json:"write_per_minute,omitempty"`
		MaxNoteBytes   *int64 `json:"max_note_bytes,omitempty"`
	} `json:"mcp,omitempty"`
	TOTPMode          *string `json:"totp_mode,omitempty"`
	DefaultVisibility *string `json:"default_visibility,omitempty"`
	PersonalProjects  *bool   `json:"personal_projects,omitempty"`
}

func (r *Router) handleSettings(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.getSettings(w, req)
	case http.MethodPut:
		r.putSettings(w, req)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

// getSettings is readable by any authenticated user — the SPA needs
// it to render the settings page even for member roles. Sensitive
// fields are stripped above.
func (r *Router) getSettings(w http.ResponseWriter, req *http.Request) {
	if !principalFromContext(req).CanSeeAllProjects() {
		// Settings are a member+ concern; guests have no settings surface.
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "insufficient role")
		return
	}
	view, err := r.effectiveSettings()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	// Members read the settings but cannot change them: a remote URL with
	// credentials in it (file or env) is for the owner only.
	if !principalFromContext(req).CanAdmin() {
		view.Git.Remote = redactUserinfo(view.Git.Remote)
	}
	WriteJSON(w, http.StatusOK, view)
}

// redactUserinfo hides the user:password@ part of a URL remote, which may
// carry a token; other remote forms (scp-like git@host:repo) are unchanged.
func redactUserinfo(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.User == nil || u.Host == "" {
		return remote
	}
	u.User = url.User("redacted")
	return u.String()
}

// effectiveSettings is the settings view the server actually runs with:
// config.toml with the GOSIDIAN_* environment applied on top, as main does at
// boot, plus the list of fields the environment sets.
func (r *Router) effectiveSettings() (settingsView, error) {
	eff, err := r.loadConfig()
	if err != nil {
		return settingsView{}, err
	}
	if err := eff.ApplyEnv(); err != nil {
		return settingsView{}, err
	}
	view := toSettingsView(eff)
	view.DefaultVisibility = r.defaultVisibilitySetting()
	view.PersonalProjects = r.personalProjectsSetting()
	view.EnvOverrides = envOverridden(r.loadConfig)
	return view, nil
}

// envOverridden lists the settings the environment sets: the fields ApplyEnv
// changes on the file's config and on the defaults. Checking both catches an
// env var that repeats the file's value (it still wins over any edit), and
// only misses one equal to both the file and the default.
func envOverridden(loadFile func() (*config.Config, error)) []string {
	defaults := func() (*config.Config, error) { return config.Default(), nil }
	keys := map[string]bool{}
	for _, load := range []func() (*config.Config, error){loadFile, defaults} {
		base, err := load()
		if err != nil {
			continue
		}
		withEnv, err := load()
		if err != nil || withEnv.ApplyEnv() != nil {
			continue
		}
		before := flattenView(toSettingsView(base))
		for k, v := range flattenView(toSettingsView(withEnv)) {
			if !reflect.DeepEqual(before[k], v) {
				keys[k] = true
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// flattenView maps a settings view to dotted JSON paths → leaf values.
func flattenView(v settingsView) map[string]any {
	var tree map[string]any
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &tree)
	out := map[string]any{}
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for k, val := range node {
			if sub, ok := val.(map[string]any); ok {
				walk(prefix+k+".", sub)
				continue
			}
			out[prefix+k] = val
		}
	}
	walk("", tree)
	return out
}

// personalProjectsSetting reports whether new accounts get a personal project.
func (r *Router) personalProjectsSetting() bool {
	return r.deps.Projects != nil && r.deps.Projects.PersonalProjectsEnabled()
}

// defaultVisibilitySetting returns the live default visibility for new
// projects, stored in the projects store rather than config.toml.
func (r *Router) defaultVisibilitySetting() string {
	if r.deps.Projects == nil {
		return projects.VisibilityPrivate
	}
	return r.deps.Projects.DefaultVisibility()
}

// putSettings is owner-only. It loads the current config, applies
// the patch, validates, and saves atomically. A failed validation
// leaves the on-disk file untouched.
func (r *Router) putSettings(w http.ResponseWriter, req *http.Request) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.Role != "owner" {
		WriteError(w, http.StatusForbidden, CodeAuthOwnerOnly, "owner role required to update settings")
		return
	}
	if r.deps.ConfigPath == "" {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "config path not wired")
		return
	}

	var body updateSettingsRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.DefaultVisibility != nil && !projects.ValidVisibility(strings.TrimSpace(*body.DefaultVisibility)) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "default_visibility must be public, internal or private")
		return
	}

	orig, err := r.loadConfig()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	cfg, err := r.loadConfig()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if errMsg := applySettingsPatch(cfg, &body); errMsg != "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, errMsg)
		return
	}
	if key := r.envConflict(orig, cfg); key != "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat,
			key+" is set by a GOSIDIAN_* environment variable; change it there (a value saved here would be overridden at every start)")
		return
	}
	// The languages are checked as the server will run them, environment
	// included: an enabled list saved here must still hold the default a
	// GOSIDIAN_I18N_DEFAULT_LANG sets, and the other way round.
	var effI18n config.I18nConfig
	if body.I18n != nil {
		eff := *cfg
		if err := eff.ApplyEnv(); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		if err := eff.I18n.Validate(); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		effI18n, _ = eff.I18n.Effective()
	}

	if err := config.Save(r.deps.ConfigPath, cfg); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "save: "+err.Error())
		return
	}
	// Apply the TOTP mode to the live webauth store so it takes effect without
	// a restart — only when this save sets it: otherwise any save would push
	// the file's value live over a policy the environment sets (the SPA
	// leaves env-set fields out of the body).
	if body.TOTPMode != nil && r.deps.Auth != nil && r.deps.Auth.WebAuth != nil {
		r.deps.Auth.WebAuth.SetTOTPMode(cfg.Webauth.TOTPMode)
	}
	// The languages too: /version serves them to the SPA from now on.
	if body.I18n != nil {
		SetI18n(effI18n.DefaultLang, effI18n.EnabledLangs)
	}
	// default_visibility lives in the projects store (not config.toml); apply
	// it live.
	if body.DefaultVisibility != nil && r.deps.Projects != nil {
		if err := r.deps.Projects.SetDefaultVisibility(strings.TrimSpace(*body.DefaultVisibility)); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "default_visibility: "+err.Error())
			return
		}
	}
	if body.PersonalProjects != nil && r.deps.Projects != nil {
		if err := r.deps.Projects.SetPersonalProjects(*body.PersonalProjects); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "personal_projects: "+err.Error())
			return
		}
	}

	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: "settings_update",
			Path:   r.deps.ConfigPath,
		})
	}

	view, err := r.effectiveSettings()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// envConflict returns the first env-set setting the patch changes to a value
// other than the effective one, or "". Sending back the effective value (what
// GET returned) is not a conflict, so a client that echoes the whole form is
// fine.
func (r *Router) envConflict(orig, patched *config.Config) string {
	overridden := envOverridden(r.loadConfig)
	if len(overridden) == 0 {
		return ""
	}
	eff, err := r.loadConfig()
	if err != nil || eff.ApplyEnv() != nil {
		return ""
	}
	before, after, effective := flattenView(toSettingsView(orig)), flattenView(toSettingsView(patched)), flattenView(toSettingsView(eff))
	for _, k := range overridden {
		if !reflect.DeepEqual(after[k], before[k]) && !reflect.DeepEqual(after[k], effective[k]) {
			return k
		}
	}
	return ""
}

func (r *Router) loadConfig() (*config.Config, error) {
	if r.deps.ConfigPath == "" {
		return config.Default(), nil
	}
	return config.Load(r.deps.ConfigPath)
}

func toSettingsView(c *config.Config) settingsView {
	return settingsView{
		Git: gitSettings{
			Enabled:     c.Git.Enabled,
			Remote:      c.Git.Remote,
			Branch:      c.Git.Branch,
			AuthorName:  c.Git.AuthorName,
			AuthorEmail: c.Git.AuthorEmail,
			DebounceMS:  c.Git.Debounce.Milliseconds(),
			Push:        c.Git.Push,
			TokenEnv:    c.Git.TokenEnv,
		},
		Trash: trashSettings{
			Enabled:     c.Trash.Enabled,
			RetentionMS: c.Trash.Retention.Milliseconds(),
		},
		I18n: i18nSettings{
			DefaultLang:  c.I18n.DefaultLang,
			EnabledLangs: append([]string(nil), c.I18n.EnabledLangs...),
		},
		MCP: mcpSettings{
			WritePerMinute: c.MCP.WritePerMinute,
			MaxNoteBytes:   c.MCP.MaxNoteBytes,
		},
		TOTPMode:       c.Webauth.TOTPMode,
		AnchorsEnabled: c.AgentAnchors.Enabled,
		GlobalsEnabled: c.Global.Enabled,
	}
}

// applySettingsPatch merges the request body into cfg, validating
// each field as it lands. Returns a non-empty error string on the
// first failure; cfg state may be partially mutated on early-exit
// but the caller hasn't called Save yet so disk is untouched.
func applySettingsPatch(cfg *config.Config, body *updateSettingsRequest) string {
	if body == nil {
		return ""
	}
	if body.Git != nil {
		g := body.Git
		if g.Enabled != nil {
			cfg.Git.Enabled = *g.Enabled
		}
		if g.Remote != nil {
			cfg.Git.Remote = strings.TrimSpace(*g.Remote)
		}
		if g.Branch != nil {
			cfg.Git.Branch = strings.TrimSpace(*g.Branch)
		}
		if g.AuthorName != nil {
			cfg.Git.AuthorName = strings.TrimSpace(*g.AuthorName)
		}
		if g.AuthorEmail != nil {
			cfg.Git.AuthorEmail = strings.TrimSpace(*g.AuthorEmail)
		}
		if g.DebounceMS != nil {
			if *g.DebounceMS < 1000 {
				return "git.debounce_ms must be at least 1000"
			}
			cfg.Git.Debounce = time.Duration(*g.DebounceMS) * time.Millisecond
		}
		if g.Push != nil {
			cfg.Git.Push = *g.Push
		}
		if g.TokenEnv != nil {
			cfg.Git.TokenEnv = strings.TrimSpace(*g.TokenEnv)
		}
		// The remote may come from GOSIDIAN_GIT_REMOTE rather than the file:
		// check what the server will run with.
		remote := cfg.Git.Remote
		if env := os.Getenv("GOSIDIAN_GIT_REMOTE"); env != "" {
			remote = env
		}
		if cfg.Git.Enabled && cfg.Git.Push && remote == "" {
			return "git push enabled but remote is empty"
		}
	}
	if body.Trash != nil {
		if body.Trash.Enabled != nil {
			cfg.Trash.Enabled = *body.Trash.Enabled
		}
		if body.Trash.RetentionMS != nil {
			if *body.Trash.RetentionMS < 0 {
				return "trash.retention_ms cannot be negative"
			}
			cfg.Trash.Retention = time.Duration(*body.Trash.RetentionMS) * time.Millisecond
		}
	}
	if body.I18n != nil {
		if body.I18n.DefaultLang != nil {
			d := strings.ToLower(strings.TrimSpace(*body.I18n.DefaultLang))
			if d == "" {
				return "i18n.default_lang cannot be empty"
			}
			cfg.I18n.DefaultLang = d
		}
		if body.I18n.EnabledLangs != nil {
			cfg.I18n.EnabledLangs = config.NormalizeLangs(body.I18n.EnabledLangs)
		}
	}
	if body.MCP != nil {
		if body.MCP.WritePerMinute != nil {
			if *body.MCP.WritePerMinute < 0 {
				return "mcp.write_per_minute cannot be negative"
			}
			cfg.MCP.WritePerMinute = *body.MCP.WritePerMinute
		}
		if body.MCP.MaxNoteBytes != nil {
			if *body.MCP.MaxNoteBytes < 0 {
				return "mcp.max_note_bytes cannot be negative"
			}
			cfg.MCP.MaxNoteBytes = *body.MCP.MaxNoteBytes
		}
	}
	if body.TOTPMode != nil {
		m := strings.TrimSpace(*body.TOTPMode)
		if m != "off" && m != "optional" && m != "required" {
			return "totp_mode must be off, optional, or required"
		}
		cfg.Webauth.TOTPMode = m
	}
	return ""
}
