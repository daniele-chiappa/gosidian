package index

import (
	"database/sql"
	"strings"
)

// SearchHit is one ranked search result.
type SearchHit struct {
	Path    string
	Title   string
	Snippet string // contains <mark>…</mark> tags; treat as trusted HTML in templates
	// Score is relative to the best hit of the same response (1 = best);
	// it is not comparable across searches.
	Score float64
	// Why names the signals behind Score: title match, backlinks,
	// importance, recency, pinned, archived, fused phrasings.
	Why []string
}

// Search runs a ranked FTS query over the whole vault. Plain user input is
// sanitized (every word quoted, prefix-matched, ANDed). Callers that filter
// by project or access use SearchWith, which applies the filter before the
// limit.
func (i *Index) Search(q string, limit int) ([]SearchHit, error) {
	return i.SearchWith(q, SearchOptions{Limit: limit})
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

// closedNoteExpr is the single SQL definition of a "closed" note — one tagged
// status:done or status:archived — shared by StaleNotes and MaintenanceCounts,
// so the memory_stale tool and the bootstrap maintenance digest cannot drift
// on what they exclude. It expects the notes table to be aliased as n.
const closedNoteExpr = `EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id AND t.tag IN ('status:done', 'status:archived'))`

// StaleNote is a RecentNote annotated with whether the note is closed (see
// closedNoteExpr): closed notes age by design, and the flag lets callers tell
// archive candidates from plans that are simply finished.
type StaleNote struct {
	RecentNote
	Closed bool
}

// StaleNotes is the inverse of RecentNotes: it returns notes whose mtime is
// strictly less than `before`, in ascending mtime order (oldest first),
// optionally scoped to a project prefix. Used by the memory_stale MCP tool to
// surface archive candidates. With excludeClosed the result is exactly the set
// MaintenanceCounts counts as stale; without it every old note is returned,
// flagged Closed where applicable.
func (i *Index) StaleNotes(project string, before int64, limit int, excludeClosed bool) ([]StaleNote, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT n.path, n.title, n.mtime, ` + closedNoteExpr + ` FROM notes n WHERE n.mtime < ?`
	args := []any{before}
	if project != "" {
		like := strings.ReplaceAll(project, "%", `\%`)
		like = strings.ReplaceAll(like, "_", `\_`) + "/%"
		q += ` AND n.path LIKE ? ESCAPE '\'`
		args = append(args, like)
	}
	if excludeClosed {
		q += ` AND NOT ` + closedNoteExpr
	}
	q += ` ORDER BY n.mtime ASC LIMIT ?`
	args = append(args, limit)

	rows, err := i.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StaleNote
	for rows.Next() {
		var n StaleNote
		if err := rows.Scan(&n.Path, &n.Title, &n.Mtime, &n.Closed); err != nil {
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

// ImportanceRow is a note with its frontmatter importance (1..5, 3 when
// absent or unparseable).
type ImportanceRow struct {
	Path       string
	Title      string
	Importance int
}

// NotesByImportance returns a project's notes whose importance is at least
// minLevel, highest first, then by path.
func (i *Index) NotesByImportance(project string, minLevel int) ([]ImportanceRow, error) {
	rows, err := i.db.Query(`SELECT path, title, importance FROM notes
        WHERE path LIKE ? ESCAPE '\' AND importance >= ?
        ORDER BY importance DESC, path`, likeUnder(project), minLevel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportanceRow
	for rows.Next() {
		var r ImportanceRow
		if err := rows.Scan(&r.Path, &r.Title, &r.Importance); err != nil {
			return nil, err
		}
		out = append(out, r)
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

// MaintenanceCounts returns the cheap per-project grooming signals served by
// the bootstrap maintenance digest (ADR-019): the number of unresolved
// wikilinks leaving the project's notes, and the number of notes older than
// staleBefore that are not closed (closedNoteExpr: tagged status:done or
// status:archived — closed plans and archived notes age by design and would
// drown the signal; memory_stale with exclude_closed lists exactly them).
// Both are single indexed queries — the digest's cost budget forbids content
// scans.
//
// excludeSuffixes drops targets by extension: the index resolves note targets
// only, so attachment embeds (![[<hash>.webp]]) are always "unresolved" here
// even when the file exists and renders — counting them inflated a real
// vault's digest by ~138 phantom broken links. Genuinely missing embeds are
// the (fs-aware, on-demand) broken-wikilink lint rule's job.
func (i *Index) MaintenanceCounts(project string, staleBefore int64, excludeSuffixes []string) (brokenLinks, staleCount int, err error) {
	like := strings.ReplaceAll(project, "%", `\%`)
	like = strings.ReplaceAll(like, "_", `\_`) + "/%"

	brokenQ := `SELECT COUNT(*) FROM links JOIN notes ON links.src_id = notes.id
		 WHERE links.target_path IS NULL AND notes.path LIKE ? ESCAPE '\'`
	args := []any{like}
	for _, suf := range excludeSuffixes {
		brokenQ += ` AND lower(links.target) NOT LIKE ?`
		args = append(args, "%"+strings.ToLower(suf))
	}
	if err = i.db.QueryRow(brokenQ, args...).Scan(&brokenLinks); err != nil {
		return 0, 0, err
	}

	if err = i.db.QueryRow(
		`SELECT COUNT(*) FROM notes n
		 WHERE n.path LIKE ? ESCAPE '\' AND n.mtime < ? AND NOT `+closedNoteExpr,
		like, staleBefore,
	).Scan(&staleCount); err != nil {
		return 0, 0, err
	}
	return brokenLinks, staleCount, nil
}
