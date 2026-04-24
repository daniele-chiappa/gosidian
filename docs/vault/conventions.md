# Vault conventions

A small set of conventions keeps retrieval predictable. Agents that
follow them can discover notes by identity — no similarity search
needed. Notes that break them still work; the typed retrieval tools
just won't see them.

## Tag vocabulary (closed)

```
type:{plan, skill, memory, doc, agent, index, handoff}
topic:{mcp, webui, vault, index, gitsync, auth, deploy, meta}
status:{draft, in-progress, done, archived, pending, snapshot}
pinned                     # marker, no value
<project-name>             # automatically valid for each top-level folder
```

`memory_lint` reports any tag outside this vocabulary as a
`frontmatter-tag-unknown` warning. Extending the vocabulary means
amending this list (and — for machine-enforced checks — the
`knownTagValues` map in `internal/lint/rules.go`).

## Importance scalar

The frontmatter field `importance: N`, where `N ∈ [1, 5]`:

- **5** — critical (must-know in any relevant context)
- **4** — high (read during sessions touching this area)
- **3** — default (the value assumed when the field is absent)
- **2** — low (useful reference, not a priority)
- **1** — archival (kept for history, don't prioritise)

Retrieved by `memory_notes_by_importance(project, min_level=3,
limit=50)`, ordered descending.

**Don't mix `pinned` and `importance: 5`** on the same "critical"
notes — they answer different questions:

- `pinned` = "read this every session"
- `importance: 5` = "high priority **when relevant**"

## Checkbox format (v1.9)

`memory_todos(project, filter?)` extracts `- [ ]` / `- [x]` checkboxes
from markdown. To be recognised, checkboxes must follow strict
GitHub-flavored format:

```markdown
- [ ] task open
- [x] task closed
- [X] task closed (uppercase X also accepted)
```

Not recognised: `* [ ]` / `+ [ ]` (non-dash bullet), `[-]` / `[/]` /
`[~]` (non-standard states), `[ ] text` (missing bullet), emoji
checkbox markers.

The scanner ignores lines inside code fences (```` ``` ```` / `~~~`)
and the initial YAML frontmatter block.

## Lint rules (v1.9)

`memory_lint(project, rules?)` ships with five baseline rules:

- `broken-wikilink` (warning) — `[[target]]` that doesn't resolve
- `orphan-note` (info) — note with no backlinks nor outlinks
  (README.md, hot.md, log.md, and `docs/*` are exempt by default)
- `frontmatter-missing` (error) — note without YAML frontmatter
- `frontmatter-tag-unknown` (warning) — tag outside the vocabulary
- `status-incoherent` (warning) — plan with `status:in-progress` but
  not referenced in `<project>/hot.md` `## Active plans`

A healthy vault returns zero `severity:error`. Use `min_severity=error`
for strict CI-style gating.

## Karpathy-Wiki-Stack project shape

Each project directory mirrors the same seven surfaces. Humans and
agents always know where to look:

| Path | Role |
|---|---|
| `hot.md` | session cache (current focus, active plans, recent decisions) |
| `README.md` | project index / landing page |
| `log.md` | append-only activity log |
| `memory/` | stable knowledge: architecture, ADRs, conventions, glossary |
| `plans/` | one file per non-trivial task, with a final `Outcome` section |
| `skills/` | reusable procedures (trigger phrase + steps + rollback) |
| `docs/` | `bugs.md` / `open-questions.md` / `improvements.md` / `qa.md` |
| `agents/` | optional — role descriptors for specialised agents |

`memory_project_scaffold` creates all of these in one call, idempotently.
See [Agent patterns](../mcp/patterns.md#bootstrap-a-new-project).
