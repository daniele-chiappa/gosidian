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
| `sort` | a field, or `path`, `title`, `modified`, optionally followed by `asc` or `desc` (default `desc`, `asc` for `path` and `title`). Up to four keys, as a list (`sort: [status asc, priority desc]`) or separated by commas (`sort: plans desc, id asc`): each key orders the notes the one before leaves tied, and the path breaks the last ties. A select field of the database the view lists sorts by its options (`low`, `medium`, `high`), not alphabetically |
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
- `path` is the note itself: `path in this.implements_imp` keeps the notes
  the `implements_imp` field of the note holding the view points at, the
  other direction of a relation; `path = [[note]]` keeps that one note.
  It takes `=`, `!=` and `in`.

A value written as a `[[wikilink]]` must name a note the reader can open,
or the view says so. In YAML write it in a block list (`- related contains
[[note]]`) or quoted: inside `[ … ]` the brackets start a list. A
database can give every row such views with [`row_views`](databases.md#row-views),
and count or sum what a row's relations reach with [rollups](databases.md#rollups).

## Embeds

A line made of `![[note#Heading]]` alone includes that section of another
note, `![[note]]` the whole note, as in Obsidian, so the same text works
there. The views and values it includes are computed with `this` the note
that embeds them: a section such as "What links here" (`links contains
this`) written once serves as a model in many notes. To list from the
other note, write its path in the view instead of `this`.

- Only a markdown note the reader may open is included; an image embed
  stays an image, a note the reader cannot open stays a link.
- One level: an embed inside the included text is shown as a link, and so
  is a note that embeds itself. A heading the note does not have shows a
  warning; the heading is found as `memory_get_section` finds it (its
  text, or an ID at its start).
- **Web UI** — the included text follows a small head that links to its
  origin and ends with a rule; the tables and boards it includes edit
  their rows as in the note itself.
- **Agents** — with `render_views` (`memory_get`, `memory_get_section`)
  and in the bootstrap, the embed line stays and the included text follows
  it between `gosidian:embed` markers, never to be copied into the file.
  Without, the `hint` says how many embeds were left out.
- **By sections** — `memory_get_section` finds the heading in the text as
  written and includes the embed whole, even when the included section
  starts with a heading of the same level; a heading that only the embed
  holds is found in the computed note. The outline marks a section that
  embeds with `embeds: N`, and the bootstrap's lite outline of `hot.md` is
  that of the text as written, so it names only the note's own headings.

## Snapshots

A snapshot freezes a note as it reads now: `memory_snapshot(path)`, `POST
/api/v1/notes/{path}/snapshot`, or the camera button of a note in the web
UI. It writes a dated note beside it, `<folder>/<name>.snapshots/
YYYY-MM-DD.md` (with the time for a second one the same day), and the note
itself does not change.

- **What it holds**: the note's text with each view replaced by its rows,
  each value by its number (its expression kept as text, between double
  backticks) and its embeds included, so the snapshot never recomputes;
  above it, a line that says what it is a snapshot of and when.
- **Frontmatter**: `type: snapshot`, `source: "[[note]]"`, `date` and the
  project's tag only, so the snapshot of a plan is no plan. It sits in a
  subfolder, out of the rows of a database and of a view of the folder.
- The vault's git keeps the series: what a backlog view showed each week,
  say. A snapshot links to what its views listed, so those notes count it
  among their backlinks.

## Obsidian bases

A `.base` file, the YAML of views that Obsidian's Bases read, shows in
gosidian as a read-only note: the tree lists it, the web UI opens it, and
`memory_get` reads it with `kind: "base"`, its YAML as written in
`source`. Each of its views becomes a view block under its name, computed
like any other with the reader's scope; what has no equivalent is said in
a warning above the view, never guessed. No tool writes a base: it is
edited in Obsidian.

| Base | View |
|---|---|
| `file.inFolder("Books")` | `from: <project>/Books` (a base reads the folders of its own project; several in an `or` are several folders) |
| `file.hasTag("a", "b")`, `tags.contains("a")` | `tags = a`, `tags in [a, b]` (a nested tag `a/x` is not matched) |
| `file.hasLink("Note")` | `links contains [[Note]]` |
| `file.hasProperty("due")`, `due.isEmpty()` | `due exists`, `due !exists` |
| `status == "done"`, `!=`, `<`, `<=`, `>`, `>=` with a string, number or boolean | the same condition |
| `x.contains("text")` | `x contains text` |
| `a && b`, `and:` | both conditions |
| `or:` of folders, or of one field equal to several values | several folders, `in` |
| `not:`, a leading `!` | the opposite condition (`=` and `!=`, `<` and `>=`, `exists` and `!exists`) |
| `order` | `columns`, with `file.name` as `title`, `file.mtime` as `modified`, `file.ctime` as `created_at`; the title comes first |
| the `sort` keys, `limit` | `sort`, `limit` |
| `type: table`, `type: list` | `as: table`, `as: list` |

Left out, each with its warning: formulas, summaries, display names,
grouping, a `sort` key without an equivalent and the keys after it (they
would order other ties), keys beyond the fourth, the other view types
(cards show as a table), and filters that name dates relative to now, the file's
name or a formula. A filter left out widens the view, and the warning says
so. `file.hasLink(this)` names the base itself, and gosidian does not
follow links to a base, so its view lists nothing. `file.ext == "md"`,
always true of a note, goes silently.

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
