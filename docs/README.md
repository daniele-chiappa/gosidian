# gosidian documentation

Technical reference for gosidian. For a product overview and 30-second
trial, read the [root README](../README.md) first.

## Getting started

- [Getting started](getting-started.md) — install from source or Docker,
  first-run checklist
- [Configuration](configuration.md) — environment variables, TOML file,
  CLI flags
- [Deployment](deployment.md) — Docker Compose, reverse proxy, backup
  & disaster recovery

## MCP server

- [Overview](mcp/overview.md) — what MCP is, why gosidian uses it
- [Tool catalogue](mcp/tools.md) — all 44 typed tools, grouped by purpose
- [Authentication](mcp/authentication.md) — bearer tokens, scopes,
  rotation
- [Client setup](mcp/client-setup.md) — Claude Code, Zed, Cursor,
  Continue, custom clients
- [Agent patterns](mcp/patterns.md) — typical session flow, bootstrap →
  discover → read → write → self-check

## Web UI

- [Overview](web-ui/overview.md) — routes, web login, admin pages
- [Editor](web-ui/editor.md) — markdown editor, live preview, HTMX
- [Settings](web-ui/settings.md) — theme presets, language selector,
  git sync config

## Vault

- [Format](vault/format.md) — directory layout, `.gosidian/`
  machine-owned files, markdown conventions
- [Conventions](vault/conventions.md) — tag vocabulary, frontmatter,
  `importance`, `pinned`
- [Multi-project layout](vault/multi-project.md) — top-level folders
  as projects, cross-project links
- [Obsidian compatibility](vault/obsidian-compat.md) — what's fully
  compatible, what degrades, what's ignored

## Internals

- [Architecture](architecture.md) — package layout, data flow, ADRs
- [Development](development.md) — build, test, release cadence

## FAQ

- [FAQ](faq.md) — "why not Obsidian Sync?", "why not Notion?", "why
  structured retrieval over RAG?", …

---

All documentation is Markdown. Render locally with `mkdocs serve` or
just read the files directly — they are the authoritative source.
