# Changelog

All notable changes to gosidian are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/); dates are
`YYYY-MM-DD`; versions follow [SemVer](https://semver.org/).

## [1.0.0] — 2026-04-25 — "Initial public release"

First public release of gosidian: a self-hosted, Obsidian-compatible
markdown vault with a first-class MCP server so AI agents get the same
memory you do. Nine internal iterations (v1.0.0 → v1.10.0, ~1 week of
intensive work) stabilised the surface; this release opens the doors.

### Highlights

- **44 MCP tools** covering the full read / write / search /
  discovery / workflow surface — every tool honours ETag optimistic
  locking and token-scoped project filtering.
- **HTMX web UI** (editor, full-text search, graph view, command
  palette, settings, admin) running on the same binary as the MCP
  server.
- **Multi-project vault layout**: `<project>/<note>.md`, with
  cross-project wiki-links, scoped tokens, and a single SQLite FTS5
  index across the whole tree.
- **Agent-first design**: `memory_bootstrap(project)` returns the
  session-start context in one call; ingest patterns (ADR, plan,
  skill, agent, docs) are pre-baked conventions, not free-form.

### Features

**MCP server (44 tools)**

- Core CRUD: `memory_get`, `memory_get_section`, `memory_get_outline`,
  `memory_get_frontmatter`, `memory_create`, `memory_update`,
  `memory_edit`, `memory_append`, `memory_delete`, `memory_batch_get`
- Search & discovery: `memory_search` (cross-project filter),
  `memory_list_notes`, `memory_list_projects`, `memory_list_tags`,
  `memory_notes_by_tag`, `memory_notes_by_importance`, `memory_recent`,
  `memory_backlinks`, `memory_outlinks` (cross-project flag),
  `memory_stale`, `memory_pinned`, `memory_plans`, `memory_skills`
- Workflow: `memory_bootstrap`, `memory_create_handoff`,
  `memory_pending_handoffs`, `memory_compact` (dry-run), `memory_self_stats`,
  `memory_refresh_hot`, `memory_todos`, `memory_lint`, `memory_ask`
- Scaffold: `memory_project_scaffold` (multi-template),
  `memory_list_bootstrap_templates`, `memory_create_project`,
  `memory_delete_project`, `memory_rename_project`, `memory_rename_note`,
  `memory_move_note`
- Attachments: `memory_upload_attachment`, `memory_list_attachments`,
  `memory_delete_attachment`, `memory_attachment_info`
- Audit: `memory_audit_tail`

**Web UI (HTMX)**

- Editor with live preview, full-text search, graph view (Cytoscape
  vendored — no CDN), command palette, theme editor, multi-project
  navigation, settings page.
- Three admin-level theme presets: Midnight Luxury (default), Light
  Clean, High Contrast WCAG-AAA. Custom 5-colour palette supported.
- Language selector in the topbar: IT (complete), EN (reference),
  ES / FR / DE (scaffolding stubs with EN fallback).
- 22 Lucide SVG icons inline, theme-aware via `currentColor`.

**Vault**

- Obsidian-compatible: YAML frontmatter, wiki-links (`[[note]]`,
  `[[project/note|alias]]`), cross-project links.
- Closed tag vocabulary: `type:*`, `status:*`, `topic:*`, `pinned`,
  plus numeric `importance: 1..5`.
- LRU cache for hot-file reads, ETag optimistic locking on all writes,
  synchronous index update after every write.
- Attachments: 6 image types + 7 document types; upload via HTTP or
  MCP base64 / source-path modes.

**Auth & multi-user**

- Web login with `owner` / `member` roles, invite-only signup,
  configurable session TTL + rate-limit window + failure cap.
- MCP bearer tokens scoped by project + scope (`read` / `write` /
  `admin`); token management via web `/admin/tokens`.
- Single-account files auto-migrate to multi-user schema on first
  start.

**Git sync**

- Auto-commit / push of the vault to a Gitea / GitHub remote.
- Graceful fail at boot: if git is unreachable, gosidian starts in
  local-only mode and logs a warning (never fatal).
- Configurable debounce, branch, author, token via env vars.

**Bootstrap templates**

- Three presets ship embedded and are seeded into
  `<vault>/.gosidian/templates/` on first start:
  `karpathy-wiki` (full layout, default), `minimal`, `team`.
- User-editable after seeding; `cp -r` a template, edit, use
  immediately — no rebuild.
- `_template.toml` meta with `name`, `description`, `prompt`,
  `[[variables]]` (required / default / auto=date / auto=project).

**Observability**

- Prometheus metrics on `/metrics` (request counter, latency
  histogram, payload bytes).
- Structured `slog` middleware for MCP calls + HTTP handlers.
- Append-only audit log with per-user filter
  (`memory_audit_tail` tool, `/audit` web page).
- `/healthz` returns `{status, version, vault, notes, git_sync}`.

**Docs**

- `docs/` tree with 19 reference files organised in four areas
  (`mcp/`, `web-ui/`, `vault/`, plus top-level getting-started,
  configuration, deployment, architecture, development, faq).
- 125-line landing README with tagline, 3-command quick start,
  audience blurbs, and a documentation index.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`,
  `PROJECT-STORY.md`, `LICENSE` (MIT).

### Internal

- Single Go binary, multi-stage Docker image (`alpine:3.20` with
  `git` + `ca-certificates`, ~45 MB compressed).
- SQLite via `modernc.org/sqlite` — pure-Go, CGO-disabled, works
  out of the box on Alpine.
- Embedded HTML / CSS / JS + 22 SVG icons via `//go:embed`.
- Full test suite with `-race` detector; tests ship inside the
  repo with `testdata/` fixtures.
- CI pipeline: `go vet` + `go test -race -count=1` + `go build` +
  build & push Docker image to `ghcr.io/daniele-chiappa/gosidian`.

### Notes

- **Public versioning is independent from prior internal versioning.**
  See "Prior internal development" below. v1.0.0 public = snapshot of
  v1.10.0 internal, rebased as "first public release".
- Initial distribution: binary / Docker image on GHCR + source. Docker
  Hub mirroring is reactive (added if concrete demand emerges).
- Zero breaking changes on the MCP tool schemas across the 44 tools —
  SemVer applies from v1.0.0 forward for public consumers.

---

## Prior internal development

gosidian was developed as an internal tool through nine versions before
being published. The internal CHANGELOG is retained below for context
on how the codebase evolved. **Public versioning restarts at v1.0.0**;
the internal `v1.x` line is not part of the public release stream and
does not map onto future public releases one-to-one.

- **v1.10.0** (2026-04-23) — UI polish bundle: Lucide icon set (22
  SVG), three theme presets (Midnight Luxury / Light Clean / High
  Contrast), language selector (IT + EN complete; ES/FR/DE stubs),
  docs split (README 587 → 125 lines, 19 files under `docs/`), removal
  of the "Today" daily-note handler.
- **v1.9.0** (2026-04-22) — Four new MCP tools for agent workflow:
  `memory_todos` (GFM checkbox extraction with plan-status
  enrichment), `memory_lint` (5 baseline rules: broken-wikilink,
  orphan-note, frontmatter-missing, frontmatter-tag-unknown,
  status-incoherent), `memory_ask` (structured OQ-NNN append),
  `memory_search` gained `projects[]` cross-project filter.
- **v1.8.0** (2026-04-22) — Bootstrap templates system.
  `memory_project_scaffold` reads from
  `<vault>/.gosidian/templates/<name>/`; three presets ship embedded
  (`karpathy-wiki`, `minimal`, `team`) and are seeded idempotently.
  New tool `memory_list_bootstrap_templates`; scaffold extended with
  `template` + `variables` parameters.
- **v1.7.2** (2026-04-22) — `SECURITY.md` pointed to a platform-level
  vulnerability reporting flow (GitHub Security Advisories in the
  public version).
- **v1.7.1** (2026-04-22) — Full README rewrite in English with
  installation, configuration, CLI, MCP client wiring, vault layout,
  i18n, development, backup/DR sections. Added `CONTRIBUTING.md`,
  `SECURITY.md`, `CHANGELOG.md`.
- **v1.7.0** (2026-04-22) — Environment-variable coverage extended
  (trash / theme / webauth / vault cache / i18n default). Per-project
  tag filtering. `internal/i18n` package with embedded JSON
  catalogues; `Accept-Language` for MCP clients. `PROJECT-STORY.md`,
  MIT `LICENSE` added.
- **v1.6.0** (2026-04-22) — `memory_project_scaffold` (first
  iteration), `memory_refresh_hot`, `/api/graph?include_cross_project`.
  `auth.Store` hot-reloads `tokens.json` via mtime check — no server
  restart needed after a CLI `token create`. Cytoscape + layout libs
  vendored.
- **v1.5.0** (2026-04-22) — Agent-to-agent handoffs
  (`memory_create_handoff` + `memory_pending_handoffs`).
  `memory_compact` with dry-run. `memory_self_stats` for
  auto-throttling. `memory_outlinks` cross-project flag.
- **v1.4.0** (2026-04-22) — Multi-user web login with owner / member
  roles, invite-only signup, session eviction on user disable.
  `auth.Token.OwnerUserID` with cascade revoke. Per-user `audit.Entry`.
  `auth.json` schema migration v1 → v2 on load.
- **v1.3.0** (2026-04-22) — Frontmatter `importance: 1..5` +
  dedicated `memory_notes_by_importance`. `memory_search` gained
  `include_outline` + `include_frontmatter` flags.
- **v1.2.0** (2026-04-22) — `memory_bootstrap` (single-call
  session-start aggregate). Structured discovery: `memory_plans`,
  `memory_skills`, `memory_pinned`, `memory_stale`. `pinned` tag
  convention.
- **v1.1.0** (2026-04-21) — `/admin/tokens` web page for creating,
  listing, revoking MCP tokens without dropping to the shell.
- **v1.0.0** (internal, 2026-04-21) — First internal tagged release.
  HTTP web UI with HTMX sidebar / editor / search / graph /
  themes; MCP server with 17 tools; ETag optimistic locking; LRU
  vault cache; attachments; audit log; Prometheus metrics;
  structured slog; gitsync with graceful fail; multi-project vault
  layout. SQLite FTS5 as primary datastore.

[1.0.0]: #
