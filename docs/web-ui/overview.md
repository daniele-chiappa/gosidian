# Web UI overview

The built-in web interface is a **Vue 3 single-page app**, built with
Vite and embedded in the binary via `go:embed` — no separate web server,
no CDN. It talks to the Go backend over a JSON REST API (`/api/v1/*`)
and an SSE stream for live updates.

## The plancia (window manager)

Since v2.3 the UI is a **"plancia"**: a niri-style scrollable tiling
window manager. Instead of one page at a time, notes, the graph, search,
and config forms open as **windows** side by side in a horizontally-
scrollable strip.

- **Open** a note from the sidebar tree, a wikilink, search, or the
  command palette (`⌘K` / `Ctrl-K`); each opens to the right of the
  focused window.
- **Resize** in discrete steps (small → medium → full) with the window's
  resize button.
- **Minimize** a window to a horizontally-scrollable footer; click to
  restore.
- **Direct links**: the window's link button opens an *ego-graph* — the
  one-hop neighbourhood of that note — as its own window.
- **Edit in place**: a note window has a View/Edit toggle; the editor
  mounts lazily in the same window (hidden for read-only users).
- **Navigate** focus between windows with `Alt-←` / `Alt-→`.

The open windows + focus are encoded in the URL (`?w=…&f=…`), so a
workspace is shareable and survives reload; when the URL is empty the
last workspace is restored from `localStorage`.

## Deep-link routes

These canonical routes still work as entry points — visiting one opens
the matching window in the plancia (so existing links and bookmarks keep
working):

| Route | Opens |
|---|---|
| `/notes/<path>` | the note (view; `…/edit` opens in edit mode) |
| `/notes/<path>/history` | the note's git history |
| `/projects` | project listing & admin |
| `/tags` / `/tags/<tag>` | tag explorer |
| `/graph` | Cytoscape graph of wiki-link relations |
| `/search` | full-text search |
| `/query` | notes by their frontmatter (see below) |
| `/settings` | theme preset, language, git sync |
| `/admin/*` | users, tokens, invites, audit (owner only) |
| `/trash` | soft-deleted notes (if enabled) |

## Query window

**Menu → Query** selects notes by their frontmatter — the same query as
the MCP `memory_query` tool, for people. Each row is a condition
`field · operator · value` (`eq`, `ne`, `in` with a comma-separated list,
`exists`, `lt`/`lte`/`gt`/`gte`, `contains`), and every condition must
hold. A namespaced tag counts as a field when the note has none
(`status:done` → `status = done`), `tags` is the tag list, ISO dates and
numbers compare as such. Optional: a project, a sort field with its
order, the fields to show (by default those of the conditions). Results
are a table; a title opens the note. The whole query lives in the URL,
so it survives a reload and can be shared as a link. The backend is
`POST /api/v1/query`, scoped to the projects the account can read.

## Rendering stack

- **Vue 3** (composition API) + **vue-router** + **Pinia** state
- **Vite** build; output embedded under `internal/server/web/dist`
- **Tailwind CSS** with a semantic-token theme system (presets +
  custom palette)
- **CodeMirror 6** for the editor (markdown, wikilink autocomplete),
  lazy-loaded
- **Cytoscape + fcose layout** for the graph view, lazy-loaded
- **vue-i18n** with catalogues precompiled at build time from
  `internal/i18n/catalogs/` (shared with the Go side); a strict CSP
  (`script-src 'self'`, no eval) is enforced
- The Go backend serves only the SPA shell, fingerprinted assets, the
  `/api/v1/*` REST API, and the MCP transports at `/mcp` and `/mcp/sse`

## Language switching

The language selector lives in **Settings**. On first load the SPA reads
the operator's configured `i18n.default_lang`; the user's later choice is
persisted client-side and wins thereafter. Keys missing from the selected
language fall back to English automatically — see
[`internal/i18n/i18n.go`](../../internal/i18n/i18n.go). Spanish, French,
and German are scaffolding stubs; contributing a complete translation is
documented in [CONTRIBUTING.md](../../CONTRIBUTING.md). Agents pick their
language via the `Accept-Language` header on the MCP transport.

## Web login

See [MCP authentication → Web UI login](../mcp/authentication.md#web-ui-login).
Web auth is optional; without it the UI is open on `0.0.0.0:8080` (put
a reverse proxy in front of anything beyond localhost — see
[Deployment](../deployment.md)).
