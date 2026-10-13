# Graph views — notes that save a view of the graph

A ```` ```graph ```` block saves a view of the graph: its **source**, its
filters, how its nodes are coloured, 2D or 3D. The web UI draws it in the
note, with **Open in the Graph** to open the same view in the Graph
window, and **Code** to see the block. A note `type: graph` is a saved
view: it holds one block.

````markdown
---
title: Map of the procedures
type: graph
---

```graph
source: vec-pg/chunks
with: {collection: procedure, k: 8, sample: 3000}
color: doc
mode: 3d
```
````

## Sources

| `source` | What the graph shows |
|---|---|
| `links` (the default) | the vault's wikilinks, as the Graph window shows them, with the filters `project`, `tag`, `focus` and `depth`, `min_degree`, `limit` |
| `<plugin>/<graph>` | a graph a [plugin](../plugins.md#graphs) offers, in the projects its owner exposed it in, asked for the note's project (or `project`); `with` holds its arguments, `color` names what groups its nodes |

A plugin's graph of an archive of vectors, say, links each chunk to its
nearest neighbours: similar chunks pull together, and a click shows the
chunk's text and opens the note it comes from. The server keeps a
plugin's graph for 15 minutes; **Compute again** in the Graph window asks
the plugin again.

## Fields

| Field | |
|---|---|
| `source` | `links`, or `<plugin>/<graph>` |
| `with` | a plugin's graph's arguments, as its schema declares them |
| `project` | the links: the project to show; a plugin's graph: the project it is asked for (default: the note's) |
| `tag`, `focus`, `depth` (1 to 6), `min_degree`, `limit` (up to 5000) | the links' filters |
| `color` | what groups the nodes; for a plugin's graph, one of its arguments |
| `mode` | `2d` (default) or `3d` |

The server checks the block when a note is written (a note on the call
says what does not read) and in the lint (`graph-invalid`). **Save as a
note** in the Graph window writes the block of the view on screen.
