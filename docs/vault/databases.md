# Database notes — one note per entry, with a schema

A long note with one section per entry (a backlog, a bug tracker, a list
of decisions) is easy to append to and hard to read: an agent asked "what
is still open?" has to page through it, and usually trusts a summary list
kept by hand instead. A **database note** turns such a list into rows:
each entry is its own note, its status, priority and dates live in its
frontmatter, and `memory_query` answers the question in one call.

Nothing needs switching on. A project without database notes works
exactly as before.

## The database note

A database note is a note with `type: database`. Its frontmatter names
the folder that holds its rows (`source`) and declares their fields:

```yaml
---
title: Improvements backlog
tags: [myproject, type:index]
type: database
source: myproject/docs/improvements
fields:
  id: {type: text, required: true}
  title: {type: text, required: true}
  status: {type: select, required: true, options: [open, in-progress, deferred, done, superseded, rejected]}
  priority: {type: select, options: [low, medium, high]}
  created: {type: date}
  closed: {type: date}
  release: {type: text}
---

# Improvements backlog

How to find, add and close entries: the examples agents follow.
```

Each row is a note **directly inside** the source folder
(`myproject/docs/improvements/IMP-127.md`), with the values in its own
frontmatter and free text in its body. When the folder also holds notes
that are not rows, an index say, `rows` narrows the rows to the notes with
given values: `rows: {type: plan}` keeps the notes with `type: plan`, or a
`type:plan` tag when the field is missing. Those keys need no declaring; a
view of the folder lists only the rows, and a new row starts with them. The body of the database note is
the place for instructions: the `memory_query` calls that list open
entries, how a new ID is chosen, how an entry is closed.

### A template for new rows

`template` (optional) names the note a row added from the web UI starts
from: a vault path, with or without `.md`, or a `[[wikilink]]` written as
a path. It must be a note of the same project, outside the source folder
(there it would be a row).

```yaml
template: myproject/templates/improvement
```

```markdown
---
title: "{{ID}} — {{TITLE}}"
status: open
created: "{{TODAY}}"
tags: ["{{PROJECT}}", type:doc]
---

# {{ID}} — {{TITLE}}
```

The placeholders are those of the project templates, `{{PROJECT}}` and
`{{TODAY}}` (UTC), plus `{{ID}}` (the row's name) and `{{TITLE}}`. Quote
them in the frontmatter, as above: unquoted, `{{` is not valid YAML. They
are filled field by field, so a title holding a colon or quotes keeps the
frontmatter valid. Without a template a new row gets its title, its `id`,
the values given and the project's tag, over a heading.

### Field types

| Type | Accepts |
|---|---|
| `text` | any scalar |
| `number` | an integer or a decimal |
| `date` | an ISO date (`2026-10-02`) or date-time |
| `checkbox` | `true` or `false` |
| `select` | one of `options` |
| `multi-select` | a list of values from `options` |
| `url` | an `http` or `https` URL |
| `relation` | a `[[wikilink]]`, or a list of them |
| `list` | a list |
| `rollup` | nothing: it is computed when the row is read ([below](#rollups)) |

`required: true` makes a field mandatory. `title` and `tags` may appear on
any row without being declared. A field named `id` must match the note's
file name, which keeps links to a row short and stable
(`[[myproject/docs/improvements/IMP-127]]`).

### Relations

A `relation` field holds `[[wikilinks]]`, quoted in the frontmatter
(`related: ["[[myproject/docs/bugs/BUG-089]]"]`). Like any wikilink in a
frontmatter value it is a link: the note it points at lists the row in
its backlinks, with the field's name, the graph draws it, and a rename
rewrites it. A view finds the rows that point at a note with `related
contains this` or `related contains [[note]]`, however the link is
written ([views](views.md#relations)).

### Row views

`row_views` (optional) lists views that every row shows below its body,
in which `this` is the row: what points at it, or anything else a view can
list. Each entry has a `title` and the keys of a [view](views.md) block.

```yaml
row_views:
  - title: Plans that implement it
    from: myproject/plans
    where: [implements_imp = this.id]
    columns: [title, status]
  - title: Everything that links here
    from: [myproject/plans, myproject/docs/improvements]
    where: [links contains this]
    as: list
```

Nothing is written to the rows. A database declares at most 8 of them,
and only those it declares show: there is no automatic one.

- **Web UI** — under the body of a row, one section per view, which folds
  and is left out when it lists nothing. A table or a board edits the
  rows it lists, as in a note. `GET /api/v1/notes/<row>/row-views` serves
  them: each with its `title`, the view as data (`view`, as `POST
  /api/v1/preview` returns a note's views) and its rows as `html`.
- **Agents** — `memory_get` and `memory_get_section` of a row with
  `render_views: true` append them after the text, between
  `gosidian:row-views` markers, each title in bold and its rows as a view
  shows them. Without `render_views` the `hint` says how many there are.
  They are not part of the bootstrap.
- **Lint** — `database-field-invalid` reports a row view that would not
  compute, on the database note.

### Rollups

A `rollup` field is computed for each row when it is read, and never
written to it: a [view](views.md) spec in which `this` is the row, whose
notes it counts, or sums, or reduces to their least or greatest value of
one field.

```yaml
fields:
  plans:
    type: rollup
    from: myproject/plans
    where: [implements_imp contains this]
  open_plans:
    type: rollup
    from: myproject/plans
    where:
      - implements_imp contains this
      - status in [draft, in-progress]
  effort:
    type: rollup
    from: myproject/plans
    where: [implements_imp contains this]
    calc: sum
    of: estimate
  first_due:
    type: rollup
    from: myproject/plans
    where: [implements_imp contains this]
    calc: min
    of: due
```

- **`calc`**: `count` (the default), `sum`, `min` or `max`; the last three
  read the field named by `of` in the notes the spec selects. `min` and
  `max` compare numbers when every value is one, otherwise text, which
  orders ISO dates too; with no value they give nothing. A sum counts the
  numbers only.
- **The other direction**: `path in this.implements_imp` selects the notes
  the row's own relation points at; add `status = done` and the rollup
  counts those of them that are done. A row without the relation counts 0.
- **Where it shows**: as a column of a view (table, list, board), in
  `memory_query` and `POST /api/v1/query` over the rows of the database
  (`from` its source) when `fields` or `sort` names it, and read-only in
  the web UI, in the table and the property panel. Each value is computed
  with the reader's scope: a note the reader may not see does not count.
- **Sorting**: a view or a query sorted by a rollup reads every row,
  computes and sorts them, and keeps the limit; it reads at most 500 rows,
  and asks to narrow the view beyond that. The rollup can be one key of
  several (`sort: plans desc, open_plans desc, id asc`), first or to
  break the ties of a field: every key is then sorted with it, the same
  way the index sorts. A `where` or a `group_by` on a
  rollup is refused: filter on the fields it counts instead.
- **One database at a time**: a rollup is computed over the rows of its
  database, so a query over several folders (`from: [a, b]`) that names
  one in `where`, `fields` or `sort` is refused, and says which folder to
  query alone. In `memory_query` and `POST /api/v1/query` a rollup's value
  is a number.
- **Not written**: a row that writes a rollup field is reported by
  `database-field-invalid`, and an edit that sets one is refused.

## Automations

A database note can declare rules that act at a time, under
`automations:`. They never act on a write, so no rule can set another
off.

```yaml
automations:
  - name: Deadlines near
    due: due              # a date field of the rows
    before: 3             # days ahead; 0 (the default) is the day itself
    where:
      - status in [open, in-progress]
    handoff: myproject-dev
    message: Look at these before the end of the week.
  - name: Monday backlog
    every: monday 09:00   # a day of the week, or "day 09:00" for each day
    snapshot: hot         # a note of the project
  - name: Weekly review
    every: friday 16:00
    handoff: myproject-dev
    message: Review the open bugs.
```

- **When**: a `due` rule watches a date field of the rows. A row comes
  in on the day its date is `before` days away, or later, so a row
  already past its date comes in too, and the rule acts once for it. It
  acts again for that row only if the date changes. An `every` rule acts
  once per slot, from the first slot after the server first saw it; a
  slot missed while the server was down acts once, at the next run.
  Italian day names work too (`lunedì 09:00`). A rule reads up to
  10,000 rows, in path order: past that, its runs and its dry run carry a
  `warning` and the server log says so once; narrow its `where`.
- **What**: `handoff` writes a handoff note to that agent in the
  project's `handoffs/` folder, from `automation`: for a `due` rule it
  lists the rows that came in, each with its date and how far it is
  (`in 3 days`, `today`, `2 days late`), all in one handoff per run.
  `snapshot` freezes the note as [`memory_snapshot`](views.md#snapshots)
  does.
- **Where**: the rules read and write as the server's own identity,
  `automation`, inside the project of the database note only. Their views
  and snapshots see what that project holds. The audit log names
  `automation` as the author, and `created_by` says so.
- **Seen by the agents**: `memory_bootstrap` lists the project's pending
  handoffs in `pending_handoffs`, the automation's among them, and the
  directives ask agents to deal with them first.
- **Tried before trusted**: `memory_automations(project, as_of?)` is a dry
  run. It writes nothing and answers "what would fire on 2026-12-01?":
  each rule, whether it fires at `as_of`, the rows it would hand off or
  the slot it would fire, when it acts next, the rows to come with their
  day, those already handed off, and the last runs. `run: true` runs the
  project's rules now.
- **Checked**: `database-field-invalid` reports a rule that does not
  parse, a `due` that is not a date field, a `where` that does not
  compute and a `snapshot` note that does not exist. The engine skips such
  a rule and keeps the others.
- **Run**: every 5 minutes (`automations.interval`), with the days and
  times in `automations.timezone`
  ([configuration](../configuration.md)). What they did is kept in
  `automations.json` in the state directory.

## What checks the rows

- **Lint** — the `database-field-invalid` rule (on by default, warning)
  reports a row with an undeclared field, a missing required one, a value
  of the wrong type or an `id` that differs from the file name, and a
  database note whose schema does not parse.
- **Writes** — `memory_create`, `memory_update`, `memory_edit`,
  `memory_append` and `memory_ingest` add a note to their result
  (`notices`) when the note just written breaks its schema, naming the
  problem and the declared fields. The write is not refused: the agent
  fixes the frontmatter with `memory_edit`.
- **Field edits over HTTP** — `PATCH /api/v1/notes/<path>/frontmatter`
  (below) refuses with 422 a value that breaks the schema for the fields
  it touches. A row that already breaks the schema elsewhere can still be
  edited one field at a time.
- **Bootstrap** — `memory_bootstrap` lists the project's databases in
  `databases`, each with its path, source folder, row count, fields and
  `template` when it names one, so an agent knows the schema and the
  model of a row before it writes.

The index reads row frontmatter as it reads any note, so `memory_query`
filters and sorts rows by their fields:

```json
{"project": "myproject",
 "where": [{"field": "id", "op": "contains", "value": "IMP-"},
           {"field": "status", "op": "in", "value": ["open", "in-progress"]}],
 "fields": ["id", "title", "priority"], "limit": 200}
```

A note can also show rows live with a [view](views.md) block, for
instance the open entries in `hot.md`.

## Editing rows in the web UI

- **In a table view** — on a row you may write, a cell of a field the
  schema declares turns into its editor on click or Enter: the options of
  a select, a date picker, a number, checkboxes for a multi-select, text
  for the rest (lists comma-separated), a note picker for a relation. A
  checkbox toggles at once. Values with `[[wikilinks]]` show as links.
- **In a row's own note** — a property panel above the body lists the
  fields of the schema, with the same editors, and the note's other
  fields, read-only. The `id` is shown, not edited: it is the file name.
  `GET /api/v1/notes/<path>/fields` serves it.

Both save one field at a time with the call below, checking the value
shown when the editor opened: if someone changed that field since,
nothing is overwritten and the editor says so.

- **New rows** — a table view of a database has "New row" under it, and
  each column of a board a "+", for a reader who may write the project.
  The form suggests the row's name from the numbering of the rows (the
  most common prefix, the highest number plus one, zero-padded alike:
  `IMP-137` gives `IMP-138`), which can be changed; it asks for the title
  and for the required fields nothing else gives. The row starts with the
  values the view's `eq` and `in` filters ask (the first value of an
  `in`) and, on a board, with the column's value, so it shows where it
  was made. The new note opens in a window.

`GET /api/v1/notes/<database>/new-row` returns the suggested `name`, the
schema's `columns` and the fields the template sets (`preset`). `POST
/api/v1/notes/<database>/rows` with `{"name", "title", "values"}` writes
the row from the template and answers 201 with the note. The whole row
must match the schema (**422** with `details.problems`), so a row made
this way passes lint; a name already taken answers **409** with the next
one in `details.name`. Both need write access to the project.

## Editing fields over HTTP

`PATCH /api/v1/notes/<path>/frontmatter` writes and removes frontmatter
keys of a markdown note without sending the whole note. It rewrites only
the lines of the keys named: order, comments, quoting and the body stay as
written, and a new key goes after the others, in the order the schema
declares them, or just before `tags` when that is the last key, as the
vault's notes are written.

```json
{"set": {"status": "done", "closed": "2026-10-03", "labels": ["ui"]},
 "unset": ["priority"],
 "expect": {"status": "open"}}
```

- `set` takes a string, a number, a bool, `null` or a list of strings for
  each key; `unset` lists keys to remove.
- `expect` gives, for some keys, the value the client last saw. A key
  whose value has changed since answers **409**, with the current values
  in `details.fields`. A change to another key of the same note in the
  meantime does not make the edit fail. For the strict check on the whole
  note, send its etag in `If-Match` or `if_match` (**412** on a mismatch,
  as for `PUT`).
- On a row of a database, a value that breaks the schema, an undeclared
  field, emptying or removing a required one, or an `id` that differs
  from the file name answers **422**, with the problems in
  `details.problems`.
- A value is written only in a form the vault reads back unchanged. A
  list item may hold a comma (it is quoted) when the note's frontmatter is
  valid YAML; one that cannot be read back whole, such as an item starting
  with `#` or with a quote, answers **422**. A key whose current YAML is
  too complex to edit line by line (a nested map, a multi-line value, an
  anchor, a key written twice) answers **400**; edit the note as text
  instead.
- The call needs write access to the project. It answers 200 with the
  note and its new `ETag`; an edit that changes nothing leaves the note
  and its etag as they were.

## Backlogs and the directives

The operational directives served by `memory_bootstrap` (v14 and later)
know about database backlogs. When a project's `docs/improvements.md` or
`docs/bugs.md` is a database note, agents file a new entry as its own
note in the folder of the same name, read what is open with
`memory_query` rather than a list kept by hand, keep `hot.md` free of such
lists, and write only the declared fields. A backlog kept as one note with
a section per entry keeps the old instructions.

## Moving an existing backlog

There is no built-in converter yet. A migration splits each `## ID —
title` section into `<source>/<ID>.md`, moves the section's status and
dates into the frontmatter, keeps the body as it was, rewrites links of
the form `[[…/improvements#IMP-127]]` to `[[…/improvements/IMP-127]]`,
and turns the old note into the database note. Do it with the server
stopped, or through the MCP write tools, so the index stays in step; the
vault's git history keeps the old note.
