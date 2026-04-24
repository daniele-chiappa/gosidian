# Vault format

The vault is a **directory of plain markdown files**. Everything
gosidian does — search, backlinks, graph, MCP retrieval — is built
from the file contents + a small frontmatter convention.

## Source of truth

The markdown files on disk are authoritative. The SQLite index at
`<vault>/.gosidian/index.db` is a **cache**: wipe it and the next
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
├── .gosidian/          # machine-owned (not versioned)
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

## Bootstrap templates

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
