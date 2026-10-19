# Web UI overview

The built-in web interface is a **Vue 3 single-page app**, built with
Vite and embedded in the binary via `go:embed` — no separate web server,
no CDN. It talks to the Go backend over a JSON REST API (`/api/v1/*`)
and an SSE stream for live updates.

## The plancia (window manager)

Since v2.3 the UI is a **"plancia"**: a niri-style scrollable tiling
window manager. Instead of one page at a time, notes, the graph, search,
and config forms open as **windows** side by side in a horizontally-
scrollable strip.

- **Open** a note from the sidebar tree, a wikilink, search, or the
  command palette (`⌘K` / `Ctrl-K`); each opens to the right of the
  focused window.
- **Resize** in discrete steps (small → medium → full) with the window's
  resize button.
- **Minimize** a window to a horizontally-scrollable footer; click to
  restore.
- **Direct links**: the window's link button opens an *ego-graph* — the
  one-hop neighbourhood of that note — as its own window.
- **Edit in place**: a note window has a View/Edit toggle; the editor
  mounts lazily in the same window. Where nothing can be edited (a
  read-only account, a base, a canvas) there is no toggle.
- **Navigate** focus between windows with `Alt-←` / `Alt-→`.
- **Confirm**: a delete, a revoke or a rename asks in gosidian's own
  dialog, in the language of the UI: `Enter` confirms, `Esc` cancels.
  Closing a window with unsaved changes still asks with the browser's
  dialog.

The open windows + focus are encoded in the URL (`?w=…&f=…`), so a
workspace is shareable and survives reload; when the URL is empty the
last workspace is restored from `localStorage`.

## Deep-link routes

These canonical routes still work as entry points — visiting one opens
the matching window in the plancia (so existing links and bookmarks keep
working):

| Route | Opens |
|---|---|
| `/notes/<path>` | the note (view; `…/edit` opens in edit mode) |
| `/notes/<path>/history` | the note's git history |
| `/projects` | project listing & admin |
| `/tags` / `/tags/<tag>` | tag explorer |
| `/graph` | graph of wiki-link relations |
| `/search` | full-text search |
| `/query` | notes by their frontmatter (see below) |
| `/settings` | theme preset, language, git sync |
| `/admin/*` | users, tokens, invites, audit (owner only) |
| `/trash` | soft-deleted notes, folders and projects (if enabled); a note or a folder comes back to its project, for who can write there; a project comes back with the access it had: who could read it sees it, who administered it (or the owner) restores or purges it |

## Query window

**Menu → Query** selects notes by their frontmatter — the same query as
the MCP `memory_query` tool, for people. Each row is a condition
`field · operator · value` (`eq`, `ne`, `in` with a comma-separated list,
`exists`, `lt`/`lte`/`gt`/`gte`, `contains`), and every condition must
hold. A namespaced tag counts as a field when the note has none
(`status:done` → `status = done`), `tags` is the tag list, ISO dates and
numbers compare as such. Optional: a project, a sort (one field, or
several separated by commas, each with its own `asc` or `desc`) with its
default order, the fields to show (by default those of the conditions). Results
are a table; a title opens the note. The whole query lives in the URL,
so it survives a reload and can be shared as a link. The backend is
`POST /api/v1/query`, scoped to the projects the account can read.

## Tree context menu

A right-click on a row of the sidebar tree (or the Menu key, or
`Shift+F10`, on the focused row) opens gosidian's own menu in place of
the browser's:

- **note**: *Download* saves the same copy as the note window's download
  button: the note as it is, or, when it shows images or links to other
  files of the vault, a zip of the note and those files (see below);
  *Delete* moves it to the trash, after a confirmation. Obsidian bases and canvases, which are read-only, only
  download.
- **folder**: *Download* saves a zip of the folder; *Delete* moves the
  whole folder to the trash as one entry, restored in one go. It needs
  the trash: with the trash off the server refuses it.
- **project**: *Download* saves the project zip. A project is deleted
  from the Projects window, which also handles its access.

*Delete* shows only where the account may write. `Esc`, a click outside
or a scroll close the menu.

## Zip export

**Projects → Export** downloads a zip of every file of a project: notes,
attachments, canvases, bases, PDFs, whatever the folder holds. Opened in
Obsidian, the archive is the vault. Hidden files and folders (`.obsidian`,
`.git`, `.gosidian` with the trash and the default state dir),
`node_modules` and symlinks are left out. Any signed-in account that can
read the project may export it; the anonymous guest of open mode may not.
**Admin → Export vault** does the same for the whole vault (owner only).
The zip is built while it downloads, so there is no size limit on the
server side; each account may export 10 times per 10 minutes, and every
export is written to the audit log (`export`, with the bytes sent). The
endpoints are `GET /api/v1/projects/{name}/export.zip`,
`GET /api/v1/folders/{path}/export.zip` (any folder, from the tree's
context menu, names starting at the folder) and
`GET /api/v1/admin/export.zip`, with the usual Bearer.

**The notes keep their images.** A note shows an image or links a file of
the vault by a `/vault-files/…` address, an `![[embed]]`, or the `src` or
`href` of an HTML note; outside gosidian that address leads nowhere. In
every zip (folder, project, vault) those references become paths relative
to the note, `![photo](../attachments/x.png)`, and the files they name come
along: where the zip holds them already (a project's `attachments/`), else
in an `attachments/` folder at the top of the zip, once each. A file of
another project is added only when the account can read it; a reference
the account may not follow, or that names no file, stays as written. The
notes in the vault do not change, and the image notes (`<note>_img/`, with
the descriptions) are in the zip of their folder as before.

A note downloads the same way: `GET /api/v1/notes/{path}?export=zip` gives
a zip of the note and the files it shows (`attachments/…`), or the note as
it is when it shows none. It is a read like any other, without the export
limit or the audit.

## Rendering stack

- **Vue 3** (composition API) + **vue-router** + **Pinia** state
- **Vite** build; output embedded under `internal/server/web/dist`
- **Tailwind CSS** with a semantic-token theme system (presets +
  custom palette)
- **CodeMirror 6** for the editor (markdown, wikilink autocomplete),
  lazy-loaded
- **force-graph** (2D) and **3d-force-graph** for the graph view,
  lazy-loaded
- **plancia-diagrams** for the diagrams of the notes (Vue Flow and dagre,
  Mermaid for mind maps, fishbones, Venns and Gantts, an SVG of its own for
  matrices) and the pipelines' drawing and editor, loaded by the first
  note that shows one ([diagrams](../diagrams.md))
- **vue-i18n** with catalogues precompiled at build time from
  `internal/i18n/catalogs/` (shared with the Go side); a strict CSP
  (`script-src 'self'`, no eval) is enforced
- The Go backend serves only the SPA shell, fingerprinted assets, the
  `/api/v1/*` REST API, and the MCP transports at `/mcp` and `/mcp/sse`

## Diagrams

A ```` ```diagram ```` block of a note is drawn where it stands, and so is
the block of a note `type: diagram` embedded with `![[ ]]`
([diagrams](../diagrams.md)). A flow or a tree pans by dragging and zooms
with its `+`, `−` and fit buttons (the wheel scrolls the note). A node
with a `[[link]]` opens its note, at the heading the link names; an
address opens in a tab. **Code** shows the YAML as written, **Export**
saves the drawing in the theme's colours (SVG), the Mermaid, or a flow's
Obsidian canvas. What is wrong in a block is listed above its drawing,
line by line. **Edit** opens the diagram window, where a flow is edited
on its drawing, a tree, a mind map or a fishbone as indented lines, a
Venn, a matrix or a Gantt as a form (a matrix's points dragged on the
drawing), and every kind in its YAML; **Import…** puts a pasted Mermaid
or canvas in its place; **Mermaid twin for Obsidian** keeps a Mermaid
block under the diagram for Obsidian; **Save** writes the block back in
its note. The **+** of a folder makes a diagram note, empty or from a
ready template. A ```` ```mermaid ```` block of a note and an Obsidian
canvas have **Convert to a diagram**.

## Pipelines

A pipeline note's ```` ```pipeline ```` block is drawn too: its trigger,
its nodes and what each waits for ([pipelines](../pipelines.md#editing-in-the-web-ui)).
**Edit** opens the pipeline window, where a node comes from the palette
of the node types (the base ones and the accepted plugins'), a line
dragged between two makes one wait for the other, and a panel sets a
node's settings as a form made from its type's schema, offering the
`{{ }}` it may read; the YAML tab edits the text, checked by the server as
it is typed; **Save** writes the block back in its note. Where the
pipelines run, the window also starts a run, resumes a waiting one and
cancels one, and draws a run's state on the nodes, live; so does the Runs
window for the run it opens. A pipeline that starts on a commit says
there, while git sync is off, that it starts only by hand.

## The cut view

Where the server runs pipelines, the window of a markdown note has
**Cut**, in view mode: the note as `note.sections` cuts it for an archive
of vectors ([chunks](../pipelines.md#chunks-for-an-archive-of-vectors)).
Each section has a blue bar on its left and each paragraph a cyan one,
with its id and size above it; the text is the one a model reads, each
image small beside its label and description, and one without a
description in red. The note is never written.

- **Rules of the cut** — the collection, the words of the images, the
  longest side of an icon, whether an image on a line of its own is a
  paragraph or goes with the text beside it, whether an icon's label is
  followed by its description's first sentence, what a chunk's text starts
  with, the longest section — are kept per project in the browser. They must be those of the pipeline's `note.sections`, or no
  block matches the archive, and the view says so.
- Given a **collection** of an archive exposed in the project (a plugin
  whose `list` and `collections` are read tools; one that does not answer
  is named under the choice), each bar says what it holds: green the same text, orange another (the note
  changed since it was cut: to cut again), grey nothing; and how many of
  its chunks the note no longer has.
- The **marks** the pipelines put on the note
  ([note.mark](../pipelines.md#marks-on-a-note)) sit on the blocks they
  name: a bar on the right in their colour and their label, their text on
  hover or when the label has the focus. Each set can be hidden; one made on an earlier version of the
  note says so. **Cut** has a dot while the note has marks.

## The IN folder

A project with `use_in` has, at the top of its tree, one **IN** row with
its new files and its files ([the IN folder](../in.md)). A click opens the
**IN window**: the folder a level at a time, its subfolders and its files
with their size, date, status and the run that took them; a search looks
under the folder. Who writes in the project moves the files chosen, sets
them aside or back as new, deletes them into the trash, or starts a
pipeline on them. The window follows the folder live; the address keeps it
(`w=in:<project>`).

## Language switching

The language selector lives in **Settings** and offers the languages the
operator enables (`i18n.enabled_langs`, all of them by default). On first
load the SPA reads the operator's configured `i18n.default_lang`; the
user's later choice is persisted client-side and wins thereafter, as long
as that language stays enabled. Keys missing from the selected language
fall back to English automatically — see
[`web/src/locales/index.ts`](../../web/src/locales/index.ts). Spanish,
French, and German are about three quarters translated; contributing the
rest is documented in [CONTRIBUTING.md](../../CONTRIBUTING.md). MCP tool
output is in English. Details: [Settings → Language](settings.md#language).

## Web login

See [MCP authentication → Web UI login](../mcp/authentication.md#web-ui-login).
Web auth is optional; without it the UI is open on `0.0.0.0:8080` (put
a reverse proxy in front of anything beyond localhost — see
[Deployment](../deployment.md)).
