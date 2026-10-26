---
title: Design a project's databases
description: When a collection of notes should become a database note, and how to design and fill one with the MCP tools — inventory, criterion, schema, relations, views, rows (a backlog written as sections included) and checks.
tags: [{{GLOBAL}}, type:skill, topic:vault]
type: skill
updated: 2026-10-22
---

# Design a project's databases

## Trigger phrase

- "turn `<project>` into databases where it makes sense"
- "make the backlog of `<project>` a database", "catalogue the stacks (hosts, certificates…) of `<project>`"
- A note keeps a hand-written list or table of items with a state, or a backlog grows past the 24 KiB that `memory_get` serves whole.

## Context

A **database note** (`type: database`) names a source folder and declares the fields of its rows. Each row is a note directly inside that folder, with its values in the frontmatter and free text in the body. `memory_query` answers "what is open", "what expires", "which proxy hosts belong to this stack" in one call, views keep `hot.md` and the indexes current without hand-kept lists, and the bootstrap lists the project's databases in `databases[]`, so every agent writes new items as rows.

The mechanics are easy. The judgement is not: which collections deserve a database, which fields they need, and keeping each fact in one place, so that a field and a tag never tell two stories.

**Versions.** gosidian ships this skill from 2.107.0, which also brings the lint rule `field-tag-mismatch`; the warning about reserved field names comes with 2.106.1. On an older server the advice holds, but nothing checks it: look for those mistakes yourself. Do not copy this note by hand into a global project under the same name: the server seeds it only where it is missing, so a hand-made copy would stay in place of the shipped one after an upgrade.

## Steps

### 1. Inventory the collections

- Read `hot.md`, the README and the index notes of the project, and list its **collections**: notes or sections of the same kind that grow. Typical ones are backlog entries (improvements, bugs), plans, inventories (stacks, hosts, proxy hosts, certificates, domains, licences) and deadlines.
- For each one, note how many items there are, which fields recur, and what people ask about them: what is open, what expires, what belongs to what.

### 2. Decide: database or text

- **A database** when the items are of one kind, keep coming, and have a state or fields you filter, sort or count, or links between items that you want to follow from either side.
- **Text** for a chronology (`log.md`, append-only), decisions (ADRs as sections), gotchas, `hot.md`, the architecture, and a list written once that will not grow.
- **Plans** become a database when you want them linked to the backlog: which plans implement an entry, how many of them are open. Their `status` is then a field, and a `status:*` tag, if the project keeps one, only mirrors it (lint `field-tag-mismatch` catches the two drifting apart).
- Show the user the proposal before writing anything: the collections, database or text for each, and the fields.

### 3. Schema

The database note's frontmatter declares `fields` as a **map** of name to definition:

```yaml
---
title: Stacks
tags: [<project>, type:index]
type: database
source: <project>/docs/stacks
template: <project>/docs/templates/stack
fields:
  title: {type: text, required: true}
  host: {type: text, required: true}
  folder: {type: text}
  status: {type: select, required: true, options: [running, stopped, retired]}
  image_tags: {type: list}
  checked: {type: date}
---
```

`databases[].fields` in the bootstrap is a **list** of `{name, type, required}`, kept in declaration order: do not copy that shape into a note, which then does not parse.

Field types: `text`, `number`, `date`, `checkbox`, `select` and `multi-select` (with `options`), `url`, `relation` (wikilinks), `list`, `rollup` (computed when read). `title` and `tags` need no declaring.

Rules that keep a database honest:

- **One fact in one place**: a field, a tag or the body, not two of them.
- **Names gosidian already uses are reserved**: `path`, `name`, `modified` and `links` are computed from the note, and `created_by`, `modified_by`, `created_at` and `modified_at` come from the audit. Call a filesystem folder `folder` or `dir`. Lint and the write warn about a reserved field.
- **Data copied from a system** (containers, ports, certificates, versions) ages: give it a `checked: {type: date}` field and update it whenever you look again.
- **`due` only for a real deadline**: a certificate's expiry, a delivery the user named. Never an estimate.
- **No counts written into rows**: count with a `rollup` field or a view.
- An `id` field must match the file name. When the source folder also holds other notes (a README), `rows: {type: <value>}` narrows the rows.
- A **template** note, outside the source folder, gives new rows their frontmatter and body outline. Quote its placeholders: `title: "{{ID}} — {{TITLE}}"`. Only `{{ID}}`, `{{TITLE}}`, `{{TODAY}}` and `{{PROJECT}}` are filled in. Leave **relation fields out of the template** and add them to the new row: a placeholder wikilink such as `[[…/<stack>]]` is a broken link for lint, and any other `{{PLACEHOLDER}}` stays in the row as written, where lint, which skips placeholders, does not see it.

The body of the database note says how to find, add and close a row, with the exact calls (`memory_query` with `from: <project>/docs/stacks`), and that rows use only the declared fields, with details in the body.

### 4. Relations

- A field that points at another note is a `relation`, written as a quoted wikilink. A proxy host row names its stack: `stack: "[[<project>/docs/stacks/app]]"`.
- The other side shows what points at it with a **row view** in the database note, computed under every row:

```yaml
row_views:
  - title: Proxy hosts
    from: <project>/docs/proxy-hosts
    where: [stack contains this]
    columns: [title]
```

- To count instead, add a rollup field: `proxy_hosts: {type: rollup, from: <project>/docs/proxy-hosts, where: [stack contains this]}`. A rollup is computed when rows are read: `memory_query` and views return it in `fields` and sort by it (reading at most 500 rows), but refuse it in `where`. Filter on the fields it counts instead.

### 5. Rows

- **A few rows**: one `memory_create` per row, starting from the template. A new ID is the highest one plus one; if the note exists already, take the next.
- **Many rows, or rows prepared locally**: write them as files in a folder and import the folder in one call, `memory_ingest` with `as: "package"` and `dest: "<project>/docs/stacks"`, first with `dry_run: true`. A remote agent zips the folder and uploads it with `transfer: "http"`.
- **Data from a system**: write what you read and when (`checked`), never volatile numbers such as a container count, which the next look makes wrong.
- **Migrating a backlog written as sections** (`## IMP-012 — title` inside one note):
  1. review it with the user first: facts that evidence settles (a release, a commit, a closing note) in one list, judgements (priority, still relevant?) in another, which the user decides;
  2. write one row per entry, merging any later "closed" addendum into its entry, with the section text unchanged in the body;
  3. rewrite the links `[[<project>/docs/improvements#IMP-012…]]` into `[[<project>/docs/improvements/IMP-012]]`, keeping any alias (find them with `memory_backlinks` and `memory_search`);
  4. turn the old note into the database note, with a "History" section saying where the previous text lives (the vault's git history).

### 6. Views instead of hand-kept lists

- In `hot.md`, a view of what is open, and `as: count` for a number; in the README, the whole index. A section made of views needs no rewriting at the end of a task.
- A "to check again" view: `where: [checked < today-90d]`.
- Deadlines: an `automations:` rule on `due` with `before:` sends a handoff before the date. Write one only when the user asks, and try it first with `memory_automations` and `as_of`.
- Decisions that came up along the way, such as an image left on `latest` or a port open to every interface, become **backlog rows**, not just a line in your report.

### 7. Check

- `memory_lint` of the project: `database-field-invalid`, `field-tag-mismatch`, `broken-wikilink`, `broken-anchor` and `frontmatter-invalid-yaml` at 0.
- `memory_query` of the open rows: the count matches the review.
- `memory_bootstrap`: each database is in `databases[]`.
- Read one row with `render_views: true`: its row views show what points at it.

### 8. Log

One entry in the project's `log.md`: the databases created, how many rows each, the links rewritten, the user's decisions, and the vault commit from before the change.

## Gotcha

- **`fields` written as a list**, copied from the bootstrap: the schema does not parse, and the write's notice says so. It is a map.
- **Unquoted values** holding `: `, a `[[wikilink]]` or a `{{placeholder}}` are not valid YAML: quote them.
- **A field named `path`** (or another reserved name) is shadowed by what gosidian computes, so views and queries do not see it.
- **A tag left behind**: change `status: done` but keep the tag `status:in-progress`, and the tag misleads whoever reads tags. Lint reports it.
- **Invented fields** (`resolved:` where the schema has `closed:`) are missed by every query on the declared field; lint reports them.
- **Never skip the review** of a backlog: entries closed elsewhere stay open in the database, and a query states them as true.
- **The write rate limit** is per MCP session: when a write is refused, the message says when to retry. Wait, then repeat the same call.

## Rollback

With git sync on, the vault's history holds every note as it was: the commit before the change restores the old notes. Rows created by mistake go away with `memory_delete` (into the trash when it is on), and a database note becomes an ordinary note again when `type: database` comes out of its frontmatter.

## Idempotency

Partial. Creating rows can be resumed: `memory_create` fails on a row that exists, and that is fine. Rewriting links and replacing the old note are done once.
