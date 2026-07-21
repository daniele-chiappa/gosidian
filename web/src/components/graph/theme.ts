/**
 * Shared theme/color helpers for the graph renderers (2D canvas and
 * 3D WebGL). Canvas/WebGL colors can't reference CSS `var(--color-x)`,
 * so the tokens are resolved up front via getComputedStyle; callers
 * re-resolve when the `data-preset` attribute on <html> changes.
 */

export interface ThemeColors {
  accent: string
  warning: string
  text: string
  textMuted: string
  bg: string
}

// Tokens are stored as `R G B` triplets (Tailwind-alpha-friendly);
// recompose to comma form so they can be string-manipulated into
// rgba() for the dimming alphas. Fallbacks are Catppuccin Mocha so
// the canvas never paints black-on-black.
function tripletToCommaRGB(triplet: string): string | null {
  const parts = triplet.trim().split(/\s+/).filter(Boolean)
  if (parts.length !== 3) return null
  const nums = parts.map((p) => Number(p))
  if (nums.some((n) => !Number.isFinite(n))) return null
  return `rgb(${nums[0]}, ${nums[1]}, ${nums[2]})`
}

export function resolveTheme(): ThemeColors {
  const root = document.documentElement
  const cs = getComputedStyle(root)
  const get = (name: string, fallback: string): string => {
    const raw = cs.getPropertyValue(name)
    const rgb = tripletToCommaRGB(raw)
    return rgb ?? fallback
  }
  return {
    accent: get('--color-accent', 'rgb(137, 180, 250)'),
    warning: get('--color-warning', 'rgb(250, 179, 135)'),
    text: get('--color-text', 'rgb(205, 214, 244)'),
    textMuted: get('--color-text-muted', 'rgb(166, 173, 200)'),
    bg: get('--color-bg', 'rgb(30, 30, 46)'),
  }
}

export function withAlpha(rgb: string, alpha: number): string {
  return rgb.replace('rgb(', 'rgba(').replace(')', `, ${alpha})`)
}

function isDarkBg(theme: ThemeColors): boolean {
  const nums = theme.bg.match(/\d+/g)?.map(Number) ?? [30, 30, 46]
  const [r = 30, g = 30, b = 46] = nums
  return 0.299 * r + 0.587 * g + 0.114 * b < 128
}

/**
 * Deterministic hue for a group name (djb2 hash spread with the
 * golden angle). Stable across sessions and filters so a folder keeps
 * its color no matter which subset of the graph is loaded.
 */
export function groupHue(group: string): number {
  let hash = 5381
  for (let i = 0; i < group.length; i++) hash = (hash * 33) ^ group.charCodeAt(i)
  return Math.abs(hash * 137.508) % 360
}

/**
 * Group → color mapper bound to a resolved theme: HSL-generated from
 * the group name, lightness picked for the bg luminance, accent for
 * the ungrouped default. Rebuild the mapper when the theme changes
 * (the cache is per-instance).
 */
export function makeGroupColor(theme: ThemeColors): (group: string) => string {
  const cache = new Map<string, string>()
  const lightness = isDarkBg(theme) ? 65 : 42
  return (group: string): string => {
    if (!group) return theme.accent
    let color = cache.get(group)
    if (!color) {
      color = `hsl(${Math.round(groupHue(group))}, 58%, ${lightness}%)`
      cache.set(group, color)
    }
    return color
  }
}
