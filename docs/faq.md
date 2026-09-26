# FAQ

Questions that arrive regularly when someone discovers gosidian.

## Why not just use Obsidian Sync?

Obsidian Sync is a great UX for a human who wants to mirror a vault
across their devices. It's not an **agent surface**: there's no
typed retrieval, no authentication layer for automation, no MCP
endpoint. gosidian doesn't compete with Obsidian — the vault format
is fully compatible — it adds a programmatic layer on top.

If your use case is "I want my notes on my iPhone and laptop" the
answer is Obsidian Sync. If it's "my agent needs to read, write, and
reason over my notes with scoped authentication", that's gosidian.

## Why not Notion / Roam / Logseq?

The same files-on-disk principle applies: gosidian needs the vault to
be a directory of markdown files so agents can edit them freely, git
can version them, and you can walk away from gosidian any time
without a migration. Notion is proprietary format; Roam is hosted
with a custom DB; Logseq uses markdown but layers a block-level model
on top.

None of these are **wrong** — they optimise for different users.
gosidian optimises for the agent-first use case that assumes markdown
plus a typed retrieval API.

## Why not RAG or vector search?

Structured retrieval (tags, frontmatter, paths, backlinks) is more
**predictable** than similarity search for an agent's working
memory. The agent wrote the note; it should find it back by identity,
not by heuristic recall.

Vector search pulls back "similar but not relevant" content → adds
noise to the context window → **increases** hallucination risk, not
decreases it. This is the core argument of
[ADR-007](architecture.md#adrs). gosidian will add semantic search
only when specific triggers hit (>10k notes, tagging discipline
consistently breaks, fuzzy human browsing becomes the primary use
case) — not before.

## Why Go?

- **Single binary**. No virtualenv, no node_modules, no runtime.
- **Predictable performance**. The embedded SQLite + pure-Go FTS5
  build makes the CLI experience snappy without GC tuning.
- **Easy Docker packaging**. Static build → Alpine → done.
- **Standard library is enough for 90% of what gosidian does.**
  Minimal dependency footprint means fewer supply-chain surprises.

This is a preference, not a universal answer. Python with FastAPI
would work; Rust with axum would work. The pure-Go path happens to
match the project's shape.

## Why Vue 3 and not React / Svelte?

The UI started as server-rendered HTML + HTMX. As it grew an editor
with live preview, a graph view, an audit trail and — in v2.3 — the
plancia window manager, the swap-in-place model stopped paying for
itself, and v2.0 moved the UI to a **Vue 3 single-page app**.

Vue was chosen over React/Svelte for a small, framework-light SPA: a
gentle composition API, first-class TypeScript, a built-in store
(Pinia) and router, and a calm upgrade cadence. The whole bundle is
embedded in the binary via `go:embed` and served under a strict CSP
(`script-src 'self'`), so there's still no CDN and no runtime npm.

Most agent work still happens **through MCP**, not the web UI — the
UI is the human-readable window into the same vault.

## Is it multi-tenant?

**No, and not by design.** One gosidian process serves one vault. Per-
project scoping on tokens is the isolation mechanism within a vault,
not between them. Running N gosidian processes for N tenants is the
supported pattern (one Docker container per customer, for instance).

Multi-tenancy would require cross-tenant user management, quota
enforcement, and data partitioning that would widen the blast radius
of every security fix. Out of scope for the foreseeable future.

## How stable is v2.x?

gosidian has been through a full v1.x line (the HTMX-rendered UI) and
the v2.0 SPA cutover; the current release is **v2.6.0**. SemVer
applies: within `v2.x`, backward compatibility on the MCP tool
surface, the web URL contract, and the vault layout is preserved.
v2.0 was a deliberate major bump because it retired the legacy HTML /
HTMX-partial endpoints in favour of the REST API at `/api/v1/*` — see
the [v2 migration guide](migration-v2.md) for the route map and the
downgrade path.

That said, **this is still a young open-source project**. File
issues, expect occasional friction. Security advisories ship through
[SECURITY.md](../SECURITY.md). PRs are reviewed on best effort.

## Can I self-host it "seriously"?

Yes — and most users will. See [Deployment](deployment.md) for the
Docker Compose + reverse proxy recipe. Backup the vault + the
`.gosidian/` directory nightly; everything else is rebuildable.

## What's the roadmap?

The short list as of v2.36:

- **v2.x** — incremental SPA polish, more localisation, theme
  refinements, and features requested by adopters (file an issue);
  candidates on the table: a WebDAV endpoint for Obsidian mobile, hooks
  for other agent CLIs
- **Deferred** — Semantic search (ADR-007: the
  [benchmark](benchmark.md) found no answer it would fix, since agents
  recover the paraphrases lexical search misses; it waits for evidence of
  real misses), multi-tenant / hosted mode, real-time
  collaborative editing, the whole-vault zip download parked during
  the v2.0 cutover (see [migration guide](migration-v2.md))

Reopening the semantic search question depends on usage evidence, not
dates. See ADR-007 triggers.
