# Vault format

The vault is a **directory of plain markdown files**. Everything
gosidian does — search, backlinks, graph, MCP retrieval — is built
from the file contents + a small frontmatter convention.

## Source of truth

The markdown files on disk are authoritative. The SQLite index at
`<state-dir>/index.db` (default `<vault>/.gosidian/index.db`) is a **cache**: wipe it and the next
startup rebuilds it from the files. This has three consequences:

- **External edits are supported**. Edit from Obsidian, VS Code,
  `vim`, or any tool that writes markdown; the watcher picks up the
  change and reindexes. See
  [Obsidian compatibility](obsidian-compat.md) for the detailed
  compatibility table.
- **Zero lock-in**. Stopping gosidian leaves the vault untouched.
  You can fork, archive, migrate — the vault is just files.
- **Git works**. Commit the `vault/` directory (minus `.gosidian/`)
  and you have a versioned history. Optional built-in git sync
  debounces and automates that.

## Directory layout

```
vault/
├── .gosidian/          # machine-owned (not versioned); state files move out with --state-dir
│   ├── index.db        # SQLite FTS5 index (rebuildable)
│   ├── tokens.json     # MCP bearer tokens (hashed)
│   ├── auth.json       # webauth accounts + invites
│   ├── audit.jsonl     # append-only audit log
│   ├── config.toml     # persistent settings
│   ├── templates/      # bootstrap templates (v1.8+)
│   └── trash/          # soft-deleted notes (if enabled)
├── projectA/
│   ├── README.md       # project landing page
│   ├── hot.md          # session cache (optional convention)
│   ├── log.md          # append-only activity log
│   ├── memory/         # stable knowledge (ADRs, conventions, …)
│   ├── plans/          # one file per non-trivial task
│   ├── skills/         # reusable procedures
│   ├── docs/           # Q&A, bug tracker, improvements
│   └── attachments/    # hashed image / document uploads
└── projectB/…
```

Top-level folders are **projects**. See
[Multi-project layout](multi-project.md) for how cross-project links
and scoped tokens interact.

## Markdown conventions

Notes support standard markdown plus two gosidian-specific affordances:

### Wiki-links

```markdown
This references [[other-note]] or [[other-project/some-note]].
Aliased: [[target|display text]].
```

Unresolved targets render with a `broken` CSS class so authors notice
typos. The MCP tool `memory_lint` reports them as `broken-wikilink`
warnings.

### Frontmatter

```yaml
---
title: Example note
description: Optional one-liner.
tags: [gosidian, type:plan, topic:mcp, status:in-progress]
type: plan
importance: 3
updated: 2026-04-23
---
```

- **`tags`** is the primary discovery surface. Vocabulary is closed
  — see [Conventions](conventions.md).
- **`importance`** is a scalar in `[1, 5]` read by
  `memory_notes_by_importance`. Default 3 when the field is absent.
- **`pinned`** in the `tags` array flags notes as read-every-session;
  retrieved by `memory_pinned`.
- **`updated`** is informational (gosidian uses `mtime` for
  retrieval, not this field).

### `.html` notes

A single-file `.html` document can be a **first-class note** alongside
`.md`, when the operator opts in. The feature is **off by default**;
enable it with `[vault] html_notes = true` in `config.toml` (or the
`GOSIDIAN_VAULT_HTML_NOTES` environment variable).

- **Frontmatter** lives in a leading **HTML comment** at the top of the
  file (instead of the YAML `---` fence used by markdown).
- The body is rendered inside a **sandboxed iframe** rather than the
  goldmark pipeline.
- An `.html` note still **participates in the graph, full-text search,
  and backlinks** exactly like a `.md` note.

See **ADR-011** for the rationale and security boundary.

### Image media notes

An image can be a **first-class note** (ADR-013). The note stays a normal
`.md` whose frontmatter declares `type: image` and a `media:` pointer to an
image attachment; the body is the **caption/transcript** — the searchable
text (it lands in full-text search and is what an agent retrieves; the image
bytes are not indexed). Off by default; enable with `[vault] media_notes =
true` (or the `GOSIDIAN_VAULT_MEDIA_NOTES` environment variable).

```markdown
---
title: Architecture diagram
type: image
media: project/attachments/abc123.png
tags: [project, type:image]
---

The plancia in tiling mode with three side-by-side windows… (caption → FTS)
```

- The note participates in tags, links, backlinks, graph and full-text
  search **exactly like any markdown note** — it *is* one.
- On read the backend resolves `media:` and exposes `kind: "image"` plus a
  `media` object (`url`, `mime`, `size`); the web UI renders the image +
  caption, and a broken pointer degrades to a placeholder.
- **Images only** (`png/jpg/jpeg/gif/webp/svg`); video is intentionally not
  supported (a git-synced vault should not carry large binaries).
- Create one from the web UI (the `+` on a tree folder → **Immagine**) or via
  the MCP tool `memory_create_media_note` (upload + note, atomic).

See **ADR-013** for the rationale.

### CSV table notes

Long tabular data (audit reports, exports) can be a **first-class note**
(ADR-016) with the same mechanism as image media notes: a normal `.md`
whose frontmatter declares `type: table` and a `media:` pointer to a
`.csv` attachment. The web UI renders the CSV as a **paginated table**;
the report note links the table note with a wikilink instead of embedding
thousands of rows in its body. Off by default; enable with
`[vault] table_notes = true` (or the `GOSIDIAN_VAULT_TABLE_NOTES`
environment variable).

```markdown
---
title: Access audit July
type: table
media: project/attachments/def456.csv
tags: [project, type:table]
---

Portal access log, July export. (caption -> FTS)

Columns: user, action, ts
Rows: 12480
```

- The **column headers and row count** are inlined into the body (the MCP
  tool does it automatically) and land in full-text search; the cell
  **values are not indexed** — write a caption saying what the table
  contains.
- On read the backend resolves `media:` and exposes `kind: "table"` plus a
  `media` object (`url`, `mime`, `size`); a broken pointer degrades to a
  placeholder.
- `.csv` only; the comma / semicolon / tab delimiter is auto-detected.
- Create one via the MCP tool `memory_create_table_note` (upload + note,
  atomic).

See **ADR-016** for the rationale.

## Bootstrap templates

With `--state-dir` (recommended, see [Configuration → State
directory](../configuration.md#state-directory)) the credential store,
config, audit log and index move out of the vault root and `.gosidian/`
holds vault content only: `templates/` and `trash/`.

Three templates ship with the binary and are seeded into
`.gosidian/templates/` on first start:

| Name | When to pick |
|---|---|
| `karpathy-wiki` (default) | Full layout — `memory/`, `plans/`, `skills/`, `docs/`, `agents/`. Best for long-lived projects. |
| `minimal` | Just `hot.md` + `README.md` + `log.md`. For experiments or spike notes. |
| `team` | Karpathy-Wiki + `agents/` with backend / frontend / devops role stubs. |

Discover and invoke them with
`memory_list_bootstrap_templates()` and
`memory_project_scaffold(project, template="karpathy-wiki")`. See
[Agent patterns → Bootstrap a project](../mcp/patterns.md#bootstrap-a-new-project)
for the typical flow.

Custom templates: copy `karpathy-wiki` to a new directory under
`.gosidian/templates/<name>/`, edit `_template.toml` (name,
description, prompt, variables) and the files inside. The new
template appears immediately — no rebuild needed.
