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

Mode choice is persisted in local storage per browser.

## Live preview

The preview uses the same goldmark pipeline as the MCP rendering path.
Wiki-links `[[target]]` are resolved and linked; unresolved targets are
rendered with a `broken` style so authors notice typos immediately.

## Save / history

- **Save** writes the note atomically (temp file + rename) and updates
  the SQLite index synchronously.
- **History** (`/notes/<path>/history`) lists previous states captured
  by git sync when enabled — with a one-click `restore` action.

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

`Cmd+K` / `Ctrl+K` opens a fuzzy finder across notes, projects, tags,
and built-in actions (go to graph, create note, …). Keyboard-first:
arrow keys to select, `Enter` to run, `Esc` to close. Recent
selections are remembered for quick re-access. The list it shows comes
from the server at each opening; the one it has shows while the new one
arrives.

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
