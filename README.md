# gosidian

> **Markdown notes your AI agents can read, write, and reason over — via MCP.**

A self-contained markdown vault with a built-in MCP server. Humans
edit through a web UI, agents talk to it over MCP, everything lives
in plain `.md` files that Obsidian (and every other markdown tool)
reads natively.

![gosidian in action](docs/demo.gif)

## Try it in your browser

[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://codespaces.new/daniele-chiappa/gosidian)

Launch a free, throwaway gosidian in
[GitHub Codespaces](https://github.com/features/codespaces) — no install,
running on your own Codespaces quota. It builds from source, seeds a small
demo vault, and opens the web UI. Log in with **`demo`** /
**`gosidian-demo`**.

## Quick start

```bash
docker run -d --name gosidian \
  -p 8080:8080 \
  -v "$(pwd)/vault:/vault" \
  ghcr.io/daniele-chiappa/gosidian:latest
# open http://localhost:8080, create admin, copy the MCP token from /admin/tokens
claude mcp add gosidian http://localhost:8080/mcp \
  --transport http --header "Authorization: Bearer $TOKEN"
```

Three commands: Docker up → token created from the web UI → agent
wired. Your `.md` vault is persisted under `./vault/`; stop the
container and the files are still there.

Other installation paths (source, custom compose, bare-metal):
[docs/getting-started.md](docs/getting-started.md).

## What is gosidian

- **A markdown vault.** Notes are `.md` files on disk. Open the same
  folder in Obsidian, VS Code, `vim`, or any editor you already use.
  Zero lock-in: delete `.gosidian/` and you have a pure Obsidian
  vault.
- **An MCP server.** 57 typed tools let agents bootstrap a session, ingest files,
  search, read, write, link, handoff, self-check, audit. Bearer-token
  authentication with per-project scoping.
- **A web UI.** A Vue 3 single-page app served from the same binary
  (built with Vite, embedded via `go:embed`). Notes, graph, search and
  config forms open as windows in a tiling "plancia" workspace —
  full-text search, backlinks, graph view, editor with live preview,
  audit trail, admin pages for tokens and users.

All three views hit the **same files on disk**. The SQLite FTS5 index
is a cache — drop it and it rebuilds.

## Who it's for

- **AI engineers** wiring agents that need persistent structured
  memory: note-taking, plans, skills, ADRs, handoffs, audit.
- **Obsidian users** who want a programmable layer on top of a vault
  they already trust.
- **Teams** with shared vault + per-project scoped tokens per agent.

## Why gosidian instead of X

- **vs RAG / vector search**: gosidian retrieves by *identity* (path,
  tag, frontmatter, backlinks) — more predictable than similarity
  search for an agent's working memory. Semantic search is
  deliberately deferred: see [ADR-007 rationale](docs/faq.md#why-not-rag-or-vector-search).
- **vs Obsidian Sync**: Sync mirrors a vault between human devices.
  gosidian adds a typed automation surface (MCP) to the same vault.
  Not competitive — complementary.
- **vs Notion / Roam**: hosted or proprietary formats; migration is
  a project. gosidian's vault is already `.md` files you can take
  anywhere.

[FAQ](docs/faq.md) covers the long form.

## Feature highlights

- Single binary, ≤50 MB, Alpine-based Docker image
- Web UI: a Vue 3 SPA (Vite, Pinia, Tailwind, CodeMirror, Cytoscape),
  embedded in the binary — editor + live preview, sidebar, search,
  graph view, attachments, audit log, admin pages
- **Plancia** tiling window manager (niri-style): notes, graph, search
  and config forms open as resizable, side-by-side windows in a
  horizontally-scrollable workspace, restorable from the URL
- MCP server over Streamable HTTP (legacy HTTP+SSE kept) with 57 typed tools
- Bearer tokens with scopes (`read` / `write`) and per-project
  restriction — including multi-project tokens for orchestrators;
  cascade-revoke on user disable
- **Agent orchestration bus**: handoff notes with an atomic
  claim/complete lifecycle, server-stamped identity, and a
  `memory_wait_changes` long-poll change feed — a minimal multi-agent
  task queue where everything stays plain markdown
- Multi-user web login with **role-based access** (owner / member /
  guest), per-project public/private visibility, and invite-only signup
  (24h TTL)
- Optional **TOTP two-factor** (global mode + per-user override) and
  **LDAP / Active Directory** login with guest auto-provisioning
- Optional git sync (debounced commits, push with token auth)
- SQLite FTS5 full-text search + ETag optimistic locking
- First-class `.html` notes, rendered in a sandboxed iframe (off by
  default, opt-in per project)
- Graph analytics over the wikilink graph: `memory_hubs` (most-linked
  notes) and `memory_path` (shortest path between two notes)
- Opt-in **self-improve loop**: agents record usage-friction insights
  per token, off by default
- Print / Save-as-PDF for any markdown note straight from the web UI
- Internationalization (IT + EN complete; ES / FR / DE scaffolding)
- Light & dark theme presets (Catppuccin, Tokyo Night, Solarized) +
  custom palette
- Opinionated [Karpathy-Wiki-Stack](docs/vault/conventions.md#karpathy-wiki-stack-project-shape)
  project layout with one-call scaffolding
- Optional [global projects](docs/vault/global-projects.md) for skills,
  agents & scaffold templates shared across projects (opt-in per
  project, local-overrides-global)

## Documentation

| Area | Start here |
|---|---|
| **Install + configure** | [Getting started](docs/getting-started.md), [Configuration](docs/configuration.md), [Deployment](docs/deployment.md) |
| **MCP integration** | [Overview](docs/mcp/overview.md), [Tool catalogue](docs/mcp/tools.md), [Authentication](docs/mcp/authentication.md), [Client setup](docs/mcp/client-setup.md), [Agent patterns](docs/mcp/patterns.md) |
| **Web UI** | [Overview](docs/web-ui/overview.md), [Editor](docs/web-ui/editor.md), [Authentication & roles](docs/web-ui/authentication.md), [Settings](docs/web-ui/settings.md) |
| **Vault** | [Format](docs/vault/format.md), [Conventions](docs/vault/conventions.md), [Multi-project](docs/vault/multi-project.md), [Global projects](docs/vault/global-projects.md), [Obsidian compatibility](docs/vault/obsidian-compat.md) |
| **Internals** | [Architecture](docs/architecture.md), [Development](docs/development.md) |
| **Common questions** | [FAQ](docs/faq.md) |

Full index: [docs/README.md](docs/README.md).

## Contributing

Contributions welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the
workflow, test expectations, and translation guidelines.

## Security

Security issues are reported privately. See [SECURITY.md](SECURITY.md)
for the disclosure process.

## License

Released under the [MIT License](LICENSE).

## See also

- [PROJECT-STORY.md](PROJECT-STORY.md) — project genesis, design
  philosophy, and a comparison with Obsidian / Logseq / RAG-based
  knowledge stacks.
- [CHANGELOG.md](CHANGELOG.md) — release history.
- [Design philosophy & project genesis](PROJECT-STORY.md).
