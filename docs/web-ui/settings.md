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

## Language

The language selector lives in **Settings** and offers the languages the
operator enables, out of five:

- **IT** (Italian) — complete
- **EN** (English) — reference
- **ES / FR / DE** — about three quarters translated; the missing
  strings (two-factor setup among them) show in English

The choice is a pure client-side preference: it persists in
**localStorage** (Pinia `ui` store, key `gosidian.ui`) and switches the
active `vue-i18n` locale in place. There is **no cookie and no
redirect**. On first boot — when no `gosidian.ui` entry exists yet — the
store seeds the locale from the operator's server-side default
`i18n.default_lang`, then falls back to English. A stored language the
operator later disables falls back to that default too. MCP tool output
is in English.

The owner chooses both under **i18n** in the same page: tick the
languages the selector offers (`i18n.enabled_langs`; all of them when
unset) and pick the default among them (`i18n.default_lang`). A save
applies at once, without a restart; a default left out of the ticked
languages is refused. `GOSIDIAN_I18N_ENABLED_LANGS` and
`GOSIDIAN_I18N_DEFAULT_LANG` set them from the environment instead (see
[configuration](../configuration.md)).

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

Push failures surface in `/healthz` (`git_sync.healthy=false`, plus
`last_error` + `last_error_at` for a request with an owner's token, see
[Health probe](../deployment.md#health-probe)) and as a red state on the metric
`gosidian_gitsync_status` (0=disabled, 1=healthy, 2=degraded).

`last_error` says which side to repair when a repository is corrupt:

- **The vault's own `.git`** — "repository corruption detected — manual
  repair required (run `git fsck --full` in the vault …)".
- **The remote** — "the remote repository is corrupt — the vault here is
  fine …": git rejected the push because the remote could not store the
  objects (`[remote rejected] … (missing necessary objects)`). Repair the
  bare repository on the remote side, or recreate it empty: the next
  push fills it with the whole history.

A push the remote rejects for another reason (a hook declining it, a
protected branch) or because it is ahead (non-fast-forward) shows git's
own message.

Git sync changes apply on the **next server restart**, not
immediately — a boot-time invariant.

## Password

The **Password** panel changes your own password: the current one, the
new one twice (at least 8 characters). Your other web sessions are
signed out; MCP tokens keep working. An account whose password the owner
set (created or reset from **Admin → Users**) gets the same form as a
full-screen step before anything else. LDAP accounts change their
password in the directory. See [Authentication &
roles](authentication.md#passwords).

## Two-factor (TOTP)

The **Two-factor** panel lets any user enroll a TOTP authenticator
(scan the QR code, then confirm a code to activate). The confirmation
hands out **8 single-use recovery codes**, shown once; the panel then
tracks how many are left and can **regenerate** the set on request
(a current TOTP code is asked for). Removing two-factor asks for your
password. Owners additionally get a **global
TOTP mode** (`off` / `optional` / `required`) here, while per-user
overrides — and the **Reset** that clears a locked-out user's second
factor — live in **Admin → Users**. Full policy semantics, including how
`off` acts as a lockout-proof master switch and how to recover a lost
authenticator, are in
[Authentication & roles](authentication.md#two-factor-authentication-totp).

## Project access

**Default visibility for new projects** (owner-only) — `private`,
`internal` or `public`. It applies to projects created from now on (from
the web UI, the API or MCP) and to folders that appear on disk without
settings; each existing project keeps its own visibility, managed from
**Projects**. Fresh installations default to private, upgraded ones to
internal. See [Authentication & roles](authentication.md#project-access-visibility-and-grants)
for how visibility and grants combine.

**Personal project for new accounts** (owner-only, default on) — every
new User account gets a private project named after it where it is
admin. Switch it off to provision nothing; the owner can still create a
personal project for an account from **Admin → Users**.

## My MCP tokens

Shown to every signed-in account except the owner (who uses
**Admin → Tokens**). Create tokens for MCP clients in *inherit* mode
(follows your access as it changes) or *custom* mode (a subset of the
projects you see now), read-only or read + write where you may write,
optionally with the `core` tool profile and an expiry — creating one asks
for your password, since a token outlives the session; revoke them, and
the OAuth logins listed alongside. A token never exceeds your own access:
the server narrows it on every request. See
[Authentication & roles](authentication.md#your-own-mcp-tokens).
