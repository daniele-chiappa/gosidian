package config

// DefaultThemePreset is the theme preset applied when the config file is
// created fresh or when Preset is left empty. Picked to match the historical
// gosidian look (the "Midnight Luxury" palette shipped since v1.0).
const DefaultThemePreset = "midnight-luxury"

// CustomThemePreset signals that the user's five colors override any preset.
// Written by the Settings form when the user flips the dropdown to
// "Custom" and edits the color pickers directly.
const CustomThemePreset = "custom"

// ThemePresets enumerates the built-in palettes selectable from Settings.
// Each preset defines the five root colors referenced by app.css via CSS
// custom properties emitted by /theme.css. The "custom" key is intentionally
// absent: when cfg.Theme.Preset == "custom" (or any unknown string)
// EffectiveTheme returns the user's five fields as-is.
//
// Design rationale:
//   - Midnight Luxury: the historical default — dark blue/teal with gold.
//   - Light clean:     inverted for bright environments, readable in daylight.
//   - High contrast:   WCAG AAA-style pure black/white with saturated accents,
//                      for accessibility or very bright monitors.
var ThemePresets = map[string]ThemeConfig{
	"midnight-luxury": {
		Preset:       "midnight-luxury",
		DeepSpace:    "#0B0C10",
		Gunmetal:     "#1F2833",
		SilverMist:   "#C5C6C7",
		ElectricBlue: "#66FCF1",
		GoldLeaf:     "#C5A021",
	},
	"light-clean": {
		Preset:       "light-clean",
		DeepSpace:    "#FAFAFA",
		Gunmetal:     "#EFEFEF",
		SilverMist:   "#333333",
		ElectricBlue: "#0066CC",
		GoldLeaf:     "#B8860B",
	},
	"high-contrast": {
		Preset:       "high-contrast",
		DeepSpace:    "#000000",
		Gunmetal:     "#1A1A1A",
		SilverMist:   "#FFFFFF",
		ElectricBlue: "#00FFFF",
		GoldLeaf:     "#FFFF00",
	},
}

// IsKnownPreset reports whether name matches a built-in preset (not "custom").
func IsKnownPreset(name string) bool {
	_, ok := ThemePresets[name]
	return ok
}

// EffectiveTheme returns the five colors that should be rendered for the
// current preset. When Preset matches a built-in palette, the preset's
// colors override the individual fields (so the color pickers in Settings
// are ignored for named presets). When Preset is "custom" or unknown the
// user's five fields are returned as-is.
func (c ThemeConfig) EffectiveTheme() ThemeConfig {
	if p, ok := ThemePresets[c.Preset]; ok {
		return p
	}
	// Custom or unknown: keep user's individual colors; Preset is normalised
	// to "custom" so callers always see a well-defined string downstream.
	out := c
	if out.Preset != CustomThemePreset {
		out.Preset = CustomThemePreset
	}
	return out
}
