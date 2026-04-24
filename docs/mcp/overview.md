# MCP overview

gosidian ships an MCP (Model Context Protocol) server alongside the
web UI. The same vault is visible to humans and agents, over the same
source of truth — plain markdown files on disk.

## What MCP is

MCP is an open protocol for connecting AI agents to tools, data, and
memory. Clients (Claude Code, Zed, Cursor, Continue, custom agents)
connect to MCP servers and call typed tools. gosidian implements the
**server** side.

Transport: **HTTP + SSE** at `/sse` by default.
Auth: **Bearer tokens** with per-project scoping.

## Why typed retrieval

gosidian exposes 44 typed tools (`memory_bootstrap`, `memory_search`,
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

- [Tool catalogue](tools.md) — all 44 tools grouped by purpose
- [Authentication](authentication.md) — creating and scoping tokens
- [Client setup](client-setup.md) — wiring Claude Code, Zed, Cursor,
  Continue, or a custom client
- [Agent patterns](patterns.md) — the canonical bootstrap → discover →
  read → write → self-check loop
