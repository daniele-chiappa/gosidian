// CodeMirror theme from the preset's tokens (src/styles/tokens.css), so it
// follows the theme switcher. The tokens are `R G B` triplets: a colour is
// `rgb(var(--color-x))`, with an alpha `rgb(var(--color-x) / a)`. They were
// passed to var() as colours, with hex fallbacks that never applied, so the
// background, gutter, cursor, selection, active line and tooltips got no
// colour at all (IMP-154, M1).
export const tok = (name: string, alpha?: number) =>
  alpha === undefined ? `rgb(var(--color-${name}))` : `rgb(var(--color-${name}) / ${alpha})`

export const editorThemeSpec: Record<string, Record<string, string>> = {
  '&': {
    height: '100%',
    backgroundColor: tok('bg-elevated'),
    color: tok('text'),
  },
  '.cm-content': {
    caretColor: tok('accent'),
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
    fontSize: '13px',
    padding: '1rem',
  },
  '.cm-gutters': {
    backgroundColor: tok('bg-elevated'),
    color: tok('text-muted'),
    border: 'none',
  },
  '.cm-activeLine': { backgroundColor: tok('surface-hover', 0.3) },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: tok('accent') },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': {
    backgroundColor: `${tok('accent', 0.25)} !important`,
  },
  '.cm-cursor': { borderLeftColor: tok('accent') },
  '.cm-tooltip': {
    backgroundColor: tok('bg-elevated'),
    color: tok('text'),
    border: `1px solid ${tok('border')}`,
    borderRadius: '4px',
  },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': {
    backgroundColor: tok('accent', 0.2),
    color: tok('text'),
  },
  '.cm-searchMatch': { backgroundColor: tok('warning', 0.3) },
  '.cm-panels': { backgroundColor: tok('bg-elevated'), color: tok('text') },
}
