package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gosidian/gosidian/internal/config"
)

// handleThemeCSS emits a tiny :root{} stylesheet that overrides the 5 root
// design tokens (plus 2 derived rgba soft colors) from the user's config.
// Included in layout.html *after* /static/css/app.css so the cascade wins.
//
// Reads config fresh from disk on every request — cheap, and ensures a save
// in /settings is visible on the next refresh without a server restart.
func (s *Server) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		// Never 500 on the theme: fall back to defaults so the page still
		// renders correctly even if the config file is briefly unreadable.
		cfg = config.Default()
	}
	t := cfg.Theme.EffectiveTheme()
	coolR, coolG, coolB := hexToRGB(t.ElectricBlue)
	goldR, goldG, goldB := hexToRGB(t.GoldLeaf)

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, `:root {
  --bg-base:          %s;
  --bg-elev-1:        %s;
  --text-secondary:   %s;
  --accent-cool:      %s;
  --accent-cool-soft: rgba(%d, %d, %d, 0.12);
  --accent-gold:      %s;
  --accent-gold-soft: rgba(%d, %d, %d, 0.15);
}
`,
		t.DeepSpace, t.Gunmetal, t.SilverMist, t.ElectricBlue,
		coolR, coolG, coolB, t.GoldLeaf, goldR, goldG, goldB,
	)
}

// hexToRGB parses "#RRGGBB" into three 0-255 ints. On any failure it returns
// (245, 246, 247) — the default --text-primary — so the emitted CSS stays
// syntactically valid.
func hexToRGB(s string) (int, int, int) {
	if !config.ValidHexColor(s) {
		return 245, 246, 247
	}
	r, err1 := strconv.ParseInt(s[1:3], 16, 32)
	g, err2 := strconv.ParseInt(s[3:5], 16, 32)
	b, err3 := strconv.ParseInt(s[5:7], 16, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		return 245, 246, 247
	}
	return int(r), int(g), int(b)
}
