# MCP overview

gosidian ships an MCP (Model Context Protocol) server alongside the
web UI. The same vault is visible to humans and agents, over the same
source of truth — plain markdown files on disk.

## What MCP is

MCP is an open protocol for connecting AI agents to tools, data, and
memory. Clients (Claude Code, Zed, Cursor, Continue, custom agents)
connect to MCP servers and call typed tools. gosidian implements the
**server** side.

Transport: **Streamable HTTP** at `/mcp` on the web port (single-port
mode, recommended — the current MCP transport, `--transport http` in
Claude Code). The legacy **HTTP + SSE** transport stays available at
`/mcp/sse` for older clients, with no removal date; a legacy standalone
SSE listener on a separate port is opt-in via `--mcp-addr` /
`GOSIDIAN_MCP_ADDR`. gosidian sends nothing server→client (change
events travel inside `memory_wait_changes`), so `GET /mcp` answers 405
and no long-lived stream sits behind your reverse proxy.
Auth: **Bearer tokens** with per-project scoping.

## Why typed retrieval

gosidian exposes 57 typed tools (`memory_bootstrap`, `memory_search`,
`memory_plans`, `memory_lint`, …) that retrieve notes by **identity**:
path, tag, frontmatter, backlinks. It does **not** ship vector
embeddings or fuzzy semantic search — that's a deliberate design
choice, documented in ADR-007.

For an agent's working memory, structured retrieval is more
predictable than similarity search. The agent knows what it wrote
(and where); it should be able to read it back without a recall
heuristic. See [FAQ: why not RAG?](../faq.md#why-not-rag-or-vector-search)
for the full rationale.

## Where to go next

- [Tool catalogue](tools.md) — all 57 tools grouped by purpose
- [Authentication](authentication.md) — creating and scoping tokens
- [Client setup](client-setup.md) — wiring Claude Code, Zed, Cursor,
  Continue, or a custom client
- [Agent patterns](patterns.md) — the canonical bootstrap → discover →
  read → write → self-check loop
