# Editor

The note editor at `/notes/<path>` is a plain markdown textarea with
live preview. No rich-text, no WYSIWYG gymnastics — the editor is
intentionally thin because the vault is the source of truth and many
users edit notes from Obsidian, a terminal editor, or another markdown
tool on the filesystem.

## Modes

A toolbar above the editor toggles four layout modes:

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

## Focus mode

The floating `maximize` button (or the `F` key) toggles **focus mode**:
hides topbar + sidebar + attachment pane so only the editor + preview
stay on screen. Re-toggle to restore. The setting is persisted in
local storage.

## Command palette

`Cmd+K` / `Ctrl+K` opens a fuzzy finder across notes, projects, tags,
and built-in actions (go to graph, create note, toggle focus mode,
…). Keyboard-first: arrow keys to select, `Enter` to run, `Esc` to
close. Recent selections are remembered for quick re-access.

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
