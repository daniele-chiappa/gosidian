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
- [Tool catalogue](mcp/tools.md) — all 63 typed tools, grouped by purpose
- [Authentication](mcp/authentication.md) — bearer tokens, scopes,
  rotation
- [Self-improvement loop](mcp/self-improvement.md) — *experimental, off
  by default*: agents record usage-friction insights; opt-in, private-first
- [Client setup](mcp/client-setup.md) — Claude Code, Zed, Cursor,
  Continue, custom clients
- [Agent patterns](mcp/patterns.md) — typical session flow, bootstrap →
  discover → read → write → self-check; handoff lifecycle + change feed
- [Agent anchors](mcp/agent-anchors.md) — surface vault-defined agent
  roles as thin referenced files in the harness working dir (opt-in)
- [Upload flow](mcp/upload.md) — REST `/api/upload` + the two MCP
  upload tools, contract and decision tree; the HTTP `/download` twin
  for fetching a note's raw bytes with the MCP token
- [Local read-only mirror](mcp/mirror.md) — `gosidian mirror sync`: a
  read-only copy of one project next to an agent, opt-in per project

## Web UI

- [Overview](web-ui/overview.md) — the Vue 3 SPA, the **plancia** window
  manager, deep-link routes, web login
- [Editor](web-ui/editor.md) — markdown editor, live preview, CodeMirror 6
- [Authentication & roles](web-ui/authentication.md) — Admin/User/Read-only
  roles, per-project visibility and grants (accounts and teams),
  restricted accounts and personal projects, self-service MCP tokens,
  TOTP two-factor, LDAP / Active Directory
- [Settings](web-ui/settings.md) — theme presets, language selector,
  git sync config

## Vault

- [Format](vault/format.md) — directory layout, `.gosidian/`
  machine-owned files, markdown conventions
- [Conventions](vault/conventions.md) — tag vocabulary, frontmatter,
  `importance`, `pinned`
- [Multi-project layout](vault/multi-project.md) — top-level folders
  as projects, cross-project links
- [Global projects](vault/global-projects.md) — shared skills, agents
  & scaffold templates that any project can opt into
- [Database notes](vault/databases.md) — one note per entry with a
  schema: backlogs and lists that `memory_query` can filter
- [Views](vault/views.md) — `view` blocks: notes that list other notes
- [Graph views](vault/graphs.md) — `graph` blocks: saved views of the graph, the plugins' graphs among them
  by folder and frontmatter, computed when the note is read
- [Pipelines](pipelines.md) — notes `type: pipeline` the server runs
  when a document arrives, a service calls or the clock strikes (opt-in)
- [The IN folder](in.md) — a project's files to work on, under their own
  names and in their own tree, which the pipelines take in batches
- [Diagrams](diagrams.md) — ```` ```diagram ```` blocks of YAML: flows,
  trees, mind maps, fishbones, Venn, matrices and Gantt charts, checked
  by the server and read by agents as text
- [Obsidian compatibility](vault/obsidian-compat.md) — what's fully
  compatible, what degrades, what's ignored

## Internals

- [Architecture](architecture.md) — package layout, data flow, ADRs
- [Benchmark](benchmark.md) — how well agents find notes: retrieval figures on a public test vault, method, how to rerun
- [Development](development.md) — build, test, release cadence

## FAQ

- [FAQ](faq.md) — "why not Obsidian Sync?", "why not Notion?", "why
  structured retrieval over RAG?", …

---

All documentation is Markdown. Render locally with `mkdocs serve` or
just read the files directly — they are the authoritative source.
