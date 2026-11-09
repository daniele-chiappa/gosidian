# Page layouts

Any markdown note can have a **design**: pages, frames of text and
images, master pages and styles, for print and PDF. It is made in a
page-layout editor that a plugin offers as an app
([plugins](../plugins.md#apps)): the plugin `layout`, designcraft's
editor in a window of gosidian. The note stays a normal markdown, and the
design is a file beside it that the tree does not show (IMP-226).

```markdown
---
title: Rivista, numero 1
layout_file: rivista/attachments/51c0e2f9a8b7d6c4.json
layout_format: 4
layout_generator: DesignCraft 0.17.0
layout_preview: rivista/attachments/9b1f0c2a7e4d3b6a.png
---

## Il primo titolo

Il testo della nota, che il design non tocca.
```

- `layout_file` is the design: the editor's `document.json`, with the
  pages, the frames, the texts and the styles, kept as an attachment of
  the project. Another version is another file: the one before stays for
  the note's history, until `memory_gc_attachments` takes it.
- **The images** are attachments, one file for the same bytes
  (`<project>/attachments/<sha256:16>.<ext>`). Each asset links the one it
  shows, by its name beside the design's file, so renaming the project
  leaves the links right. Saving the layout again adds no file for an
  image already there. When the file of a link changes, the layout opens
  with the new one, as a linked image does.
- `layout_preview` is the first page, small, shown at the top of the note.
  `layout_format` and `layout_generator` are the editor's package format
  and version. `layout_pdfs` lists the PDFs kept beside the note, each
  `label: path`: one for each version, by its name (`V3: …`), and the
  draft's (`draft: …`). An older `layout_pdf` counts as the draft's.
- A note without `layout_file` has no design yet: the editor starts from
  an empty document, and the first save writes the file and the fields.
  The note keeps its type: `type: layout` only marks a note made as a
  page layout from the start.
- **What a new design starts from** (IMP-232), read only while the note has
  no design, and taken away by the first save:
  - `layout_page`: the page, by the editor's name for it (`A4`, `A5`,
    `Letter`…) or as a size in millimetres (`148x210mm`);
  - `layout_template`: another page layout note. The design starts with its
    styles, swatches, parent pages and page size, and one empty page: none
    of its frames or its text. A template the reader may not read, or
    without a design, leaves the window saying so, and the design starts on
    the page asked, or A4.
- **Downloading a note** gives a normal markdown: the `layout_*` fields
  and the design stay behind. The export of a whole vault or project
  copies the files as they are.

## The pieces of a note

The design places **pieces** of the note's text, and they are numbered in
the note itself, by a comment no reader sees:

```markdown
<!-- 1 -->
## Introduzione

<!-- 2 -->
Il primo paragrafo.

<!-- 3 -->
![Schema dell'impianto](attachments/ab12cd34ef56ab78.png)

<!-- 4 escluso -->
Un appunto che resta nella nota e non va nel design.
```

- A piece runs from its comment to the next one. **Lay out this note**
  numbers every block: a paragraph, a heading, an image, a list, a table, a
  block of code, a quote. Taking a comment away joins that block to the
  piece before, and `POST …/pieces/number` with `{"n": 3}` sets the blocks of
  piece 3 apart again. A block written later has no number of its own until
  then.
- **The number is the piece's identity**: a frame shows the piece of that
  number, so swapping 3 and 5 in the note swaps what the design shows, and
  the text of the note does not move. A new number comes after the highest
  in the note: one that left it may still be on a page.
- `escluso` keeps a piece out of the design. In a fence a comment is code.
- The comments are no words of the note: search, `note.sections` and a
  downloaded note leave them out.

## In the web UI

**New note** has the kind **Page layout** when a plugin's app opens that
type: it makes the note and opens the editor. Its form asks the **page
size** (A4, A5, A6, B5, Letter, Letter - Half, or a size of its own in
millimetres) or **a page layout to start from**, among those the reader
may read, and writes them as `layout_page` or `layout_template`. The
palette (Ctrl+K) offers **New page layout** in the folder of the note in
the focused window, the note or its design, else of the last note read
(IMP-232). Any other
markdown note has **Lay out this note** among its actions, for whoever may
write it.

A note with a design shows a card at its top, with the first page, and has
**Open in the editor**, there and among its actions, when the owner
accepted the manifest of the plugin's app. The editor opens in a window of
its own (`app:<path>` in the address), framed from the plugin's address:

- The editor has no menu bar there, and always starts on the same
  interface. The window's bar has **Save**, **Place image**, **Export** and
  the **project references**; right-click menus and Quick Apply (Ctrl+Enter)
  stay in the editor.
- The editor opens the note's design, or a new one for a note without a
  design yet, in the web UI's language when the editor has it. A new one
  takes the note's name and the page or template the note names, A4
  without one; File › Document Setup changes the page.
- **Save** in the editor (Ctrl+S) saves the note; the window's bar says
  when, and a save refused says why. The document counts as saved only
  when the server kept it: a save refused leaves it unsaved, and so does
  what changed during a save.
- Only the note's document goes to the note: another one dropped on the
  editor saves to the downloads, and the window warns of changes not saved
  in any of them.
- The note's text can change while the design is open: that is no
  conflict, and the panel hears the pieces again. A design saved elsewhere
  since the editor opened it is not overwritten: the window says so and
  offers **Reload**, which discards the editor's changes and opens the
  design as it is now, or **Save anyway**, which writes the document as it
  is by then over it.
- The **Pieces** panel, beside Layers, lists the pieces in the order of the
  note with what became of each: to place, placed, out of date, of another kind, **changed
  in the design**, **changed on both sides**. **Place** makes a frame on the
  page, **Update** brings its frames up to the text (**Update all**
  when there are more), **Edit** opens the piece over the design, **Number**
  gives each block of a joined piece its own number. An image's description
  has its own line, `3.1` for image 3, to place apart as a caption; what the
  image's own note says shows beside it, to copy, and does not go on the
  page. An image goes by its description, or by what its own note says of
  it, before its file's name.
- **A booklet of hundreds of pieces** (IMP-232): above the list, which
  scrolls on its own, stay the **search** (the words of the pieces, or a
  number) and the **filters** by status, each with how many it holds: all,
  to place, out of date, changed in the design, only in the design. A frame
  selected on the page marks its piece and scrolls the list to it, whatever
  the filter; a click on a piece's name selects its frames and brings their
  page into view at the same zoom.
- **Add text** and **Add image**, above the list, put a new piece in the
  note, after the piece of the frame selected or at its end, and on the
  page at once. A text has a kind: paragraph, heading 1 to 3, or list, which
  goes into the note as `#`, `##`, `###` or `- `, and onto the page in the
  document's style of that name: **Heading 1**–**3**, **List Bullet**,
  **List Number**, **[Basic Paragraph]**, which the booklet changes in its
  Paragraph Styles and **Update** keeps.
- **Where a piece goes**, by Place or once added: below the last frame of
  the nearest piece before it in the note that has one, in that frame's
  column and in the first stretch free for it, as wide as the column and as
  tall as its text; then the next column, the next page, or a page added
  after when neither has room. A piece longer than a page runs on in frames
  on pages added after it. An image goes the same way, at most as wide and
  as tall as its column. Without a piece before it on a page, a piece goes
  at the top of the page in view.
- **Words changed in a frame** (IMP-230): a frame remembers the words it
  showed when it took its piece, so a piece whose frame changed its words on
  the page is **changed in the design**. Only the words count: bold, italic,
  styles and sizes stay the design's. **Update**, **Update all** and an
  **Edit** of the piece leave such a frame alone; **Put back as in the note**
  gives it the note's text again, after a second click.
  - **Bring to the note** writes the frame's words in the piece's text,
    where the note has those words: headings, links, tables and marks stay,
    and a mark typed in the frame stays a word (`\*`). When the words read
    there exactly as in the frame, they go at once; when they don't (a new
    paragraph in a table cell, say), the piece opens over the design with
    them, to look over and save. **Bring all to the note** does it for every
    piece changed in the design only.
  - A piece changed in the note too is **changed on both sides**: **Choose**
    opens it with the frame's words beside, to copy or to take with **Use
    these words**; saving writes the note and the frames take that text,
    **Keep the note** gives the frames the note's text.
  - Once the design is saved, the window lists the pieces changed in the
    design that the note lacks, with **Bring all to the note**.
- **The order of the pieces** (IMP-231) is the note's, as the order of the
  layers is the document's. A piece moves with the handle `⠿` at the start
  of its line, dragged onto another piece (before it on its upper half,
  after it on the lower one), or with `Alt+↑` / `Alt+↓` while the handle
  has the focus. It moves in the note with its comment and its blocks, and
  keeps its number, so the frames that show it still do: after a move the
  numbers are no longer in a row. What stands before the first comment
  stays first.
  - When the pages read the pieces in another order, the panel says so,
    with **Sort the note as the pages**. It lists the fewest moves (*7 goes
    after 3*, *5 goes first*), and **Apply** writes them. A piece on a page
    takes along the pieces that follow it in the note and are on none, and
    the pieces before the first one on a page stay first; a piece on more
    frames goes by the first one read (page, then top, then left).
  - The note never changes by itself. Each piece goes with the hash it was
    read with: a note changed meanwhile writes nothing, and the panel gets
    the pieces as they are now, to move them again.
- A frame made on the page lives **only in the design**, listed apart, each
  with **Bring to the note** (**Bring all to the note** when there are
  more): it enters the note with a number, as Markdown (its paragraphs, and
  the headings, quotes, code, lists and tables its styles say), after the
  piece whose frame comes before it as the pages are read, before them all
  when it comes first.
- **Edit** changes the piece where it stands: the text in the note's editor,
  with the piece as it reads beside it, or an image with its description and
  **Replace image**. **Save** writes that stretch of the note, a change
  elsewhere does not stop it, and the frames that show the piece catch up,
  leaving the design to save. An image inserted in a text, or pasted among
  its blocks, needs a frame of its own: it becomes a new piece, right after,
  to place. **Open in the note** is there for the rest of the text.
- **Place** shows the images among the project's attachments: the one
  chosen goes into the page, and the layout links it.
- **Export** is a direct download: PDF, PNG, IDML, EPUB, HTML or
  PowerPoint go to the browser's downloads and the note stays as it is.
  In the PDF, **Convert text to outlines** (under Advanced) draws the text
  as paths: the file carries no font and prints the same everywhere, but
  its text can't be selected, searched or read aloud.
- **PDF in the vault**, for who can write the note, keeps the PDF beside
  it instead (IMP-229). From the design as it is, it is the draft's, and
  takes the place of the draft's before. From a version opened to read,
  it is that version's, by its name, and stays when the next version
  comes. The note's card links the PDF of what readers see, the pinned
  version's or else the draft's, and downloads it as `<note>-V3.pdf`; who
  can edit sees the draft's too. The garbage collector of the attachments
  and a rename of the project know these files as the note's.
- Changes not saved are shown on the window, as in a note being edited.

The window speaks to the editor with `postMessage` (the bridge
`layout/2`): it takes a message only from its own frame, at the app's
origin, and the editor holds no credentials of gosidian's.

## An agent's session

An agent composes a note and its design together, one block at a time,
with the [`layout_*` tools](../mcp/tools.md#page-layouts): the text goes
into the note as pieces, each frame stays linked to its piece, and the
design is the same the editor opens.

```text
layout_open {note}                      → {session, etag, new}
layout_add_piece {note, text}           → {added: {n, hash}}
layout_run {session, commands: [
  {command: "piece.placeText", params: {n, hash, text, spread, rect}}]}
layout_render {session, page}           → a PNG, to look at the page
layout_save {session}                   → the design, in the note
layout_close {session}
```

The engine runs in the plugin, one process a session, and reaches only the
session's folder: gosidian brings it the design and the fonts, and takes
the design back, with the token's rights, so the plugin keeps no files and
no credentials. In `piece.placeImage` and `file.place`, `path` is the vault
path of an attachment.

Nothing reaches the vault before `layout_save`, because each save leaves a
new document among the attachments: megabytes for a booklet. It fails if the
design was saved since the opening, and a session unused for a while ends,
with what it had not saved (`LAYOUT_ENGINE_IDLE`, 30 minutes by default). A
token that may only read can open a design and render it, not save it.

A note's [valid version](../web-ui/editor.md#the-valid-version) has its design:
`layout_open {note, version: "valid"}`, or a tag, opens it to read and render,
`GET /api/v1/layouts/{path}?version=` gives it to the window, and the files the
GC took come back from git. Such a session has no `layout_save`.

## Fonts

The owner keeps the installation's fonts in Admin → Fonts, or with `POST
/api/v1/admin/fonts`: `.ttf`, `.otf` or `.ttc`, up to `[web]
layout_max_font_bytes`. They live in the state folder, out of the vault:
no git sync and no export. The editor gets them before a document opens,
each from an address with its hash that a browser keeps for good, so a
new font asks no new build and takes effect from the next opening. An
agent's session gets them too.

A font's licence is for the owner to read: the page shows what the font
says of embedding (`fsType`) and warns of those only for preview and
print, and refuses none.

## The editor's package

The editor opens and saves a package (`.designcraft`, a zip of its
`document.json`, the preview and the placed files). gosidian takes it
apart into attachments the note names, and puts it back together:

- `GET /api/v1/layouts/{path}`: the package of the note. Each asset has
  the bytes of its link when the reader may read it, an attachment of a
  project it sees. The others open without them, named in the header
  `Gosidian-Layout-Missing` (their ids). `204` for a note without a design
  yet; `422` when `layout_file` does not read, so that the editor does not
  start anew and save over it.
- `PUT /api/v1/layouts/{path}` (`If-Match` the etag `GET` gave, the
  design's: the note's text may change meanwhile): the package saved. Every file is checked before anything is written, as an
  attachment is: its type among the attachments', at most 10 MiB
  (`[uploads] max_attachment_bytes`), bytes that are what its extension
  says. An entry the package should not have (a path out of it among
  them) refuses the whole save. The answer is the design's new etag, its
  `file` and the attachment of each asset.
- `GET /api/v1/layouts/{path}/pieces`: the pieces of the note, in the
  order of its text: `n`, `kind`, `text`, `hash`, `line`, `blocks`, and
  `excluded`, `duplicate` (the number of a piece before it) and `image`
  (`src`, `alt`, `alt_hash`, `path`, the attachment, and `described`, what
  its image note says) when they apply.
- `POST /api/v1/layouts/{path}/pieces` with `{"after": 3, "text": "…"}` or
  `{"after": 3, "image": "<attachment>", "alt": "…"}`: a new piece in the
  note, after that piece or at its end. The answer is the pieces, and the
  new one in `added`.
- `POST /api/v1/layouts/{path}/pieces/number` (`If-Match` the note's etag):
  a comment before the blocks without one, every one, or with `{"n": 3}`
  those of that piece. The answer is the pieces as they are now.
- `POST /api/v1/layouts/{path}/pieces/reorder` with `{"order": [{"n": 3,
  "hash": "…"}, …]}`: the numbered pieces in that order, each with the hash
  it was read with, which a save of the design meanwhile leaves valid. Each
  keeps its number. An order that leaves a piece out is a 400, a piece
  changed since a 409. The answer is the pieces as they are now.
- `POST /api/v1/layouts/{path}/export?name=<file>.pdf`: a PDF an app
  wants kept, as an attachment the note lists in `layout_pdfs`. With
  `&version=<tag>` it is that version's, by its label, beside the others;
  without, the draft's. The plugin `layout` sends one for **PDF in the
  vault**: the window asks for it (`run` with `to_host`), and every other
  export is a download.

A package is at most 256 MiB unpacked, its `document.json` 16 MiB, its
entries 10,000: `[web] layout_max_bytes`, `layout_max_document_bytes` and
`layout_max_entries` in [the configuration](../configuration.md).
