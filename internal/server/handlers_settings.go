package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/config"
)

// handleSettings renders a form for editing <vault>/.gosidian/config.toml on
// GET and persists it on POST. Changes take effect on restart.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		http.Error(w, "settings not available: config path not wired", http.StatusInternalServerError)
		return
	}
	cfg, err := config.Load(s.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderSettings(w, r, cfg, "", "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		upd, errMsg := parseSettingsForm(cfg, r)
		if errMsg != "" {
			s.renderSettings(w, r, upd, "", errMsg)
			return
		}
		if err := config.Save(s.configPath, upd); err != nil {
			s.renderSettings(w, r, upd, "", "save failed: "+err.Error())
			return
		}
		s.renderSettings(w, r, upd, "Impostazioni salvate. Tema applicato al prossimo refresh; git sync richiede riavvio.", "")
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, cfg *config.Config, ok, errMsg string) {
	data := map[string]any{
		"Title":    "Settings",
		"Git":      cfg.Git,
		"Debounce": cfg.Git.Debounce.String(),
		"Theme":    cfg.Theme,
		"OK":       ok,
		"Error":    errMsg,
		"Path":     s.configPath,
	}
	s.renderPage(w, r, "settings.html", data)
}

// parseSettingsForm updates cfg in place from posted form values and returns
// a human-readable error string on validation failure.
//
// Partial POST semantics (IMP-027): the Settings form carries a hidden
// "_form_version=settings_full" marker. When present, every checkbox on the
// form is treated as "intentionally submitted" — unchecked boxes become
// false per HTML standard. When absent (e.g. a curl call flipping only the
// theme preset), checkbox fields and blank text fields are left untouched
// so the rest of the config survives the partial update.
func parseSettingsForm(cfg *config.Config, r *http.Request) (*config.Config, string) {
	isFullForm := r.FormValue("_form_version") == "settings_full"

	if isFullForm {
		cfg.Git.Enabled = formCheckbox(r, "git_enabled")
		cfg.Git.Push = formCheckbox(r, "git_push")
	}
	if v, ok := formStringIfPresent(r, "git_remote"); ok || isFullForm {
		cfg.Git.Remote = strings.TrimSpace(v)
	}
	if v, ok := formStringIfPresent(r, "git_branch"); ok || isFullForm {
		cfg.Git.Branch = strings.TrimSpace(v)
	}
	if v, ok := formStringIfPresent(r, "git_author_name"); ok || isFullForm {
		cfg.Git.AuthorName = strings.TrimSpace(v)
	}
	if v, ok := formStringIfPresent(r, "git_author_email"); ok || isFullForm {
		cfg.Git.AuthorEmail = strings.TrimSpace(v)
	}
	if v, ok := formStringIfPresent(r, "git_token_env"); ok || isFullForm {
		cfg.Git.TokenEnv = strings.TrimSpace(v)
	}

	if raw := strings.TrimSpace(r.FormValue("git_debounce")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return cfg, "commit_debounce invalido: " + err.Error()
		}
		if d < time.Second {
			return cfg, "commit_debounce deve essere almeno 1s"
		}
		cfg.Git.Debounce = d
	}

	if cfg.Git.Enabled && cfg.Git.Push && cfg.Git.Remote == "" {
		return cfg, "push abilitato ma remote vuoto"
	}
	if cfg.Git.Enabled && cfg.Git.Push && cfg.Git.TokenEnv == "" {
		return cfg, "push abilitato ma token_env vuoto (niente auth per HTTPS)"
	}

	// Theme preset — when set to a known preset, its 5 colors overwrite the
	// individual fields (the preset is the single source of truth). When set
	// to "custom" (or empty for backward-compat), the 5 color fields drive
	// the palette like before.
	if preset := strings.TrimSpace(r.FormValue("theme_preset")); preset != "" {
		if p, ok := config.ThemePresets[preset]; ok {
			cfg.Theme = p
			return cfg, ""
		}
		if preset != config.CustomThemePreset {
			return cfg, "preset tema sconosciuto: " + preset
		}
		cfg.Theme.Preset = config.CustomThemePreset
	}

	// Theme — 5 root colors. Empty fields keep the current value so a partial
	// POST (e.g. if someone builds a test payload by hand) doesn't blank them.
	themeFields := []struct {
		name string
		dst  *string
	}{
		{"theme_deep_space", &cfg.Theme.DeepSpace},
		{"theme_gunmetal", &cfg.Theme.Gunmetal},
		{"theme_silver_mist", &cfg.Theme.SilverMist},
		{"theme_electric_blue", &cfg.Theme.ElectricBlue},
		{"theme_gold_leaf", &cfg.Theme.GoldLeaf},
	}
	for _, f := range themeFields {
		v := strings.TrimSpace(r.FormValue(f.name))
		if v == "" {
			continue
		}
		if !config.ValidHexColor(v) {
			return cfg, "colore " + f.name + " non valido (atteso #RRGGBB)"
		}
		*f.dst = v
	}
	return cfg, ""
}

func formCheckbox(r *http.Request, name string) bool {
	v := r.FormValue(name)
	return v == "on" || v == "true" || v == "1"
}

// formStringIfPresent returns the raw form value plus a boolean indicating
// whether the field was actually present in the POST body. Used to
// distinguish "field explicitly set to empty string" from "field not sent"
// when handling partial updates (see IMP-027 semantics).
func formStringIfPresent(r *http.Request, name string) (string, bool) {
	if r.PostForm == nil {
		// ParseForm should have been called by the caller; if not, fall back
		// to URL query for safety.
		_ = r.ParseForm()
	}
	_, ok := r.PostForm[name]
	return r.PostForm.Get(name), ok
}
