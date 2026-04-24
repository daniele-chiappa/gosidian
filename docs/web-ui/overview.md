# Web UI overview

Routes and capabilities of the built-in HTMX web interface. All pages
are server-rendered — no JavaScript framework, no build step.

## Primary routes

| Route | Purpose |
|---|---|
| `/` | Home (recent + pinned notes) |
| `/notes/<path>` | Read or edit a note |
| `/projects` / `/projects/<name>` | Project listing and dashboard |
| `/tags` / `/tags/<tag>` | Tag explorer (per-project filter) |
| `/graph` | Cytoscape graph of wiki-link relations |
| `/search?q=…` | Full-text search |
| `/settings` | Web UI settings (git sync, theme preset, language) |
| `/admin/tokens` | MCP token management |
| `/admin/users` | User & invite management (owner only) |
| `/audit` | Audit trail (filterable by user, action, date) |
| `/trash` | Soft-deleted notes (if enabled) |

## Rendering stack

- **HTMX** for interactivity (swap-in-place, progressive enhancement)
- **Go `html/template`** for rendering, strictly `map[string]any` data
  (not typed structs) by project convention
- **SQLite FTS5** backs full-text search
- **Cytoscape + fcose layout** for the graph view; all JavaScript
  libraries are vendored under `internal/server/static/js/vendor/` —
  no CDN calls at runtime
- **Lucide** inline SVG icons embedded via `//go:embed` since v1.10 —
  see the icon library entry in the project conventions

## Language switching

The topbar `<select class="lang-switcher">` offers five languages
(IT, EN, ES, FR, DE as of v1.10). Selecting one navigates to
`/api/i18n?lang=<code>&next=<current-path>`, which sets the
`gosidian_lang` cookie for one year and redirects back.

Keys missing from the selected language fall back to English
automatically — see the fallback chain in
[`internal/i18n/i18n.go`](../../internal/i18n/i18n.go). Spanish,
French, and German are v1.10 scaffolding stubs (topbar + common
strings); contributing a complete translation is documented in
[CONTRIBUTING.md](../../CONTRIBUTING.md).

## Web login

See [MCP authentication → Web UI login](../mcp/authentication.md#web-ui-login).
Web auth is optional; without it the UI is open on `0.0.0.0:8080` (put
a reverse proxy in front of anything beyond localhost — see
[Deployment](../deployment.md)).
