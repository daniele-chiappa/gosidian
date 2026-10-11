# Diagrams

A diagram is a block of YAML in a note: what it shows (the nodes, the
branches, the sets, the tasks), with its positions on the side when an
editor sets them (ADR-036, IMP-169). The YAML is the source of truth: an
agent reads and writes it as text, the server checks it, and the
drawings, the editor and the conversions to Mermaid, JSON Canvas and SVG
all start from it.

````markdown
# Arrival of a document

```diagram
kind: flow
nodes:
  - {id: upload, label: Upload, shape: pill}
  - {id: convert, label: docling.convert, link: "[[team/pipelines/arrivals]]"}
  - {id: note, label: The note, shape: doc}
edges:
  - "upload -> convert"
  - "convert -> note: markdown"
```
````

**Where diagrams go**:
- in a ```` ```diagram ```` block inside any note;
- or in a note with `type: diagram` in its frontmatter that holds one such block. Other notes embed it with `![[team/diagrams/arrivals]]`, link it and find it like any note.

**What the server does with them**:
- it checks every block, both when a note is written and in the lint rule `diagram-invalid`;
- it counts the `[[links]]` of the nodes as links of the note, so they show as backlinks;
- it says each diagram in words to the agents (`memory_get`).

**What the web UI does with them**: it draws them, with
[plancia-diagrams](https://github.com/daniele-chiappa/plancia/tree/main/diagrams),
a package of the plancia repository.
- A **flow** and a **tree** are drawn with Vue Flow, laid out by dagre in their `direction`, at the positions of `layout:` where it gives some. They pan by dragging and zoom with their buttons.
- A **mind map** and a **fishbone** are drawn by Mermaid.
- A **Venn**, a **matrix** and a **Gantt** show their text until they are drawn too.
- A node with a `[[link]]` opens its note, at the heading the link names; an address opens in a tab. `POST /api/v1/preview` returns the diagrams' `[[links]]` resolved as the note's other links are, in `diagram_links` (the target as written → the note's path).
- **Code** shows the YAML as written; **Export SVG** saves the drawing in the colours of the theme, light or dark.
- What is wrong in a block is listed above the drawing, which leaves those parts out.
- A block at the top level of a note is drawn; one inside a list or a quote stays its code.

**Editing in the web UI**: **Edit**, in the head of a drawn diagram (for
who may change the note), opens the diagram window beside the note. A
diagram embedded with `![[…]]` opens in the note it is written in; the
window's address keeps the note and the block's index, so a reload opens
it again.
- A **flow** is edited on its drawing: drag a node (the first drag fixes
  every node where it is, in `layout:`), add a node, join two by dragging
  from the dot of one onto the other, delete what is selected with Delete;
  a panel sets a node's label, shape, class, link (with the notes found as
  you type) and group, and an edge's label and line; **Lay out again**
  empties `layout:`.
- A **tree**, a **mind map** and a **fishbone** are edited as indented
  lines beside their drawing: Enter adds a branch after, Tab and Shift+Tab
  change its level, Alt+↑ and Alt+↓ move it, Backspace on an empty line
  takes it away, a line dragged by its grip goes after the one it is
  dropped on; a branch's link is set below the lines.
- The **YAML** tab edits the text itself, every kind included (a Venn, a
  matrix and a Gantt are edited there for now), and the problems are
  listed under both tabs, line by line.
- Ctrl+Z and Ctrl+Shift+Z undo and redo; **Save** (Ctrl+S) writes the
  block in place of the old one, and nothing else of the note.
- The editor changes only what it edits: comments, short forms and the
  order of the fields stay as written, what it adds is in the full form.
- A note changed since the window opened gets a banner: reload (the draft
  goes), or put the diagram in the new version of the note, whose other
  changes stay.

**A new diagram**: the **+** of a folder in the tree has the kind
**Diagram**, which makes a note `type: diagram` from a small template of
the kind chosen, opened in the editor.

## Common fields

| Field | |
|---|---|
| `diagram` | the format, `1`; it may be left out |
| `kind` | `flow`, `tree`, `mindmap`, `fishbone`, `venn`, `matrix`, `gantt` |
| `title`, `description` | text, both optional |
| `layout` | optional, written by an editor: `id: {x, y, w, h}` for the ids of the diagram, `x` and `y` the top left corner and `w`, `h` the size, in pixels. Without it the drawing lays itself out; an agent can leave it alone |

An **id** is a letter, then letters, digits, `-` and `_`, at most 40, and
is used once in a diagram. A **link** is a `[[note]]` (or
`[[note#Heading|text]]`) or an `http(s)://` address. A `[[note]]` link opens the note,
and it counts as a link of the diagram's note. A diagram holds at most
2,000 elements and 256 KiB of YAML.

## Flow

Boxes and arrows: procedures, architectures, states, and the pipelines' graph.

| Field | |
|---|---|
| `direction` | `TB` (default), `LR`, `BT`, `RL` |
| `nodes` | `{id, label, shape, link, class}`; a plain text is a node whose id and label are that text |
| `shape` | `box` (default), `pill`, `decision`, `data`, `doc`, `db`, `circle`, `note` |
| `class` | `user`, `data`, `ext`, `planned`, `off`: what the box stands for, as in the diagram skill's legend |
| `edges` | `{from, to, label, style}`, or the text `"from -> to"` or `"from -> to: label"`; `style` is `solid` (default), `dashed` or `dotted`. An edge may join groups too |
| `groups` | `{id, label, nodes: [ids]}`; a node is in one group at most |

## Tree, mind map, fishbone

The three are one shape of data, a root with branches, drawn three ways.

```yaml
kind: mindmap
root: Archive
children:
  - Arrival: [PDF, DOCX, Images]      # a branch: {Label: [children]}
  - Search:
      - Text                          # a leaf: a text
      - {label: Similarity, id: sim, link: "[[team/rag]]", children: [Embeddings]}
```

- `root` is a text or `{id, label, link}`; `children` beside it are its branches (or `root: {label, children}`).
- A branch is a text (a leaf), `{Label: [children]}`, or `{id, label, link, children}`. Up to 20 levels.
- `tree` also takes `direction`.
- In a **fishbone** the root is the effect (the problem), the first level the categories (the 6M: methods, machines, materials, manpower, measurement, environment, or yours), the levels below the causes.

## Venn

```yaml
kind: venn
sets: [Invoices, Contracts, {id: q, label: Quotes}]
areas:
  - {sets: [Invoices, Contracts], label: Framework agreements, size: 3}
```

2 to 5 sets. An area names the sets it is shared by, with a label and a relative `size`, and each combination appears once.

## Matrix

Either quadrants, a 2×2 with points:

```yaml
kind: matrix
x: {label: Urgency, low: Not urgent, high: Urgent}
y: {label: Importance, low: Not important, high: Important}
quadrants: [Plan, Do now, Drop, Delegate]   # top left, top right, bottom left, bottom right
items:
  - {label: Release, x: 0.8, y: 0.9}        # x and y from 0 to 1
```

or a grid of cells (SWOT, comparisons):

```yaml
kind: matrix
rows: [Internal, External]
columns: [Helpful, Harmful]
cells:
  - [Strengths, Weaknesses]
  - [Opportunities, Threats]
```

## Gantt

```yaml
kind: gantt
sections:
  - label: R1
    tasks:
      - {id: runner, label: Job runner, start: 2026-10-08, end: 2026-10-09, status: done}
      - {id: nodes, label: Base nodes, after: runner, days: 1}
  - label: R2
    tasks:
      - {label: Release, after: nodes, status: milestone}
```

- `tasks` without `sections` is one section.
- A task has a `label`, then `start` (YYYY-MM-DD) or `after` (a task's id, and no chain may come back to itself), then `end` or `days`. A `milestone` needs neither.
- `status` is `done`, `active`, `critical` or `milestone`.

## Checks

A block that does not read gives:
- **when written**, a notice on the MCP call with the problems on the note's lines. The note is saved as written;
- **in the lint**, an issue `diagram-invalid` (warning) per problem, on its line, and one for a note `type: diagram` without exactly one block;
- **in `memory_get`**, each diagram's `problems`.

`memory_get` returns `diagrams: [{kind, title, line, text, problems}]` for a note with diagrams. `text` is the diagram in words, for example `Flow (TB), 3 nodes, 2 edges: Upload → docling.convert; docling.convert → The note (markdown)`. It is also the drawing's text alternative. `memory_bootstrap` gives the format in `capabilities.diagrams`.

## The examples

`docs/diagrams/examples/valid/` holds one example of each kind at least.
`docs/diagrams/examples/invalid/` holds a broken one per problem, its first
line naming the problem. `expected.json` says what the server's reader makes
of each one: its problems, on their lines, and its text
(`UPDATE_EXPECTED=1 go test ./internal/diagram` writes it again). The
server's tests read them all, and the web UI's run plancia-diagrams' reader
on them against `expected.json`: they are the format's contract, and the two
readers give the same messages on the same lines.

## In Obsidian

Obsidian shows a ```` ```diagram ```` block as code. The editor will
optionally save a twin beside it: an SVG attachment, or a ```` ```mermaid ````
block, which Obsidian draws.
