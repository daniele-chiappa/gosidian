# Design — gosidian

Locked design system of the web UI. Hallmark runs read this file first;
views defer to it. Amend intentionally — the file is the rule. Written on
2026-10-08 from the code as it stands, not as a target: where the system
has no token, this file says so.

## System
- Genre · modern-minimal — a developer tool: dense, keyboard-first, restrained
- Macrostructure · none, an application shell: top bar, resizable sidebar
  (menu, vault tree, recent notes), plancia windows side by side
- Theme · five presets on `<html data-preset>`: Catppuccin Mocha (dark,
  default), Catppuccin Latte (light), Tokyo Night, Solarized Light, Custom
  (Mocha plus per-token overrides set in Settings)
- Axes · tinted dark or light paper / no display face / one blue accent

## Tokens (canonical · `src/styles/tokens.css` is the source of truth)
Colours are `R G B` triplets, read as `rgb(var(--color-x) / <alpha>)`
through the Tailwind names in `tailwind.config.ts` (`bg-surface`,
`text-text-muted`, `border-border`, …).
```css
:root {
  --color-bg; --color-bg-elevated;             /* paper, raised paper */
  --color-surface; --color-surface-hover;      /* rows, cards, hover */
  --color-text; --color-text-muted; --color-text-inverse;
  --color-accent; --color-accent-hover; --color-accent-fg; /* fg = ink on accent */
  --color-danger; --color-warning; --color-success; --color-info;
  --color-border; --color-border-strong; --color-overlay;
  --color-focus: var(--color-accent);          /* focus rings, `ring-focus` */
  --shadow-sm; --shadow-md; --shadow-lg;
  --radius-sm: 0.25rem; --radius-md: 0.5rem; --radius-lg: 0.75rem;
  --control-h: 2rem;                           /* input, select, button: `h-control` */
  --control-h-sm: 1.75rem;                     /* window toolbars: `h-control-sm` */
}
```
- Fonts · `font-sans` system-ui stack, `font-mono` ui-monospace stack; no
  web font (strict CSP), no display face
- Type · Tailwind scale; `text-xs` and `text-sm` carry the UI, `text-lg` to
  `text-2xl` the window headings, `prose` (typography plugin) the notes,
  its colours read from these tokens in `tailwind.config.ts` (no
  `prose-invert`)
- Code · the server marks highlighted tokens with chroma classes;
  `src/styles/code.css` paints them: keywords accent, strings success,
  numbers warning, names info, comments text-muted
- Spacing · Tailwind's 4 px scale
- Focus · `focus:ring-focus` (2 px); `--color-focus` is the accent unless
  a preset sets its own
- Controls · two heights: 32 px (`h-control`) for inputs, selects and
  buttons in a form, 28 px (`h-control-sm`) in window toolbars
- Not tokenised · motion durations and easings (Tailwind defaults),
  z-index (plancia sidebar 40, dialogs 50, menus 60)
- plancia · its core variables map to these tokens in
  `src/styles/plancia-bridge.css`, so windows follow every preset

## CTA voice
- Primary · `bg-accent text-accent-fg hover:bg-accent-hover`, `rounded`,
  compact in window toolbars (`px-2 py-1 text-xs`)
- Secondary · `border border-border hover:bg-surface-hover`, same radius
- Destructive · `text-danger`, no fill
- Icons · Lucide only, 14–16 px

## Motion stance
- Silent: colour and opacity on hover, the tree's disclosure arrow turns;
  no entrances
- Reduced-motion fallback · not declared yet

## Exports
`src/styles/tokens.css` is the source of truth; `tailwind.config.ts`,
`src/styles/plancia-bridge.css` and `src/styles/code.css` map it.
