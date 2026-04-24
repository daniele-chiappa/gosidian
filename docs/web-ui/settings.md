# Settings

The `/settings` page edits `<vault>/.gosidian/config.toml`. Changes to
the theme take effect on the next page refresh; git sync changes take
effect on the next server restart.

## Theme

gosidian ships three built-in theme presets (v1.10):

| Preset | Default palette (5 roots) |
|---|---|
| **Midnight Luxury** (default) | `#0B0C10` / `#1F2833` / `#C5C6C7` / `#66FCF1` / `#C5A021` |
| **Light clean** | `#FAFAFA` / `#EFEFEF` / `#333333` / `#0066CC` / `#B8860B` |
| **High contrast** (WCAG-AAA) | `#000000` / `#1A1A1A` / `#FFFFFF` / `#00FFFF` / `#FFFF00` |

Selecting a named preset from the dropdown sets all five colors at
once — the color pickers are hidden because the preset is the single
source of truth.

Switching the preset to **Custom** unlocks the five color pickers so
you can define an arbitrary palette. The five roots drive `--bg-base`,
`--bg-elev-1`, `--text-secondary`, `--accent-cool`, and
`--accent-gold`; every other design token in `app.css` is derived
from them.

The choice persists in `config.toml` under `[theme]` with `preset =
"midnight-luxury"` (or the selected variant). Environment variable
`GOSIDIAN_THEME_PRESET` overrides the file setting at startup.

## Language

The topbar `<select class="lang-switcher">` offers five languages:

- **IT** (Italian) — complete
- **EN** (English) — reference
- **ES / FR / DE** — v1.10 scaffolding stubs; topbar strings
  translated, everything else falls back to English

Selecting a language navigates to `/api/i18n?lang=<code>&next=<path>`,
which sets the `gosidian_lang` cookie (1-year TTL) and redirects
back to the current page. MCP clients request a language through
the standard `Accept-Language` header.

To contribute a complete translation, see the *Translating gosidian*
section in [CONTRIBUTING.md](../../CONTRIBUTING.md).

## Git sync

Optional. When enabled, gosidian auto-commits the vault after every
write (debounced, default 30s) and pushes to the configured remote
when `git.push` is true.

- `Remote URL` — clone URL of the git remote (Gitea, GitHub,
  self-hosted)
- `Branch` — default `main`
- `Author name` / `Author email` — used in the commit metadata
- `Debounce` — minimum time between consecutive commits (`30s`, `2m`)
- `Push` — checkbox; when off, gosidian commits locally only
- `Env var for the HTTPS token` — name of the environment variable
  containing the push token (e.g. `GITEA_TOKEN`). The token itself is
  **never** persisted to disk — it has to be exported in gosidian's
  environment

Push failures surface in `/healthz` (`git_sync.healthy=false` with
`last_error` + `last_error_at`) and as a red state on the metric
`gosidian_gitsync_status` (0=disabled, 1=healthy, 2=degraded).

Git sync changes apply on the **next server restart**, not
immediately — a boot-time invariant.
