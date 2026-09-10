# Settings

The `/settings` page edits `<state-dir>/config.toml`. Changes to
the theme take effect on the next page refresh; git sync changes take
effect on the next server restart.

## Theme

The **Theme preset** dropdown in Settings lists the frontend presets
defined in `web/src/styles/tokens.css`:

| Preset | Tone |
|---|---|
| `catppuccin-mocha` (default) | dark |
| `tokyo-night` | dark |
| `catppuccin-latte` | light |
| `solarized-light` | light |
| `custom` | dark (defaults to Mocha) |

Each preset overrides the same set of CSS variables on
`<html data-preset="…">`, so switching theme is a single class change.
Selecting `custom` falls back to the Mocha palette as a base.

The choice is stored **in the browser** (Pinia `ui` store, localStorage
key `gosidian.ui`), not in `config.toml`.

> **Note (IMP-054):** the backend and frontend currently track *different*
> preset catalogues — the names above are the ones the SPA actually
> renders; the server-side preset list has diverged. The mismatch is
> tracked as **IMP-054**.

## Language

The language selector lives in **Settings** and offers five languages:

- **IT** (Italian) — complete
- **EN** (English) — reference
- **ES / FR / DE** — scaffolding stubs; topbar strings translated,
  everything else falls back to English

The choice is a pure client-side preference: it persists in
**localStorage** (Pinia `ui` store, key `gosidian.ui`) and switches the
active `vue-i18n` locale in place. There is **no cookie, no `/api/i18n`
endpoint, and no redirect**. On first boot — when no `gosidian.ui` entry
exists yet — the store seeds the locale from the operator's server-side
default `i18n.default_lang`, then falls back to English. MCP clients are
unaffected by this picker.

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

## Two-factor (TOTP)

The **Two-factor** panel lets any user enroll a TOTP authenticator
(scan the QR code, then confirm a code to activate). Owners additionally
get a **global TOTP mode** (`off` / `optional` / `required`) here, while
per-user overrides live in **Admin → Users**. Full policy semantics —
including how `off` acts as a lockout-proof master switch — are in
[Authentication & roles](authentication.md#two-factor-authentication-totp).
