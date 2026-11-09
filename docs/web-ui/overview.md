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
  focused window, and the focus goes to the window's title, which a
  screen reader says: `Tab` goes on to its first control. The windows a
  reload brings back leave the focus where it is.
- **Resize** in discrete steps (small → medium → full) with the window's
  resize button.
- **Minimize** a window to a horizontally-scrollable footer; click to
  restore.
- **Direct links**: the window's link button opens an *ego-graph* — the
  one-hop neighbourhood of that note — as its own window.
- **Edit in place**: a note window has a View/Edit toggle; the editor
  mounts lazily in the same window. Where nothing can be edited (a
  read-only account, a base, a canvas) there is no toggle.
- **Drafts**: while you edit, the unsaved text is also kept in this
  browser, per account and note, so a session that ends, a reload, a
  crash or a window closed with unsaved changes does not lose it. When
  the note opens again it offers *Restore* or *Discard*; a restored draft
  saves on the version it was written on, so a note changed meanwhile
  asks before it is overwritten. A draft goes when the note is saved,
  with *Load the saved version*, when the note is deleted, at sign-out,
  when another account signs in here, and after the longest session
  (`GOSIDIAN_LOGIN_SESSION_MAX_TTL`).
- **Navigate** focus between windows with `Alt-←` / `Alt-→`.
- **Close or minimize**: the focus goes back where the window was opened
  from, if that is still on the page, or else to the title of the window
  that takes over, and to the main once no window is left; a window
  restored from the footer takes it on its title, and so does the window
  `Alt-←` / `Alt-→` move to.
- **Tabs**: the plancia starts on the *Tabs* view, one window at a time,
  each with its tab. The tabs are one `Tab` stop, which a screen reader
  hears as tabs: the arrows go from one to the next, `Home` and `End` to
  the first and the last, `Delete` closes the tab's window. The bar's
  *Strip* button puts the windows side by side, and *Tabs* brings them
  back; the browser keeps the view you pick.
- **Full screen**: the button with four corners in a window's bar makes
  that window cover the page. The bar, the sidebar and the other windows
  go, and the browser goes full screen too. `Esc`, or the same button,
  ends it, and so does closing the window; a note opened from it takes
  its place. Where the browser does not go full screen (an iPhone), the
  window covers the page all the same, and the button ends it. `Esc` is
  the browser's: pressed in the page-layout editor, it ends the full
  screen too.
- **Narrow screens**: at 640 px and under (a phone, or a desktop zoomed
  to 200% and more) the page fits the width with no sideways scrolling.
  The sidebar is a drawer: the button at the left of the bar opens it,
  with the focus on its first control; its own button, `Esc` and a click
  beside it close it, with the focus back on the bar's button; and it
  closes when a window takes the focus, as a note opened from the tree
  does. One window at a time takes the width, as in the *Tabs* view;
  the bar has no *Strip* / *Tabs* choice, and the view you saved comes
  back on a wider screen. The bar keeps Search and the badges, and puts
  the account's name, its role and *Sign out* in its ⋯ menu.
- **Skip to the active window**: the first `Tab` from the top of the
  page shows this link, which takes the focus past the bar and the
  sidebar to the title of the focused window. A screen reader also jumps
  by landmark (the bar, the vault sidebar, the main with the windows) and
  by heading: each window opens on its title and goes down one level at a
  time, and a desk with no windows says so in its heading.
- **The focus stays where you work**: a note just made opens with the
  focus in its editor; a creation the server refuses puts it on the name,
  with the reason (a name already taken says which file exists, under
  the field); a field of the properties, once saved, and a card
  moved on a board, in its new column, keep it; the palette closed with
  `Esc` gives it back where it was opened. *Save* stays in the `Tab`
  order with nothing to save, and does nothing then. A note that reloads
  itself, as when a field is saved, stays on screen until its new version
  is there.
- **Screen readers** hear what changes under the keyboard, from one live
  region of the shell: a save, a card moved on a board, how many notes a
  search found (or that none did), how many entries an audit filter
  keeps, a change made elsewhere to the note being edited. The command
  palette says which entry the arrows are on
  ([editor → command palette](editor.md#command-palette)).
- **Every control has a name**: the editor is *Text of* the note; a
  select in a table's row says whose it is (*Role of claude*, *Level of
  a team on a project*); an editable cell of a view starts with the
  value it shows (*review (edit Status)*); a field whose placeholder is
  an example has a label over it in a form of several fields, and a name
  of its own where what it is for shows around it (the search, a new
  project).
- **Labels say what they do**: a project's grants read *Members: 1 ·
  teams: 0*; its flags are words (*not in git*, *short read*), each code
  first in the tooltip and next to its word in *What the flags do*; the
  audit says what each entry did (*signed out of the web UI*), the code
  in the tooltip, and its Action filter is a menu of the actions by
  group. A note's toolbar, from about 800 px (880 while editing), says a
  word next to each icon, the whole name in the tooltip; narrower, the
  icons alone, then the ⋯ menu.
- **Fields in the reader's language**: the properties panel, the tables,
  the boards and the forms of a database name its fields and a select's
  values as its schema says, or as the web UI's catalogue does for the
  fields of gosidian's own databases, the codes in the tooltips; dates
  read as the reader writes them
  ([databases → names in the web UI](../vault/databases.md#names-in-the-web-ui)).
- **Tab title**: the browser's tab says the focused window and its
  project, `nota-alfa · agile-lab · gosidian`, so two tabs of gosidian
  are told apart; the login page says `Sign in · gosidian` (`Sign up`
  for an invite), the consent page the client it authorizes.
- **Confirm**: a delete, a revoke or a rename asks in gosidian's own
  dialog, in the language of the UI, and `Esc` cancels. A delete or a
  revoke opens on *Cancel*, so an `Enter` pressed out of habit loses
  nothing: `Tab` reaches the red button. A rename opens on its field,
  where `Enter` confirms. Closing a window with unsaved changes still
  asks with the browser's dialog.

The open windows + focus are encoded in the URL (`?w=…&f=…`), so a
workspace is shareable and survives reload; when the URL is empty the
last workspace is restored from `localStorage`.

Each window you open is a step of the browser's history (IMP-224,
F-29): **Back** closes the last window opened, and **Forward** opens it
again; the other windows stay as they are. Closing that window with its
× goes back the same step. Any other change rewrites the current step:
the focus, a filter of the window, closing another window. Back on a
window with unsaved changes asks first, as the × does; on Cancel the
window stays. The windows a reload or a link opens add no step.

## Deep-link routes

These canonical routes still work as entry points — visiting one opens
the matching window in the plancia (so existing links and bookmarks keep
working):

| Route | Opens |
|---|---|
| `/notes/<path>` | the note (view; `…/edit` opens in edit mode) |
| `/notes/<path>/history` | the note's git history |
| `/projects` | project listing & admin; a rename says how many notes that named the project's files it rewrote, and how many, in projects you may not write, keep the old name ([Multi-project](../vault/multi-project.md#project-discovery--creation)) |
| `/tags` / `/tags/<tag>` | tag explorer |
| `/graph` | graph of wiki-link relations |
| `/search` | full-text search |
| `/query` | notes by their frontmatter (see below) |
| `/settings` | theme preset, language, git sync |
| `/admin/*` | users, tokens, invites, audit (owner only) |
| `/trash` | soft-deleted notes, folders and projects (if enabled); a note or a folder comes back to its project, for who can write there; a project comes back with the access it had: who could read it sees it, who administered it (or the owner) restores or purges it |

## Search window

**Search** (the top bar's button, or **Menu → Search**) looks for words
in the notes' text, in every project the account reads or in the one
named in its second field. Above the hits it says how many it found.
Each hit shows its title with its project beside, so two notes titled
alike are told apart, then its path and a few lines of its text; a hit
that lacks some words of the query says which. When nothing is found,
it suggests checking the spelling, has a button that removes the project
filter when there is one, and one that looks for the text in the names,
in the command palette (`Ctrl+K`, see
[editor → command palette](editor.md#command-palette)).

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

## Admin audit

**Menu → Admin → Audit** (owner only) lists what the accounts and the
agents did, the newest first; *When* sorts from the oldest. A filter by
actor, action (a menu of the actions by group), source and path keeps
some entries, and the page says which it shows of how many match
(*1–100 of 1,234*), with *Previous page* and *Next page*; *How many
entries* sets the page's size. The section, and the audit's filter,
order and page, live in the window's address: a reload or a link shows
the same entries, and the filter stays while another section is open.
The backend is `GET /api/v1/admin/audit` (`limit`, `offset`, `order`,
and the filters `actor`, `user_id`, `action`, `source`, `path_prefix`,
`since`, `until`); its `total` counts the entries the filters keep.

## Sidebar tree

The sidebar lists the vault's projects, folders and notes. A folder's
row is a button that opens and closes it (`Enter` or `Space` from the
keyboard), and remembers it in this browser; a screen reader hears
whether it is open. A closed folder draws nothing inside it, so a large
vault costs the page only the folders open.

Where the account may write, a folder has a **+** beside its row that
opens the creation window for that folder. It shows with the pointer on
the row or the focus in it (`Tab` from the row reaches it), and always
on a touch screen.

A note's row shows the note's title, the `title` of its frontmatter,
and its file name on hover. A note without a title shows its file name,
cut in the middle where the row is too narrow, so that its last ten
letters (the words after a date) stay in sight; a screen reader reads it
whole. The rows stay in file-name order, so notes named by their date
stay in time order, and the window a row opens starts with the title.
**Recent**, under the tree, shows its notes the same way.

The tree follows the writes in the vault, of this page, of the agents and
of the files on disk, with no reload of the page. A write reloads the
tree of its project alone, asked with the ETag of the last answer: an
edit of a note's body, which leaves the tree as it was, costs a 304
without a body. A change to a whole project (made, renamed, deleted)
reloads the whole tree. Large answers come in gzip.

The sidebar's width changes by dragging its right edge, or from the
keyboard, as a window's: `Tab` reaches the edge, the arrows move it by
24 px (64 with `Shift`), and `Home`, like a double click, brings it back
to 260 px. It goes from 180 to 560 px, and this browser keeps it. On a
narrow screen the sidebar is a drawer at most 280 px wide, with no edge
to drag.

On a wide screen the button at the left of the bar closes the sidebar,
and opens it again; so does `Ctrl+\` (`Cmd+\` on a Mac). The browser
keeps the choice, and a narrow screen's drawer leaves it as it is. A key
typed in the page-layout editor stays in the editor's frame: there, the
bar's button does it.

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
- **Vite** build; output embedded under `internal/server/web/dist`,
  without source maps
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
