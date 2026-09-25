# gosidian

> **Markdown notes your AI agents can read, write, and reason over — via MCP.**

A self-contained markdown vault with a built-in MCP server. Humans
edit through a web UI, agents talk to it over MCP, everything lives
in plain `.md` files that Obsidian (and every other markdown tool)
reads natively.

**Listed on** the [official MCP Registry](https://registry.modelcontextprotocol.io/v0/servers?search=gosidian)
as `io.github.daniele-chiappa/gosidian`,
[Glama](https://glama.ai/mcp/servers/daniele-chiappa/gosidian) and
[mcpservers.org](https://mcpservers.org/servers/daniele-chiappa/gosidian)
[![Glama score](https://glama.ai/mcp/servers/daniele-chiappa/gosidian/badges/score.svg)](https://glama.ai/mcp/servers/daniele-chiappa/gosidian)
[![Listed on mcpservers.org](https://mcpservers.org/badge.svg)](https://mcpservers.org/servers/daniele-chiappa/gosidian)

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

## Compared to similar projects

The "markdown vault + agents" space is crowded. This is where gosidian
sits and where it doesn't, as of September 2026:

| If you are looking at… | What those projects do | Where gosidian differs |
|---|---|---|
| **Obsidian MCP bridges** — [mcp-obsidian](https://github.com/MarkusPfundstein/mcp-obsidian), [obsidian-local-rest-api](https://github.com/coddingtonbear/obsidian-local-rest-api), wrappers around the Obsidian CLI | Expose a running Obsidian desktop app to agents over MCP. | Headless server: no Obsidian process needed, runs on a box or in a container, multi-user with roles, per-project tokens, audit trail. The vault stays a plain Obsidian vault. |
| **Markdown memory servers** — [basic-memory](https://github.com/basicmachines-co/basic-memory), [mcp-vault](https://github.com/cyt-666/mcp-vault) | Same "files, not a database" idea; usually add hybrid semantic search, a cloud tier or WebDAV sync. | Ships a full web UI, real multi-user, an agent handoff bus and a server-served working method (versioned directives, lint, stale detection). No semantic search by design ([ADR-007](docs/faq.md#why-not-rag-or-vector-search)), no hosted tier. |
| **Client-side wiki skills** — [obsidian-wiki](https://github.com/Ar9av/obsidian-wiki) and the "LLM Wiki" pattern | Slash-commands the agent runs locally to compile and maintain a wiki. No server, no auth, no UI. | The same pattern implemented server-side and agent-agnostic: one-call scaffold, directives served at bootstrap, handoffs, audit. Complementary: those skills work against a gosidian vault too. |
| **Agent memory services** — [mem0](https://github.com/mem0ai/mem0), [agentmemory](https://github.com/rohitg00/agentmemory), [mcp-memory-service](https://github.com/doobidoo/mcp-memory-service) | Memory as an opaque store: embeddings, recall benchmarks, auto-capture hooks. | Memory is markdown that humans read in Obsidian or the web UI; no embeddings, no LLM calls in the binary, retrieval by identity and graph. No published recall numbers yet, no auto-capture hooks. |
| **Note apps with community MCP servers** — [SilverBullet](https://github.com/silverbulletmd/silverbullet), [Trilium](https://github.com/TriliumNext/Trilium), [SiYuan](https://github.com/siyuan-note/siyuan) | Mature editors, mobile apps, sometimes real-time collaboration; MCP added by third-party servers on top of their API. | MCP-first: the server is in the binary, behind the same login, roles and audit as the UI. No mobile app, no real-time collaboration, no WYSIWYG editor. |

Honest gaps, in the order they come up: semantic / hybrid search
(deferred, see the [FAQ](docs/faq.md#why-not-rag-or-vector-search)),
mobile sync beyond git, real-time collaboration, a hosted offering,
published retrieval benchmarks. The [roadmap](docs/faq.md#whats-the-roadmap)
says which of these are planned.

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
- Opt-in **OAuth 2.1 authorization server** so claude.ai / Claude Desktop
  custom connectors, ChatGPT connectors and Claude Code's browser login
  get their own tokens through a consent screen — no pasted bearer
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
