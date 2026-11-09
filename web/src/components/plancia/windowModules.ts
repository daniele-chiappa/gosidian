/**
 * Tone classes for header tags (key = a free tone string, usually a topic).
 * Minimal palette: gosidian windows rarely carry cross-module tags, but the
 * WindowFrame renders them for parity with the products-dc pattern. Resolved
 * against gosidian's semantic tokens with a neutral fallback.
 */
export const WINDOW_TONES: Record<string, string> = {
  accent: 'bg-accent/10 text-accent',
  info: 'bg-info/10 text-info',
  warning: 'bg-warning/10 text-warning',
  danger: 'bg-danger/10 text-danger',
}

export const windowTone = (tone: string | undefined): string =>
  (tone && WINDOW_TONES[tone]) || 'bg-surface-hover text-text-muted'
