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

## Why HTMX and not React / Svelte?

Most agent work happens **through MCP**, not the web UI. The UI is a
human-readable window into the same vault — it doesn't need client-
side routing, global state, or reactivity beyond swap-in-place. HTMX
delivers the interactivity users expect with zero build step, zero
npm install, zero framework upgrade treadmill.

If/when a page grows complex enough to need a real frontend
framework, that page (and only that page) can ship its own bundle.
Nothing about the server prevents it.

## Is it multi-tenant?

**No, and not by design.** One gosidian process serves one vault. Per-
project scoping on tokens is the isolation mechanism within a vault,
not between them. Running N gosidian processes for N tenants is the
supported pattern (one Docker container per customer, for instance).

Multi-tenancy would require cross-tenant user management, quota
enforcement, and data partitioning that would widen the blast radius
of every security fix. Out of scope for the foreseeable future.

## How stable is v1.0.0?

gosidian has been used internally since mid-2026 through nine
private iterations (v1.0 through v1.9). The v1.0.0 public tag is the
first external release, not "the first cut". SemVer applies from
here: `v1.x` preserves backward compatibility on the MCP tool
surface, web URL contract, and vault layout.

That said, **this is a young open-source project**. File issues,
expect occasional friction. Security advisories ship through
[SECURITY.md](../SECURITY.md). PRs are reviewed on best effort.

## Can I self-host it "seriously"?

Yes — and most users will. See [Deployment](deployment.md) for the
Docker Compose + reverse proxy recipe. Backup the vault + the
`.gosidian/` directory nightly; everything else is rebuildable.

## What's the roadmap?

The short list as of v1.0.0:

- **v1.1** — UI polish iterations, more localisation, theme refinements
- **v1.2+** — Features requested by early adopters (file an issue)
- **Deferred** — Semantic search (ADR-007), multi-tenant mode,
  real-time collaborative editing

Reopening the semantic search question depends on usage evidence, not
dates. See ADR-007 triggers.
