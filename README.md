# gosidian

> **Markdown notes your AI agents can read, write, and reason over — via MCP.**

A self-contained markdown vault with a built-in MCP server. Humans
edit through a web UI, agents talk to it over MCP, everything lives
in plain `.md` files that Obsidian (and every other markdown tool)
reads natively.

<!-- TODO: replace with real 10-15s demo GIF once v1.0.0 ships:
     create a note in the web UI → agent reads via MCP → agent edits
     → UI live-updates. Target ≤5MB, host at docs/demo.gif.
-->

![Screenshot placeholder](docs/screenshot-placeholder.png)

## Quick start

```bash
docker run -d --name gosidian \
  -p 8080:8080 -p 8765:8765 \
  -v "$(pwd)/vault:/vault" \
  ghcr.io/daniele-chiappa/gosidian:latest
# open http://localhost:8080, create admin, copy the MCP token from /admin/tokens
claude mcp add gosidian http://localhost:8765/sse \
  --transport sse --header "Authorization: Bearer $TOKEN"
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
- **An MCP server.** 44 typed tools let agents bootstrap a session,
  search, read, write, link, handoff, self-check, audit. Bearer-token
  authentication with per-project scoping.
- **A web UI.** HTMX, server-rendered, no JavaScript framework. Full-
  text search, backlinks, graph view, editor, audit trail, admin
  pages for tokens and users.

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
- Web UI with editor + live preview + sidebar + search + graph view +
  attachments + audit log + admin pages
- MCP server over HTTP + SSE with 44 typed tools
- Bearer tokens with scopes (`read` / `write`) and per-project
  restriction; cascade-revoke on user disable
- Multi-user web login (owner + members, invite-only, 24h invite TTL)
- Optional git sync (debounced commits, push with token auth)
- SQLite FTS5 full-text search + ETag optimistic locking
- Internationalization (IT + EN complete; ES / FR / DE scaffolding)
- Three theme presets (Midnight Luxury, Light clean, High contrast)
  + custom palette
- Opinionated [Karpathy-Wiki-Stack](docs/vault/conventions.md#karpathy-wiki-stack-project-shape)
  project layout with one-call scaffolding

## Documentation

| Area | Start here |
|---|---|
| **Install + configure** | [Getting started](docs/getting-started.md), [Configuration](docs/configuration.md), [Deployment](docs/deployment.md) |
| **MCP integration** | [Overview](docs/mcp/overview.md), [Tool catalogue](docs/mcp/tools.md), [Authentication](docs/mcp/authentication.md), [Client setup](docs/mcp/client-setup.md), [Agent patterns](docs/mcp/patterns.md) |
| **Web UI** | [Overview](docs/web-ui/overview.md), [Editor](docs/web-ui/editor.md), [Settings](docs/web-ui/settings.md) |
| **Vault** | [Format](docs/vault/format.md), [Conventions](docs/vault/conventions.md), [Multi-project](docs/vault/multi-project.md), [Obsidian compatibility](docs/vault/obsidian-compat.md) |
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
