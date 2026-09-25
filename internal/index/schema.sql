CREATE TABLE IF NOT EXISTS notes (
    id         INTEGER PRIMARY KEY,
    path       TEXT NOT NULL UNIQUE,
    title      TEXT NOT NULL,
    mtime      INTEGER NOT NULL,
    size       INTEGER NOT NULL,
    importance INTEGER NOT NULL DEFAULT 3  -- frontmatter importance, 1..5 (v1)
);

CREATE TABLE IF NOT EXISTS links (
    src_id      INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    target      TEXT NOT NULL,           -- raw target text from [[...]]
    target_path TEXT,                    -- resolved vault-relative path (nullable)
    alias       TEXT
);
CREATE INDEX IF NOT EXISTS links_src ON links(src_id);
CREATE INDEX IF NOT EXISTS links_target ON links(target);
CREATE INDEX IF NOT EXISTS links_target_path ON links(target_path);

CREATE TABLE IF NOT EXISTS tags (
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tags_tag ON tags(tag);
CREATE INDEX IF NOT EXISTS tags_note ON tags(note_id);

-- Columns are weighted by search.go (bm25): title, meta = the raw
-- frontmatter (tags, description, aliases…), body = the text without it.
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    title,
    meta,
    body,
    tokenize='unicode61 remove_diacritics 2'
);
