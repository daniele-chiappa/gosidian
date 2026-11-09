# Editor

A note opens as a **plancia window** with a **View / Edit** toggle in
the window header. It defaults to *view* (rendered preview); flipping
to *edit* mounts the editor in the same window — no second window. The
editor itself is **CodeMirror 6**, loaded lazily so a window that's
only ever read never pulls the editor chunk. No rich-text, no WYSIWYG
gymnastics — the editor is intentionally thin because the vault is the
source of truth and many users edit notes from Obsidian, a terminal
editor, or another markdown tool on the filesystem.

## Modes

While in *edit*, a toolbar above the editor toggles four layout modes:

- **Editor** — full-width textarea, no preview
- **Split** — editor + preview side-by-side
- **Stacked** — editor on top, preview below (good on narrow displays)
- **Preview** — rendered note only; no editing affordance

The choice among the first three is persisted in local storage per browser.
Preview is for a look and is not remembered: Edit always shows the text.

## Live preview

The preview uses the same goldmark pipeline as the MCP rendering path.
Wiki-links `[[target]]` are resolved and linked; unresolved targets are
rendered with a `broken` style so authors notice typos immediately.

## Wikilink suggestions

Typing `[[` and a few letters suggests the notes whose **title or file
name** contains them, ignoring case: `[[nota-b` finds *Nota beta*
(`nota-beta.md`), and so does `[[nota b`. What a note says in its text
does not count. A title that starts with the letters comes first, then a
file name that does, then the others. Each suggestion shows the title
with the path beside it, and picking one writes the path:
`[[agile-lab/notes/nota-beta]]`. `Ctrl+Space` right after `[[` lists the
notes edited last. A `relation` field of the properties suggests notes
the same way.

## Tags

A markdown note shows its tags as chips above its text: among the fields
of the property panel for a row of a database, on a line of their own
for any other note. Who may write the note takes a tag away with its ×
and adds one in the field beside them: Enter, a comma or a space adds
what is typed (without the `#`), and a pasted list is split at its commas
and spaces. While you type, the field suggests the project's tags, the
most used first, then the project's `tag_vocabulary` when it has the
vocabulary flag; ↑ and ↓ walk them, Enter picks one, Esc closes them. A
tag that is new is written all the same. Each change is saved at once,
against the tags the note had: if someone changed them meanwhile, nothing
is overwritten and the field shows the tags of now. A reader who may not
write sees the chips alone, and nothing when there are none. The tags
written in the text (`#tag`) are not chips.

In the editor the frontmatter's tags are checked as you write: a line
above the editor says which are empty (`[a,, b]`) or have a space
(`topic:audit type:task`, one tag for the server), until they are right.
It does not stop the save.

## Save / history

- **Save** writes the note atomically (temp file + rename) and updates
  the SQLite index synchronously. The toolbar then says *saved at* and
  the time, and a screen reader hears it, at each save; in a narrow
  window the toolbar keeps the time with a check, the whole in its
  tooltip.
- **History** (`/notes/<path>/history`, or *History* in the note's
  menu) lists the commits of the note when git sync is on, newest first:
  the date, who wrote it, and what was done (`update notes/plan.md`,
  `rename a.md -> b.md`). See [History](#history) below.


## The valid version

A note that changes while the right text must stay available can name its
**valid version**, which the web UI calls the pinned one: a booklet under
revision, say. A reader sees that version, with a line that says a newer draft
exists and a button to it; who edits works on the note as it is, the draft, and
is told that readers keep seeing the other. A pinned note goes on changing: no
need to unpin it to work on it. Copying and downloading follow what the window
shows. The page layout opens the draft for who can edit it, and the version for
who can only read: there it reads and exports, and has no Save.

- The **version button**, in the bar of the note and of its design, says what
  readers see: *Latest*, or *Pinned V3* with a dot when a newer draft exists.
  Its panel is for the project's admin, and shows the rest of the readers what
  it says, with *Open* on each version:
  - a click on a version pins it, and one on *The latest change* goes back;
  - *Pin this version* gives the note as it is a name, such as `V3`, and pins
    it in one step, saving first what is not saved. *Tag only* names it
    without pinning it;
  - *Another commit…* does the same for an older commit;
  - *MCP callers see* chooses what agents read while the note changes: the
    latest change, the same as readers, or a named version
    ([what they get](../mcp/tools.md#what-a-note-gives-mcp-callers)).
- In the note's **History** the admin does the same from a version's entry. A
  name never moves to another version, and the named versions show on their
  entries for everyone.
- It needs git sync: the version is a tag on the commit, and the note names
  it in `valid_version` in its frontmatter. Any other write leaves that key as
  it was, and the properties panel refuses it. Renames, the trash and a
  restore keep it.
- A valid version says which text is right, it hides nothing: who reads the
  note can reach the draft and the history.
- `memory_gc_attachments` keeps what the valid version names, and a version
  chosen long after brings back from git the attachments it lost.
- **Search reads the valid version**: a word only in the draft is found by no
  search, and a word of that version finds the note. The title, the tags, the
  links and the frontmatter stay the note's as it is. A pipeline reads the
  draft unless it asks for
  [`at: valid`](../pipelines.md#the-valid-version-of-a-note), which is what a
  pipeline that cuts notes for a bot must set.
- If the tag can no longer be read, the window shows the note as it is, with a
  warning, and search reads the note as it is.

## History

Each entry of the **History** window is a button: a click opens that
version under it, and a second click closes it.

- **The diff** comes first: what changed from that version to the note
  as it is now. A line it had that is gone has a red background and a
  `−`, a line added since a green one and a `+`; a screen reader hears
  *removed* and *added*. The newest version is the note itself, and says
  so. A note renamed since says the name it had then.
- **Show the text of this version** shows it as it was, read-only.
- **Restore this version**, for who may write the note, asks first, in a
  dialog that opens on *Cancel*. The note goes back to that version as a
  save writes it: an editor open on it sees it changed elsewhere, and
  the history gets a new commit at its top, in your name, `restore
  <path> to <commit>`, which takes the focus. The note as it was before
  stays in the history, a version like the others. If the note changed
  since the version was opened, nothing is written and the window says
  so, with the diff read again.
- A version over 1 MiB, one that is not text, or the commit that
  deleted the note shows neither diff nor text, and says why.

The versions a note had in a project you cannot read are not listed.

## Notes over 1 MiB

The web UI sends a note's text to the server to preview it and to save
it, and the server takes at most 1 MiB in such a request. A larger note,
which only git, an editor on the disk or an import can write, opens
**read-only**, with a line above it that says why and **Edit** disabled:
a markdown note shows its text in the editor, without a preview, and an
HTML note keeps its frame. Edit it where it was written. A draft that grows past
1 MiB is refused when you save, with the same reason, and stays in the
editor. A preview that fails on a note of any size leaves the note open,
with the reason where the preview would be.

## Print / Save as PDF

In **view mode**, a **Print** button appears in the note's header — but
**only for Markdown notes** (`.html` notes are not printable; the
sandboxed iframe the browser clips to a single page is out of reach —
see IMP-053). Clicking it opens the browser's native print dialog, from
which you typically pick *Save as PDF*.

The print stylesheet (`@media print`) shows **only this note's rendered
article** and hides the rest of the plancia (topbar, sidebar, other
windows) plus the browser's own header/footer chrome, so a single,
clean note reaches the page. The button calls `printNote()`, which
tags the article with the `gosidian-print-target` class, fires
`window.print()`, and removes the class again on `afterprint`.

## Download

A **Download** button in the note header saves the note's **original
source file** as-is (the raw `.md` or `.html`, with its filename). It is
fully client-side: the saved content is already loaded, so `downloadOriginal()`
wraps it in a `Blob` and triggers an `<a download>` — no server round-trip.

## Maximize / window controls

There is no dedicated "focus mode" anymore — to give a note the full
screen, use the plancia window's **maximize** control. See
[Overview → window controls](overview.md) for the full set of window
affordances (maximize, minimize, close, drag, resize).

## Command palette

`Cmd+K` / `Ctrl+K` opens a finder for a note, a project or a tag by its
name: a note by its title or its path. Keyboard-first: arrow keys to
select, `Enter` to open, `Esc` to close. A project opens Projects with
the focus on its name, in view, whether Projects was open already or
not. With the field empty, the notes
viewed recently come first. The list it shows comes from the server at
each opening; the one it has shows while the new one arrives.

The palette matches names, not what the notes say. Once there is a
text, its last entry, **Search the text for "…"**, looks for it in the
notes' text: it opens Search with the text, or hands it to the Search
already open, which searches again even when its field holds that text. The **Search** button in the top bar opens Search, and
shows no shortcut: `Ctrl+K` opens the palette.

To a screen reader the palette is a dialog named *Jump to a note, a
project or a tag*, and its field a combobox: it keeps the focus, `Tab`
included, and says which entry the arrows are on, with its kind when the
list mixes kinds. `Home` and `End` go to the first and the last entry
once the arrows are in the list, and move the caret in the text before;
the entry chosen stays in view as the list scrolls.

## Not supported in-editor

Deliberately excluded from the web editor because they belong
elsewhere:

- **Rich text formatting toolbar** — the markdown source is the
  source of truth; a toolbar would hide it
- **Collaborative editing** — out of scope; use git sync for
  multi-writer workflows
- **Drag-drop reordering in outline panel** — the outline is a read-
  only view of the heading structure

For heavy-duty editing (long-form writing, multi-cursor operations,
vim bindings) open the vault in Obsidian, VS Code, or your editor of
choice and edit on disk. The watcher picks up external changes and
reindexes in real time — see
[Vault format → Source of truth](../vault/format.md#source-of-truth).
