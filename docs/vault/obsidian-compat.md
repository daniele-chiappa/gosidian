# Obsidian compatibility

gosidian is designed to be **opened side-by-side with Obsidian on
the same vault**. Zero migration, zero lock-in: both read the same
markdown files.

## What's fully compatible (both directions)

| Feature | Format | gosidian | Obsidian |
|---|---|---|---|
| Markdown body | CommonMark + GFM | ✅ | ✅ |
| YAML frontmatter | standard | ✅ | ✅ |
| Wiki-link `[[target]]` |  | ✅ | ✅ |
| Aliased `[[target\|display]]` |  | ✅ | ✅ |
| Heading anchor `[[target#section]]` |  | ✅ (resolves target) | ✅ |
| Frontmatter tag array `tags: [x, y]` |  | ✅ | ✅ |
| Standard markdown link `[x](url)` |  | ✅ | ✅ |
| Code fences + syntax highlighting |  | ✅ | ✅ |
| Standard image embed `![alt](path)` |  | ✅ | ✅ |

Open the vault in Obsidian (File → Open vault as folder) and clicks on
wiki-links navigate, the graph view populates, search finds, and
backlinks appear — without editing a single file.

## Where the match is imperfect

### Obsidian → gosidian (things Obsidian has that gosidian doesn't)

- **`![[target]]` embeds** — the parser recognises them as links
  (counts as outlink/backlink), but the web UI doesn't render them
  inline. Link graph coherent, rendering: plain link.
- **Block references `[[target#^block-id]]`** — the anchor is stored
  as part of the link; rendering degrades to the target.
- **`.canvas` files** (Obsidian Canvas) — JSON, not markdown. Ignored
  by gosidian (left on disk untouched).
- **`.obsidian/` folder** (workspace / config / plugins) — ignored
  entirely by gosidian.
- **Plugin content**:
  - **Dataview** queries in code blocks — preserved as static code
    blocks; gosidian doesn't execute them.
  - **Templater** — likewise preserved as template source.
  - **Excalidraw** `.excalidraw.md` — file is valid markdown so it's
    indexed, but the graphical layer isn't rendered.
- **Inline tags `#topic/sub`** — the vault used here prefers
  frontmatter tags. Inline tags work in Obsidian but gosidian's
  `memory_list_tags` reads from frontmatter only.

### gosidian → Obsidian (things gosidian has that Obsidian ignores)

- **`.gosidian/` folder** — hidden, Obsidian doesn't touch it.
- **Custom frontmatter fields** (`importance`, `type`, `status`,
  `pinned`, `trigger_phrase`) — Obsidian preserves them but doesn't
  interpret them. You can query them with the **Dataview** plugin,
  which is the closest Obsidian equivalent to gosidian's typed
  retrieval tools.
- **Colon-based tag namespaces** (`type:skill`, `topic:mcp`,
  `status:in-progress`) — Obsidian accepts them as tags but its tag
  UX prefers `/` hierarchies (`type/skill`). Cosmetic, not a blocker.
- **Multi-project layout** — gosidian treats top-level folders as
  projects with native semantics (token scope, boot aggregate).
  Obsidian sees them as ordinary subfolders — cross-project
  `[[projectB/note]]` links work as plain wiki-links.
- **Attachment management** (hash-addressed, ETag-aware) — Obsidian
  sees the files as plain files.
- **Audit log / metrics / web UI / MCP** — zero on the Obsidian side.
  Server-only features.

## Simultaneous use

Keeping Obsidian open alongside a running gosidian works. Both watch
the filesystem and react to the other's writes:

- Write a note in Obsidian → gosidian's watcher reindexes → visible
  in `memory_search` immediately.
- Write via MCP `memory_create` → atomic filesystem write → Obsidian
  reloads the note in its UI.

### Cautions

- **Same-note concurrent edits**: last write wins. gosidian's ETag
  optimistic locking protects writes **via MCP** from each other; it
  can't protect from an external editor (Obsidian doesn't send ETag
  headers). In practice: one person, one editor at a time.
- **Git conflicts**: if you use gosidian's built-in git sync **and**
  the Obsidian Git plugin in parallel, the two will race. Pick one.

## Zero lock-in

If gosidian ever stops being the right fit, the vault is already in
its final portable form. Delete `.gosidian/` and you have a pure
Obsidian (or VS Code, or raw filesystem) vault. No export, no
conversion, no format change.

This is [ADR-002](../architecture.md#adrs) in action: gosidian is a
*layer* on top of a markdown vault, not a replacement for it.
