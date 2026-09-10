# Migration to v2.0 — SPA cutover

gosidian v2.0 retires the HTMX-rendered web UI in favour of a Vue 3
SPA backed by a REST API at `/api/v1/*`. The cutover is **breaking
for operators who relied on legacy HTML or HTMX-partial endpoints**;
end-user URLs (`/notes/<path>`, `/projects/<slug>`, ...) keep
working unchanged because Vue Router runs in history mode and the
shell catch-all returns the SPA for any unmatched path.

## What changed

### Frontend
- The 22 Go HTML templates in `internal/server/templates/`, all
  per-page Go handlers (`internal/server/handlers_*.go`), and the
  ~1KLOC of custom JS under `internal/server/static/{js,css,icons}/`
  are removed. Vite-fingerprinted assets live under
  `/static/dist/*`.
- Vue 3 + Pinia + vue-router 4 + Tailwind 3.4. CodeMirror 6 powers
  the editor (markdown, fold, multi-cursor, wikilink autocomplete,
  paste-upload). Cytoscape 3.30 + fcose powers `/graph`.
- Strict CSP attached to the SPA shell: `script-src 'self'`, no
  `unsafe-inline` / `unsafe-eval`. i18n messages are AOT-compiled at
  build time (`@intlify/unplugin-vue-i18n`) so vue-i18n's runtime
  message compiler never touches `new Function()`.

### Backend / API surface

| v1.x route | v2.0 replacement |
|---|---|
| `GET /` HTML home | `GET /` SPA shell |
| `GET /login` HTML form | `GET /login` SPA route + `POST /api/v1/login` JSON |
| `GET /signup` HTML form | `POST /api/v1/signup` JSON |
| `GET /logout` redirect | `POST /api/v1/logout` JSON |
| `GET /notes/<path>` HTML | `GET /notes/<path>` SPA route + `GET /api/v1/notes/<path>` JSON |
| `GET /projects` HTML | `GET /projects` SPA route + `GET /api/v1/projects` JSON |
| `GET /tags` HTML | `GET /tags` SPA route + `GET /api/v1/tags` JSON |
| `GET /search?q=` HTML | `GET /search?q=` SPA route + `GET /api/v1/search` JSON |
| `GET /graph` HTML | `GET /graph` SPA route + `GET /api/v1/graph` JSON |
| `GET /settings` HTML | `GET /settings` SPA route + `GET/PUT /api/v1/settings` JSON |
| `GET /admin/{tokens,users,...}` HTML | `GET /admin/*` SPA route + `GET/POST/DELETE /api/v1/admin/*` JSON |
| `GET /trash` HTML | `GET /trash` SPA route + `GET/POST/DELETE /api/v1/trash[/<id>[/restore]]` JSON |
| `GET /audit` HTML | `GET /admin/audit` SPA route + `GET /api/v1/admin/audit` JSON |
| `POST /api/preview` HTMX | `POST /api/v1/preview` JSON |
| `GET /api/tree` HTMX | `GET /api/v1/tree` JSON |
| `GET /api/backlinks` HTMX | `GET /api/v1/notes/<path>/backlinks` JSON |
| `GET /api/note-excerpt` HTMX | `GET /api/v1/notes/<path>/excerpt` JSON |
| `GET /api/command-palette` HTMX | `GET /api/v1/command-palette` JSON |
| `POST /api/attach` HTMX | `POST /api/v1/attach` JSON |
| `POST /api/upload` HTMX | `POST /api/v1/upload` JSON |
| `GET /api/i18n` HTMX | `GET /api/v1/i18n` JSON |
| `GET /api/download-vault` HTML | *deferred to v2.x — re-add as `/api/v1/admin/download-vault` if needed* |

### Auth
- Cookie-session auth (`gosidian_session`) is gone. The SPA uses
  Bearer tokens (`gsp_<base64url>`) returned by `POST /api/v1/login`
  and persisted in `localStorage` under `gosidian.auth`.
- MCP token store remains separate (`<state-dir>/tokens.json`, default `<vault>/.gosidian/`) and
  unchanged for agents.
- The `GOSIDIAN_SPA_MODE` env flag — used during the v2-spa
  development branch as a feature gate — has been removed; the
  SPA is the only frontend now. Operators who set the flag
  explicitly can drop it from their env files.

### CSP
The SPA shell ships with a strict CSP attached at the response
header level (see `internal/api/v1/security.go`):

```
default-src 'self';
script-src 'self';
style-src 'self' 'unsafe-inline';
img-src 'self' data: blob:;
font-src 'self';
connect-src 'self';
worker-src 'self' blob:;
frame-ancestors 'none';
form-action 'self';
base-uri 'self';
object-src 'none';
```

`unsafe-inline` on `style-src` is required for Tailwind utility
runtime + Reka UI's scoped style injection — accepted trade-off.
No `unsafe-eval` anywhere; runtime `new Function()` users (notably
vue-i18n's message compiler) are precompiled at build time.

## Upgrade path

Deployments using the official Docker image (`docker-compose pull`
or pinning a tag) get the new build automatically.

```sh
docker compose pull
docker compose up -d
```

Bookmarks pointing at `/notes/<path>`, `/projects/<slug>`, etc. keep
working. The first request after upgrade re-prompts users to log
in (the cookie session is gone). Credentials persist; only the
session shape changed.

External integrations that scrape HTML or call legacy `/api/*`
HTMX partials must migrate to `/api/v1/*` JSON. The OpenAPI 3.1
spec lives at `internal/api/v1/spec/openapi.yaml`.

## Downgrade path

Pinning the Docker tag to the last v1.x release (e.g. `v1.12.0`)
restores the legacy behaviour. The vault on disk is forward- and
backward-compatible: notes, attachments, the SQLite index, and
git-sync metadata are unchanged across the cutover.

```sh
# In your docker-compose.yml
image: gosidian:v1.12.0   # or ghcr.io/daniele-chiappa/gosidian:v1.12.0
```

The webauth `accounts.json` file is also unchanged; existing users
keep their hashed passwords across the rollback.

## Build (developers)

The Dockerfile is now 3-stage. Out of the box:

```sh
docker build -f src/Dockerfile -t gosidian:v2.0 ./src
```

For local dev without Docker, run two terminals:

```sh
# Terminal 1: Go server
cd src && go run ./cmd/gosidian --vault ./vault --addr :8080

# Terminal 2: Vue dev server with HMR
cd src/web && npm run dev
# Navigate to http://localhost:5173 (Vite proxies /api/* and
# /static/dist/* to the Go server on :8080)
```

The Vite dev server bypasses the embedded `dist/` for fast
iteration; it also reloads i18n catalogues on save.

## What's deferred

- `GET /api/download-vault` (whole-vault zip): not in v2.0 — the
  legacy handler lived in the cookie-session world. Re-add as
  `GET /api/v1/admin/download-vault` (owner-only) when the use
  case lands.
- Per-project zip download (was the v1.13 sidebar icon): same as
  above, re-add under `/api/v1/projects/<slug>/download` when
  needed.
- Playwright wider E2E (note CRUD / conflict / SSE / search /
  graph): the Phase 7.x canary (`tests/e2e/canary.spec.ts`)
  guards the BUG-009 class today; the suite grows from there.
- Custom theme editor (per-token override via
  `[data-preset="custom"]`): the preset is wired but the inline
  editor is a follow-up.
