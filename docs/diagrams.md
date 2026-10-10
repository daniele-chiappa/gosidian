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

**What the server does with them today**:
- it checks every block, both when a note is written and in the lint rule `diagram-invalid`;
- it counts the `[[links]]` of the nodes as links of the note, so they show as backlinks;
- it says each diagram in words to the agents (`memory_get`).

The web UI drawing and the editor come in the next releases of the plan
`20261010-diagrammi`: a flow drawn with Vue Flow and dagre, the other
kinds drawn by Mermaid. Until then a diagram shows as its YAML.

## Common fields

| Field | |
|---|---|
| `diagram` | the format, `1`; it may be left out |
| `kind` | `flow`, `tree`, `mindmap`, `fishbone`, `venn`, `matrix`, `gantt` |
| `title`, `description` | text, both optional |
| `layout` | optional, written by an editor: `id: {x, y, w, h}` for the ids of the diagram. Without it the drawing lays itself out; an agent can leave it alone |

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
line naming the problem. The server's tests read them all, and so will the
web UI's: they are the format's contract.

## In Obsidian

Obsidian shows a ```` ```diagram ```` block as code. The editor will
optionally save a twin beside it: an SVG attachment, or a ```` ```mermaid ````
block, which Obsidian draws.
