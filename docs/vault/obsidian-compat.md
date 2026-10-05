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

- **`![[target]]` embeds** — a line made of an embed alone includes
  that note, or its section with `![[target#Heading]]`, one level deep
  ([views](views.md#embeds)); an embed inside a sentence stays a link.
  Either way it counts as an outlink and a backlink.
- **Block references `[[target#^block-id]]`** — the anchor is stored
  as part of the link; rendering degrades to the target.
- **`.base` files** (Obsidian Bases) — shown read-only, each view
  translated into a gosidian view, with a warning for what has no
  equivalent (formulas, summaries, cards…): see
  [views](views.md#obsidian-bases). gosidian never writes them, and a
  ```` ```base ```` block inside a note stays a code block.
- **`.canvas` files** (Obsidian Canvas) — shown read-only: see
  [Obsidian canvases](#obsidian-canvases) below. gosidian never writes
  them.
- **`.obsidian/` folder** (workspace / config / plugins) — ignored
  entirely by gosidian.
- **Plugin content**:
  - **Dataview** queries in code blocks — preserved as static code
    blocks; gosidian doesn't execute them. For filters on frontmatter
    fields (type, status, dates, importance…) write a
    [view block](views.md), or use the **Query** window or the MCP
    `memory_query` tool.
  - **Templater** — likewise preserved as template source.
  - **Excalidraw** `.excalidraw.md` — file is valid markdown so it's
    indexed, but the graphical layer isn't rendered.
- **Inline tags `#topic/sub`** — indexed like the frontmatter ones,
  with one difference: a word of 3, 4, 6 or 8 hex digits (`#AC1F24`,
  `#fff`, `#c0392b80`, also in a list such as `#FAFAFA/#EFEFEF`) is a
  color in gosidian, never a tag, while
  Obsidian makes a tag of it when it starts with a letter. A note that
  needs such a tag puts it in its frontmatter (`tags: [cafe]`), which
  is always a tag.

### gosidian → Obsidian (things gosidian has that Obsidian ignores)

- **`.gosidian/` folder** — hidden, Obsidian doesn't touch it.
- **Custom frontmatter fields** (`importance`, `type`, `status`,
  `pinned`, `trigger_phrase`) — Obsidian preserves them but doesn't
  interpret them. You can query them with the **Dataview** plugin,
  which is the closest Obsidian equivalent to gosidian's `memory_query`
  tool and Query window.
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

## Obsidian canvases

A `.canvas` file (JSON Canvas 1.0) shows in gosidian as a read-only
note: the tree lists it, and the web UI draws its cards where the file
puts them — text cards rendered as markdown, file cards with the start of
their note (or the section a `#Heading` subpath names) and a title that
opens it, images, web links, groups behind the cards they hold, and the
connections as curves with their arrows, colors and labels. Drag the
background to move around, Ctrl + wheel (or a pinch) zooms, the buttons
zoom and fit the canvas. `memory_get` reads it for agents with
`kind: "canvas"`: its cards as text, the notes of its file cards as
wikilinks, its connections as `A → B: label` lines, and with `raw:true`
its JSON in `source`.

What gosidian leaves to Obsidian: editing (no tool and no window writes
a canvas), a group's background image, and the index — a canvas does
not count as a link to the notes it holds, so they show no backlink from
it, and it is not in the graph or the search. A card of a note the
reader may not see shows only its path.

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
