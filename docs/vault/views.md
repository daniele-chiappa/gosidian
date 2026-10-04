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
| `sort` | a field, or `path`, `title`, `modified`, optionally followed by `asc` or `desc`. A select field of the database the view lists sorts by its options (`low`, `medium`, `high`), not alphabetically |
| `columns` | what to show; `title` links to the note, `path` and `modified` come from the note itself, anything else from its frontmatter. Default: `title` plus the fields used in `where` and `sort` |
| `limit` | rows to show (default 50, max 500); a note under the table says when more match |
| `as` | `table` (default), `list`, `board`, or `count` |
| `group_by` | with `as: board`, the field whose values make the board's columns: a `select` or `checkbox` field of the database the view lists; with `as: count`, the field whose values the count is split by |

A **board** groups the notes by `group_by`. When the view lists the rows
of a database, its columns follow the options of that select field (empty
ones included), then `false` and `true` for a checkbox; other values come
after them, and a column for the notes without a value comes last, when
there are some. Without a schema the columns are the values found.
Agents read a board as one list per non-empty column. In the web UI it is
a row of columns; on a card you may write, dragging it to another column,
or picking one from its "Move to…" menu, rewrites its `group_by` field
(the column without a value removes it), unless someone moved the card
meanwhile. A view of a database also offers a new row (under a
table, in each column of a board) to a reader who may write it: see
[new rows](databases.md#editing-rows-in-the-web-ui).

```view
from: myproject/docs/improvements
where:
  - status != superseded
as: board
group_by: status
columns: [title, priority]
```

A **count** shows how many notes the view selects instead of listing them:
one number, or with `group_by` a number per value, in the order of the
select's options (a note with a list field counts once for each of its
values, the notes without a value last). Agents read `**36** notes ·
priority: high 1 · medium 14 · low 21`; the web UI shows the number large.

```view
from: myproject/docs/improvements
where:
  - status in [open, in-progress]
as: count
group_by: priority
```

String conditions use `=`, `!=`, `<`, `<=`, `>`, `>=`, `in [a, b]`,
`contains`, `exists` and `!exists`. ISO dates and numbers compare as such,
as in `memory_query`.

Relative values:

- `this` alone is the note that holds the view, as a link: see
  [relations](#relations) below.
- `this.<field>` is a field of the note that holds the view, so a note can
  list what points at it: in the note of `IMP-124`,
  `implements_imp = this.id` lists the plans that implement it (on a
  list, `=` matches an element; `contains` would match text inside one,
  so `IMP-12` would find `IMP-124` too).
  `this.path` and `this.name` (the file name without extension) always
  exist.
- `today`, `today-7d`, `today+30d` are dates relative to the day the view
  is computed: `closed >= today-30d` lists what closed in the last month.

### Relations

A condition whose value is a note matches by link, wherever and however
the link is written ([[path]], [[file name]], with an alias or a
heading):

- `related contains this` keeps the notes whose `related` field links to
  the note holding the view; `related contains [[myproject/docs/bugs/BUG-089]]`
  those that link to that note. `=` reads the same, `!=` keeps the notes
  whose field does not link there (or that lack it).
- `links` is a pseudo-field for every link of a note, body and frontmatter
  alike: `links contains this` lists what links to the note, its
  backlinks as a view. It is not a column.

A value written as a `[[wikilink]]` must name a note the reader can open,
or the view says so. In YAML write it in a block list (`- related contains
[[note]]`) or quoted: inside `[ … ]` the brackets start a list. A
database can give every row such views with [`row_views`](databases.md#row-views).

## Values in the text

A number can also sit inside a sentence, as an inline code span:

```markdown
Open bugs: `=count(myproject/docs/bugs where status in [open, in-progress])`.
```

After `=count(` come the folders, as in `from` (several separated by
commas), then, optionally, `where` and the conditions of a view joined by
`and` (`this`, `today` and relations included). The web UI shows the
number, its expression on hover; agents get the number followed by the
expression, `` 3 (`=count(…)`) ``, so they see what produces it, and a copy of
that form written back into the note counts as the expression alone. A
value in a code block stays as written, and so does one in a span of two
backticks (`` `` `=count(…)` `` ``), the way to show the syntax in prose.

## Where views are computed

A view is always computed with the reader's own scope: it never lists a
note its reader could not open.

- **Web UI** — the note shows the table, list or board in place of the
  block, with links you can follow. `POST /api/v1/preview` also returns
  each view as data in `views` (columns typed by the database's schema,
  the rows with their fields and whether the reader may edit them, a
  board's columns, and for a database the values a new row starts with,
  `defaults`, and whether the reader may add one, `creatable`), matched
  to its place in the HTML by the `data-view` index of its
  `<div class="gosidian-view">`. The schema of a database is
  given only when the reader may open the database note.
- **`memory_bootstrap`** — `hot_md` and the other session files come with
  their views computed: each block stays, and its result follows it between
  `gosidian:view-result` markers. The file's `etag` is unchanged (it is the
  one `if_match` needs); `views_etag` adds a hash of the results and is the
  value to pass in `known_etags`, since the plain etag can no longer prove
  that the views are unchanged. In `mode: "lite"` `hot_md` is an outline
  without the body, so without the views: see below.
  The views of a session file are held to about 8 KiB together: when they
  do not fit, the small ones stay whole and a long one keeps the rows that
  fit, then says how many it left out and gives the `memory_query` that
  returns them all. The automatic lite shape is decided on the text as
  written, so a backlog that grows never takes the tables out of the
  bootstrap; `maintenance` reports the computed size and asks to narrow
  the views past 12 KiB.
- **`memory_get` / `memory_get_section`** — return the note as stored, so
  an agent editing it sees the file; `render_views: true` computes the
  views too.

A read that returns view blocks without their rows says so: `memory_get`,
`memory_get_section` and `memory_batch_get` (content mode) add a `hint`
with the number of blocks left uncomputed, and on a row of a database
with [row views](databases.md#row-views) how many of those. Outlines mark the sections that
hold views or values with `views: N` — `memory_get_outline`, `memory_batch_get` in
outline mode, a truncated `memory_get` and the lite `hot_md` of
`memory_bootstrap`, which also carries a `hint`. An agent reading `hot.md`
by sections then knows which ones to fetch with `render_views: true`.

The computed result is never written to the file. A result copied into a
note by mistake is dropped the next time the views are computed, rather
than shown twice, and the directives (v16 and later) tell agents to leave
sections made of views alone at the end of a task; v17 adds when to read
with `render_views`.
