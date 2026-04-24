# Multi-project layout

gosidian's vault is multi-project by default: every top-level folder
under `vault/` is a separate **project**. Projects share the same
physical filesystem but get their own scoped retrieval namespace,
token scope, and boot aggregate.

## Top-level folders = projects

```
vault/
├── gosidian/          # project A
│   ├── hot.md
│   ├── plans/
│   └── …
├── dockers/           # project B
│   ├── hot.md
│   └── …
└── my-personal-notes/ # project C
    └── …
```

`memory_list_projects()` returns the three names. `memory_bootstrap`
takes a `project` parameter and returns the aggregate for that project
only. `memory_search` is project-scoped by default; pass
`projects=["a","b"]` for cross-project results.

## Cross-project wiki-links

Wiki-links across projects are explicit:

```markdown
See [[gosidian/memory/architecture]] for the internal layout.
```

The parser resolves `<project>/<path>` as an absolute vault path.
`memory_outlinks(path, include_cross_project=true)` returns these
links; the default `false` keeps results tightly scoped.

The graph view at `/graph?include_cross_project=true` renders cross-
project edges as dashed lines — visually distinct from intra-project
links.

## Scoped tokens

A bearer token created with `--project foo` sees only `foo/*` in every
tool response:

- `memory_list_notes()` → only `foo/` entries
- `memory_search("x")` → results restricted to `foo/`
- `memory_search("x", projects=["bar"])` → silently returns an empty
  set; scope intersection, no error
- `memory_create(path="bar/new.md", …)` → rejected with a scope
  error

This lets you issue per-agent tokens that can't leak state between
unrelated projects. See [Authentication](../mcp/authentication.md#per-project-scoping)
for the full behaviour.

## When to use one project vs many

**One project** — use when:

- A single domain / topic owns the vault
- Cross-linking between parts of the notes is frequent and flat
  hierarchy feels natural
- No need for per-audience access control

**Multiple projects** — use when:

- Separate domains share the same gosidian instance (work notes +
  personal notes + side-project ADRs)
- Different agents need scoped access (e.g. a research agent with
  `--project research` read-only, a writing agent with
  `--project blog` write)
- You want `memory_bootstrap(project)` to return tight, relevant
  aggregates instead of a flood

The top-level folder structure is a configuration choice, not a
limitation of the tool. Refactor freely: `memory_rename_project` and
`memory_move_note` keep wiki-links from breaking.

## Project discovery / creation

- **Create** (admin token): `memory_create_project(name="newproject")`
- **Delete** (admin token): `memory_delete_project(name)`
- **Rename** (admin token): `memory_rename_project(from, to)`
- **Scaffold** the Karpathy-Wiki layout:
  `memory_project_scaffold(project, template="karpathy-wiki")`

See [Agent patterns → Bootstrap a project](../mcp/patterns.md#bootstrap-a-new-project).
