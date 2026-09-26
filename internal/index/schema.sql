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

-- The indexed words, one row per distinct term: query-side stemming picks
-- the inflections of a search word from here (stem.go).
CREATE VIRTUAL TABLE IF NOT EXISTS notes_vocab USING fts5vocab(notes_fts, 'row');

-- Frontmatter as queryable fields (v2, memory_query): one row per scalar,
-- one per list element, plus the namespaced tags (status:done → status) of
-- a note that has no field of that name. value is the text as written; num
-- and date are filled when the text reads as a number or an ISO date.
CREATE TABLE IF NOT EXISTS note_fields (
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    value   TEXT NOT NULL,
    num     REAL,
    date    TEXT,
    source  TEXT NOT NULL  -- field | list | tag
);
CREATE INDEX IF NOT EXISTS note_fields_key ON note_fields(key, value);
CREATE INDEX IF NOT EXISTS note_fields_note ON note_fields(note_id);
