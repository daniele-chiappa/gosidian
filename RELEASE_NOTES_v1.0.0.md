# gosidian v1.0.0 — Initial public release

First public release of **gosidian**: a self-hosted, Obsidian-compatible
markdown vault with a first-class MCP server — so AI agents share the
same memory layer you do.

After nine internal iterations (~1 week of intensive development), the
surface is stable enough to open the doors.

## What is it?

gosidian is one binary that runs:

- An **HTMX web UI** (editor, full-text search, graph view, command
  palette, theme editor, admin pages) — you use it like a lightweight
  Obsidian clone.
- An **MCP server** exposing **44 tools** over SSE — your AI agents
  (Claude Code, Cursor, Aider, Codex, custom ones) use it like a
  memory layer they can read, write, search, and reason over.

Same storage (plain `.md` files with Obsidian-compatible YAML
frontmatter + wiki-links), same multi-project layout, same SQLite FTS5
index across both surfaces.

## Quick start

```bash
docker run -d --name gosidian -p 127.0.0.1:8080:8080 -p 127.0.0.1:8765:8765 \
  -v "$(pwd)/vault":/vault ghcr.io/daniele-chiappa/gosidian:latest
# 1. Open http://localhost:8080, create the admin user, copy the MCP token
# 2. Wire your agent:
claude mcp add gosidian http://localhost:8765/sse --header "Authorization: Bearer $TOKEN"
```

Three commands, no prerequisites. The full getting-started guide lives
in `docs/getting-started.md`.

## Highlights in this release

**Agent-first workflow**

- `memory_bootstrap(project)` — session-start aggregate in one call
  (hot state + README + active plans + skills + recent notes +
  project stats).
- Pre-baked ingest patterns: ADR, plan, skill, agent, docs — the
  conventions are in the template, not in the agent's head.
- Closed tag vocabulary (`type:*`, `status:*`, `topic:*`, `pinned`)
  plus numeric `importance: 1..5`.

**44 MCP tools** covering read, write, edit, search, batch, discovery,
workflow (handoffs, compact, todos, lint, ask, bootstrap, self-stats),
scaffold, attachments, audit. Every tool honours ETag optimistic
locking and token-scoped project filtering.

**HTMX web UI** with editor, search, graph view, command palette, 22
Lucide SVG icons inline (theme-aware), three admin-level theme
presets (Midnight Luxury / Light Clean / High Contrast WCAG-AAA),
five-language selector (IT + EN complete; ES / FR / DE stubs).

**Multi-project vault** with cross-project wiki-links
(`[[project/note]]`), graph view across projects, per-token project
scoping, single SQLite FTS5 index over the whole tree.

**Multi-user** with owner / member roles, invite-only signup,
configurable session TTL / rate-limit / failure cap. MCP bearer
tokens scoped by project + scope (`read` / `write` / `admin`).

**Bootstrap templates** — three presets ship embedded
(`karpathy-wiki`, `minimal`, `team`) and are seeded into
`<vault>/.gosidian/templates/` on first start. Users `cp -r` a
template, edit, use — no rebuild.

**Git sync** auto-commits / pushes the vault to a Gitea / GitHub
remote with configurable debounce. Graceful fail: if git is
unreachable, gosidian starts local-only (never fatal).

**Observability**: Prometheus metrics on `/metrics`, structured slog
middleware, append-only audit log with per-user filter, `/healthz`
endpoint.

**Single-binary deployment**: multi-stage Docker image
(`alpine:3.20` + `git` + `ca-certificates`, ~45 MB compressed),
pure-Go SQLite (no CGO), embedded HTML / CSS / JS / SVG. Deploy,
reverse-proxy for TLS, done.

## Distribution

- **Source**: `github.com/daniele-chiappa/gosidian` — MIT licensed.
- **Container image**: `ghcr.io/daniele-chiappa/gosidian:v1.0.0` and
  `:latest`.
- Docker Hub is intentionally not mirrored on day one; will be added
  if concrete demand emerges.

## Docs

- `README.md` — landing page with tagline, quick start, audience blurbs.
- `docs/getting-started.md`, `docs/configuration.md`,
  `docs/deployment.md`, `docs/architecture.md`, `docs/development.md`,
  `docs/faq.md`.
- `docs/mcp/` — MCP server reference (overview, tools, auth, client
  setup, usage patterns).
- `docs/web-ui/` — web UI reference (overview, editor, settings).
- `docs/vault/` — vault format, conventions, multi-project layout,
  Obsidian compatibility.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`,
  `PROJECT-STORY.md`.

## A note on versioning

The `v1.0.0` on GitHub is **not** a map of any prior internal v1.0.0.
gosidian went through nine internal iterations (v1.0.0 → v1.10.0 over
~1 week) before this first public release. The `CHANGELOG.md` retains
the internal history as a "Prior internal development" section for
context.

Public versioning starts at v1.0.0 and evolves with its own cadence;
it is deliberately disaccoppiato from any private line that may
continue internally.

## Known caveats

- The `ES` / `FR` / `DE` translations are **scaffolding stubs**: only
  the topbar + `common.*` keys are translated; everything else falls
  back to English. Contributions welcome — see
  `CONTRIBUTING.md#translations`.
- Docker Hub is not mirrored (see above).

## Thanks

gosidian builds on years of work across the Obsidian, Go, and MCP
ecosystems — in particular the HTMX / Alpine stack, `modernc.org/sqlite`
(pure-Go SQLite), `mark3labs/mcp-go`, and Lucide icons.

If gosidian helps your flow, the best way to say thanks is to open an
issue with the one thing that's missing or broken — that's what moves
v1.1 forward.
