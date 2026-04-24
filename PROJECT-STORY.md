# Project story

## Why gosidian exists

Gosidian started as a very specific frustration: **AI agents that work on
real software need persistent memory, and the tooling to give them that
memory is either half-baked or pulls in the wrong direction**.

The industry default for "give the LLM context" is RAG over a knowledge
base — embed every document, retrieve the most-similar chunks at query
time, feed them into the prompt. For documentation-style content RAG is
fine. For an **agent's working memory** it is often actively harmful: the
retrieval returns notes that are *topically similar* rather than *scoped to
the current task*, and an LLM handed similar-but-irrelevant context tends
to hallucinate more, not less. The agent drifts.

What an agent actually needs is the opposite: **explicit, typed, retrieval
by identity**. "Give me the plans whose `status` is `in-progress` for
project X." "Read hot.md." "List skills whose trigger phrase matches
'rebuild'." No similarity; no ambiguity; no magic. Just tools that encode
what the author meant.

That is the architecture gosidian commits to: the agent's memory is a
directory of markdown notes with a small convention on top (frontmatter
with `type`, `status`, `tags`, `importance`), and the server exposes typed
MCP tools for *exactly* those fields.

## What gosidian is (and isn't)

**Is**: a self-hosted personal knowledge base that doubles as
agent-workable memory. One binary, one vault folder, one SQLite index.
Users edit notes via a web UI (HTMX, no JavaScript framework) or any
external editor (Obsidian, vim, VS Code — the vault is just plain markdown
files with YAML frontmatter). Agents connect over MCP via HTTP + SSE with
bearer tokens; each token can be scoped to one project and to read/write
independently.

**Isn't**: a RAG system, a LangChain replacement, a vector database. Full-
text search is FTS5. Structured discovery is a set of typed tools
(`memory_plans`, `memory_skills`, `memory_notes_by_tag`,
`memory_notes_by_importance`, `memory_pinned`, `memory_stale`,
`memory_backlinks`, …). The architecture explicitly rejects embedding-based
retrieval for the agent's own memory — the trade-off is documented in
ADR-007 of the project's design log.

## The Karpathy-Wiki-Stack pattern

The organisational pattern inside the vault is **Karpathy-Wiki-Stack**,
inspired by Andrej Karpathy's musings on personal wikis and by the
[`Ar9av/obsidian-wiki`](https://github.com/Ar9av/obsidian-wiki) repo layout.
Each project lives under one top-level folder and contains:

- `hot.md` — session cache (current focus, active plans, recent decisions)
- `README.md` — project index / landing page
- `log.md` — append-only activity log
- `memory/` — stable knowledge (architecture, ADRs, conventions, glossary,
  environments)
- `agents/` — optional role descriptors for specialised AI agents
- `plans/` — one file per non-trivial task, with a final `Outcome` section
- `skills/` — one file per repeatable procedure
- `docs/` — Q&A, open questions, bug tracker, improvements backlog

Notes reference each other with `[[wikilinks]]`. Tags in the frontmatter
follow a closed vocabulary (`type:{plan,skill,memory,doc,agent,index}`,
`status:{draft,in-progress,done,archived}`, `topic:*`, plus two optional
markers: `pinned` and the `importance: 1..5` scalar).

The convention matters more than any single tool: when an agent knows that
a plan-in-progress is *always* a note with `type: plan` + `status:
in-progress` + a file under `<project>/plans/`, it can retrieve the right
set deterministically with one MCP call (`memory_plans`) instead of
probabilistically with a vector lookup.

## Architecture at a glance

A single Go binary serves both HTTP (web UI) and an MCP SSE endpoint:

```
┌──────────────────────────────────────────────────────────┐
│                  gosidian (single binary)                │
│   web UI :8080           MCP SSE :8765                   │
│         └──────┬──────────────┘                          │
│                │ shared state                            │
│       ┌────────▼─────────┐                               │
│       │  vault / index   │  (SQLite FTS5, LRU cache)     │
│       │  parser / gitsync│                               │
│       └────────┬─────────┘                               │
│                │                                         │
│                ▼                                         │
│        <vault>/*.md  ←→  fsnotify watcher                │
│                │                                         │
│                ▼                                         │
│        git push (opzionale) → any git remote             │
└──────────────────────────────────────────────────────────┘
```

Key invariants:

- **The vault is the source of truth, the SQLite index is a cache**. If
  the index is corrupted or deleted, a scan rebuilds it from the markdown
  files.
- **Single writer**. All writes — whether from the web UI, the MCP server
  or an external editor caught by fsnotify — flow through the same
  upsert-sync path, with an LRU read cache in front.
- **No magical merging**. Git sync (optional) is fail-loud: commits are
  local-first, push errors surface in `/healthz` and Prometheus metrics.
- **Optimistic locking**. Every read tool returns an `etag`; every write
  tool accepts `if_match` for safe multi-agent pipelines.

## What makes it different

Compared to Obsidian + community plugins: no plugin system, no Electron, no
per-user account lock-in, but a real **MCP surface** (34+ tools) that
treats the vault as a **machine-readable data layer** first, a human-
readable note-taking app second.

Compared to Logseq or Roam: server-side rendering, no JS framework, opt-in
web auth with invite-only membership, and the whole thing runs in a single
Alpine-based container under 50 MB.

Compared to RAG-on-markdown systems: no embeddings, no vector store. The
agent's MCP toolbelt is the retrieval layer, and it is deterministic.

## Status

Gosidian is stable as of v1.7. The core API (MCP tool set, frontmatter
conventions, vault layout) is considered backward-compatible within
major versions. Breaking changes ship as a v2 with a migration guide.

Contributions, questions and forks are welcome. See the main README for
quickstart and configuration, and `LICENSE` for the MIT terms.
