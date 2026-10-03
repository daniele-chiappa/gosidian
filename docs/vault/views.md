# Views — notes that show live data

A **view** is a fenced block in a note that lists other notes by folder and
frontmatter. The note stores only the spec; the list is computed every time
the note is read, so a section like "open entries" or "active plans" never
goes stale and never needs rewriting.

````markdown
```view
from: myproject/docs/improvements
where:
  - status in [open, in-progress]
  - {field: priority, op: eq, value: high}
sort: id asc
columns: [id, title, priority]
```
````

Nothing needs switching on. On GitHub, in Obsidian or in a plain editor a
view stays a readable code block.

## The spec

| Key | Meaning |
|---|---|
| `from` | the folder whose notes the view lists (or a list of folders); only notes **directly** inside it, the rows of a [database note](databases.md) |
| `where` | conditions, all required: a `"field op value"` string or a `{field, op, value}` map as in `memory_query` |
| `sort` | a field, or `path`, `title`, `modified`, optionally followed by `asc` or `desc` |
| `columns` | what to show; `title` links to the note, `path` and `modified` come from the note itself, anything else from its frontmatter. Default: `title` plus the fields used in `where` and `sort` |
| `limit` | rows to show (default 50, max 500); a note under the table says when more match |
| `as` | `table` (default), `list`, or `board` |
| `group_by` | with `as: board`, the field whose values make the board's columns: a `select` or `checkbox` field of the database the view lists |

A **board** groups the notes by `group_by`. When the view lists the rows
of a database, its columns follow the options of that select field (empty
ones included), then `false` and `true` for a checkbox; other values come
after them, and a column for the notes without a value comes last, when
there are some. Without a schema the columns are the values found.
Agents read a board as one list per non-empty column.

```view
from: myproject/docs/improvements
where:
  - status != superseded
as: board
group_by: status
columns: [title, priority]
```

String conditions use `=`, `!=`, `<`, `<=`, `>`, `>=`, `in [a, b]`,
`contains`, `exists` and `!exists`. ISO dates and numbers compare as such,
as in `memory_query`.

Two kinds of relative values:

- `this.<field>` is a field of the note that holds the view, so a note can
  list what points at it: in the note of `IMP-124`,
  `implements_imp contains this.id` lists the plans that implement it.
  `this.path` and `this.name` (the file name without extension) always
  exist.
- `today`, `today-7d`, `today+30d` are dates relative to the day the view
  is computed: `closed >= today-30d` lists what closed in the last month.

## Where views are computed

A view is always computed with the reader's own scope: it never lists a
note its reader could not open.

- **Web UI** — the note shows the table, list or board in place of the
  block, with links you can follow. `POST /api/v1/preview` also returns
  each view as data in `views` (columns typed by the database's schema,
  the rows with their fields and whether the reader may edit them, a
  board's columns), matched to its place in the HTML by the `data-view`
  index of its `<div class="gosidian-view">`. The schema of a database is
  given only when the reader may open the database note.
- **`memory_bootstrap`** — `hot_md` and the other session files come with
  their views computed: each block stays, and its result follows it between
  `gosidian:view-result` markers. The file's `etag` is unchanged (it is the
  one `if_match` needs); `views_etag` adds a hash of the results and is the
  value to pass in `known_etags`, since the plain etag can no longer prove
  that the views are unchanged. In `mode: "lite"` `hot_md` is an outline
  without the body, so without the views: see below.
- **`memory_get` / `memory_get_section`** — return the note as stored, so
  an agent editing it sees the file; `render_views: true` computes the
  views too.

A read that returns view blocks without their rows says so: `memory_get`,
`memory_get_section` and `memory_batch_get` (content mode) add a `hint`
with the number of blocks left uncomputed. Outlines mark the sections that
hold views with `views: N` — `memory_get_outline`, `memory_batch_get` in
outline mode, a truncated `memory_get` and the lite `hot_md` of
`memory_bootstrap`, which also carries a `hint`. An agent reading `hot.md`
by sections then knows which ones to fetch with `render_views: true`.

The computed result is never written to the file. A result copied into a
note by mistake is dropped the next time the views are computed, rather
than shown twice, and the directives (v16 and later) tell agents to leave
sections made of views alone at the end of a task; v17 adds when to read
with `render_views`.
