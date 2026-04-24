package index

import (
	"database/sql"
	"strings"
)

type SearchHit struct {
	Path    string
	Title   string
	Snippet string // contains <mark>…</mark> tags; treat as trusted HTML in templates
}

// Search runs an FTS5 query. Plain user input is sanitized; callers can pass
// raw FTS syntax too.
func (i *Index) Search(q string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 50
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	// escape double quotes, wrap each token in quotes + prefix search
	terms := strings.Fields(q)
	for i, t := range terms {
		t = strings.ReplaceAll(t, `"`, ``)
		terms[i] = `"` + t + `"*`
	}
	ftsQuery := strings.Join(terms, " ")

	rows, err := i.db.Query(`
        SELECT n.path, n.title, snippet(notes_fts, 1, '<mark>', '</mark>', '…', 12)
        FROM notes_fts
        JOIN notes n ON n.id = notes_fts.rowid
        WHERE notes_fts MATCH ?
        ORDER BY rank
        LIMIT ?
    `, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Path, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

type NoteRow struct {
	ID    int64
	Path  string
	Title string
}

func (i *Index) Note(path string) (*NoteRow, error) {
	var n NoteRow
	err := i.db.QueryRow(`SELECT id, path, title FROM notes WHERE path = ?`, path).
		Scan(&n.ID, &n.Path, &n.Title)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// NotesByPrefix returns notes whose vault-relative path starts with prefix
// followed by "/". An empty prefix returns all notes.
func (i *Index) NotesByPrefix(prefix string) ([]NoteRow, error) {
	if prefix == "" {
		return i.AllNotes()
	}
	like := strings.ReplaceAll(prefix, "%", `\%`)
	like = strings.ReplaceAll(like, "_", `\_`) + "/%"
	rows, err := i.db.Query(
		`SELECT id, path, title FROM notes WHERE path LIKE ? ESCAPE '\' ORDER BY path`,
		like,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteRow
	for rows.Next() {
		var n NoteRow
		if err := rows.Scan(&n.ID, &n.Path, &n.Title); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (i *Index) AllNotes() ([]NoteRow, error) {
	rows, err := i.db.Query(`SELECT id, path, title FROM notes ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteRow
	for rows.Next() {
		var n NoteRow
		if err := rows.Scan(&n.ID, &n.Path, &n.Title); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type TagCount struct {
	Tag   string
	Count int
}

func (i *Index) Tags() ([]TagCount, error) {
	rows, err := i.db.Query(`SELECT tag, COUNT(*) c FROM tags GROUP BY tag ORDER BY c DESC, tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.Tag, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TagsByProject returns tag counts limited to notes whose path starts with
// `<project>/`. Equivalent to Tags() filtered server-side; used by
// memory_bootstrap to compute per-project top tags without loading every note.
func (i *Index) TagsByProject(project string) ([]TagCount, error) {
	if project == "" {
		return i.Tags()
	}
	like := strings.ReplaceAll(project, "%", `\%`)
	like = strings.ReplaceAll(like, "_", `\_`) + "/%"
	rows, err := i.db.Query(`
        SELECT t.tag, COUNT(*) c
        FROM tags t JOIN notes n ON n.id = t.note_id
        WHERE n.path LIKE ? ESCAPE '\'
        GROUP BY t.tag
        ORDER BY c DESC, t.tag
    `, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.Tag, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecentNote carries the mtime so agents can tell "what changed since X".
type RecentNote struct {
	Path  string
	Title string
	Mtime int64
}

// RecentNotes returns notes ordered by descending mtime, optionally scoped to
// a project (top-level folder) and to mtime >= since. An empty project
// matches all notes; a since of 0 means "no lower bound".
func (i *Index) RecentNotes(project string, since int64, limit int) ([]RecentNote, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if project == "" {
		rows, err = i.db.Query(
			`SELECT path, title, mtime FROM notes WHERE mtime >= ? ORDER BY mtime DESC LIMIT ?`,
			since, limit,
		)
	} else {
		like := strings.ReplaceAll(project, "%", `\%`)
		like = strings.ReplaceAll(like, "_", `\_`) + "/%"
		rows, err = i.db.Query(
			`SELECT path, title, mtime FROM notes WHERE path LIKE ? ESCAPE '\' AND mtime >= ? ORDER BY mtime DESC LIMIT ?`,
			like, since, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecentNote
	for rows.Next() {
		var n RecentNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Mtime); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// StaleNotes is the inverse of RecentNotes: it returns notes whose mtime is
// strictly less than `before`, in ascending mtime order (oldest first),
// optionally scoped to a project prefix. Used by the memory_stale MCP tool to
// surface archive candidates.
func (i *Index) StaleNotes(project string, before int64, limit int) ([]RecentNote, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var (
		rows *sql.Rows
		err  error
	)
	if project == "" {
		rows, err = i.db.Query(
			`SELECT path, title, mtime FROM notes WHERE mtime < ? ORDER BY mtime ASC LIMIT ?`,
			before, limit,
		)
	} else {
		like := strings.ReplaceAll(project, "%", `\%`)
		like = strings.ReplaceAll(like, "_", `\_`) + "/%"
		rows, err = i.db.Query(
			`SELECT path, title, mtime FROM notes WHERE path LIKE ? ESCAPE '\' AND mtime < ? ORDER BY mtime ASC LIMIT ?`,
			like, before, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecentNote
	for rows.Next() {
		var n RecentNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Mtime); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (i *Index) NotesByTag(tag string) ([]NoteRow, error) {
	rows, err := i.db.Query(`
        SELECT n.id, n.path, n.title
        FROM notes n JOIN tags t ON t.note_id = n.id
        WHERE t.tag = ?
        ORDER BY n.path
    `, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteRow
	for rows.Next() {
		var n NoteRow
		if err := rows.Scan(&n.ID, &n.Path, &n.Title); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NotesByTagInProject is NotesByTag constrained to notes under the given
// project prefix. Empty project falls back to NotesByTag.
func (i *Index) NotesByTagInProject(tag, project string) ([]NoteRow, error) {
	if project == "" {
		return i.NotesByTag(tag)
	}
	like := strings.ReplaceAll(project, "%", `\%`)
	like = strings.ReplaceAll(like, "_", `\_`) + "/%"
	rows, err := i.db.Query(`
        SELECT n.id, n.path, n.title
        FROM notes n JOIN tags t ON t.note_id = n.id
        WHERE t.tag = ? AND n.path LIKE ? ESCAPE '\'
        ORDER BY n.path
    `, tag, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteRow
	for rows.Next() {
		var n NoteRow
		if err := rows.Scan(&n.ID, &n.Path, &n.Title); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
