# Architecture

Package layout, data flow, ADRs. For the "why we built gosidian this
way" narrative, see [PROJECT-STORY.md](../PROJECT-STORY.md).

## Package layout

```
cmd/gosidian/        # entry point + CLI subcommands
internal/vault/      # file access + LRU cache + fsnotify
internal/index/      # SQLite FTS5 + backlinks + tags
internal/parser/     # goldmark + wiki-link / tag extraction
internal/server/     # HTTP router, embedded Vue SPA shell
internal/api/v1/     # REST JSON handlers (/api/v1/*) + CSP/security
internal/authz/      # RBAC predicate (fail-closed)
internal/mcp/        # MCP tools + SSE server (server.go / tools.go)
internal/auth/       # MCP bearer tokens
internal/webauth/    # web login + invite + sessions
internal/audit/      # append-only audit log
internal/gitsync/    # debounced commit/push
internal/i18n/       # embedded translation catalogues
internal/scaffold/   # bootstrap template seeder
internal/lint/       # vault hygiene rules
internal/insights/   # self-improve digest builder
internal/config/     # TOML loader + env overrides
```

The web UI source lives outside `internal/` under `src/web/` (a Vue 3 +
Vite SPA); `npm run build` emits a fingerprinted bundle that the Go
binary embeds via `//go:embed` and serves under a strict CSP. See
[Web UI overview](web-ui/overview.md).

## Read path

A `memory_search` call crosses the following layers:

1. **MCP transport** (`internal/mcp`, `server.go` / `tools.go`)
   validates the token and dispatches by tool name.
2. **Tool handler** (`internal/mcp/tools_*.go`) parses arguments,
   resolves the project scope, applies the scope intersection for
   `projects[]` params, and calls…
3. **Index** (`internal/index`) — FTS5 over SQLite.
4. **Vault** (`internal/vault`) — reads the concrete markdown file
   through the LRU cache (128 entries by default; mtime-validated).
5. **Parser** (`internal/parser`) — goldmark with wiki-link + tag +
   frontmatter extractors when full-body enrichment is requested.
6. Response serialised with an `etag` header for optimistic locking.

## Write path

1. Tool handler validates input + checks `if_match` ETag if provided
   (returns a dedicated error on mismatch).
2. `vault.Save` writes atomically (temp file + rename).
3. Synchronously: LRU invalidated, SQLite index upserted in the same
   request, audit log appended.
4. fsnotify is a **fallback** for external writes; the write path
   itself never waits on it.

## Key invariants

- **Filesystem is source of truth.** The SQLite index is rebuildable.
- **Writes are atomic + synchronous.** A successful write guarantees
  the next read (local or via MCP) sees the new content.
- **ETag optimistic locking is uniform.** Every write tool accepts
  `if_match`; concurrent writers reload on mismatch.
- **Scoped tokens intersect, never expand.** A `--project foo` token
  asking for `projects=["bar"]` gets an empty result, never a scope
  violation.
- **Closed tag vocabulary.** `memory_lint` enforces it.

## Beyond markdown

- **HTML notes** (ADR-011, opt-in via `[vault] html_notes`). When
  enabled, single-file `.html` notes are first-class: enumerated,
  indexed, and rendered inside a sandboxed `srcdoc` iframe
  (`sandbox="allow-scripts"` **without** `allow-same-origin`), so a
  note's scripts can never touch the host origin, cookies, or the
  REST API. Default off.
- **Media & table notes** (ADR-013 / ADR-016, opt-in via
  `[vault] media_notes` / `table_notes`). An image or a CSV becomes a
  first-class note as a plain `.md` whose frontmatter declares
  `type: image` / `type: table` plus a `media:` pointer to the
  attachment; the body is the searchable caption. The SPA renders the
  image, or the CSV as a paginated table. Default off.
- **Graph analytics.** Backlinks/outlinks feed two MCP tools beyond
  plain link listing: `memory_hubs` ranks the most-connected notes,
  and `memory_path` finds the shortest wiki-link path between two
  notes — both computed over the index, not the filesystem.

## ADRs

Architectural decision records live in `<project>/memory/decisions.md`
in each gosidian vault (they describe the working project, not the
codebase itself). When a fresh vault is created with
`memory_project_scaffold`, a seed `decisions.md` is dropped in from
the chosen bootstrap template under
`<vault>/.gosidian/templates/<name>/memory/`. The most load-bearing
ADRs in the reference implementation:

- **ADR-002** — The vault is the source of truth. gosidian is a
  layer; deleting `.gosidian/` leaves a pure markdown vault (zero
  lock-in).
- **ADR-003** — Final Docker stage is `alpine + git`, not distroless.
  Git is required at runtime for `gitsync` to shell out.
- **ADR-004** — SQLite is the primary datastore. Postgres is not
  planned; trigger points for re-evaluation are documented.
- **ADR-005** — NPM custom config bind mount must be `rw` (for the
  Docker Compose reverse-proxy setup).
- **ADR-006** — Embedding choice for any future semantic search:
  sqlite-vec, not external vector DB. Deferred by ADR-007.
- **ADR-007** — Semantic search deferred sine die. Structured
  retrieval beats fuzzy similarity for agent-first memory. Re-open
  triggers documented explicitly.
- **ADR-011** — Single-file `.html` notes are a first-class note type
  (opt-in). They render in a sandboxed `srcdoc` iframe with
  `allow-scripts` but no `allow-same-origin`, isolating note scripts
  from the host origin. Default off.

Read the project's own `memory/decisions.md` (in a running gosidian
instance) for the authoritative current list.

## Concurrency model

- **Single-process** by design. Multiple gosidian processes pointing
  at the same vault is unsupported (SQLite write contention + fsnotify
  duplication).
- **Goroutines**: one for the web HTTP server, one for the MCP SSE
  server, one for gitsync's debounce timer, one for fsnotify's event
  loop. Everything else is per-request.
- **Rate limiting**: a write limiter per MCP session (`60/minute`
  default, rolling window), with five times that shared by all the
  sessions of a token, protects against runaway agent loops without
  letting agents on one token take each other's budget.
