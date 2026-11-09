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
limitation of the tool. Refactor freely: `memory_move_note` and
`memory_rename_project` keep wiki-links from breaking, and a project
renamed keeps the links to its attachments and its image and table notes
too, in the notes the token may write. Views and databases name a
project in their `from:` as text: update those by hand.

## Project discovery / creation

- **Create** (admin token): `memory_create_project(name="newproject")`.
  The project starts with a fresh access entry (the store's default
  visibility); a name that MCP tokens are still scoped to is refused,
  since the new project would fall into their scope.
- **Delete** (admin token): `memory_delete_project(name)`. With the
  trash on (`trash.enabled`) the project goes there with its access
  (visibility, member and team grants) and the web UI's trash restores
  it as it was; otherwise it is removed from disk. MCP tokens scoped to
  it alone are revoked, and tokens that also list other projects lose
  it.
- **Rename** (admin token): `memory_rename_project(from, to)`, or
  *Rename* in the web UI's Projects window (an admin grant). Its notes
  move under the new name, with its access and the MCP tokens scoped to
  it, and what names its files follows it, in the notes the renamer may
  write, the project's own included:
  - wiki-links and embeds with the project's path, `[[old/x]]`,
    `[[old/x#heading|alias]]`, in the body and in the frontmatter;
  - links to its attachments, `![](/vault-files/old/attachments/a.png)`;
  - the `media:` pointer of its image and table notes.

  A reference to a file that is not there stays as it is, and so do
  links by name (`[[x]]`), which keep resolving. Notes of projects the
  renamer may not write keep the old path: the answer counts them
  (`not_rewritten`, `links.not_rewritten` in the web UI's), without
  naming them. With git sync on, each note rewritten outside the project
  is committed as an `update` in the renamer's name. The `from:` of
  views, databases and boards, inline queries and canvases are not
  rewritten.
- **Scaffold** the Karpathy-Wiki layout:
  `memory_project_scaffold(project, template="karpathy-wiki")`

See [Agent patterns → Bootstrap a project](../mcp/patterns.md#bootstrap-a-new-project).
