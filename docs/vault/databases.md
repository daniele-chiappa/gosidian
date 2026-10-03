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
frontmatter and free text in its body. The body of the database note is
the place for instructions: the `memory_query` calls that list open
entries, how a new ID is chosen, how an entry is closed.

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

`required: true` makes a field mandatory. `title` and `tags` may appear on
any row without being declared. A field named `id` must match the note's
file name, which keeps links to a row short and stable
(`[[myproject/docs/improvements/IMP-127]]`).

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
  `databases`, each with its path, source folder, row count and fields,
  so an agent knows the schema before it writes.

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

## Editing fields over HTTP

`PATCH /api/v1/notes/<path>/frontmatter` writes and removes frontmatter
keys of a markdown note without sending the whole note. It rewrites only
the lines of the keys named: order, comments, quoting and the body stay as
written, and a new key goes after the others, in the order the schema
declares them.

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
- A value is written only in a form that every reader of the vault reads
  back unchanged. One that has none, such as a list item containing a
  comma, answers **422**. A key whose current YAML is too complex to
  edit line by line (a nested map, a multi-line value, an anchor, a key
  written twice) answers **400**; edit the note as text instead.
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
