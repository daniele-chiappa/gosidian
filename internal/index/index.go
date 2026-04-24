package index

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/gosidian/gosidian/internal/parser"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type Index struct {
	mu sync.Mutex
	db *sql.DB
}

type NoteDoc struct {
	Path    string
	Title   string
	Body    string
	ModTime int64
	Size    int64
}

func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Index{db: db}, nil
}

func (i *Index) Close() error { return i.db.Close() }

// Upsert stores the note, extracts links/tags from the body, and refreshes
// notes_fts. Existing rows for the same path are replaced.
func (i *Index) Upsert(n NoteDoc) error {
	id, err := i.upsertLocked(n)
	if err != nil {
		return err
	}
	if err := i.ResolveLinksFor(id); err != nil {
		return err
	}
	// Resolve any previously-unresolved link that now matches this note.
	return i.resolveInbound(id, n.Path, n.Title)
}

// resolveInbound updates previously-unresolved links whose raw target matches
// the given note's path/title/basename to point at noteID's path.
func (i *Index) resolveInbound(noteID int64, notePath, title string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	base := notePath
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.TrimSuffix(base, ".md")

	// Candidate target strings that should resolve to this note.
	candidates := []string{notePath, strings.TrimSuffix(notePath, ".md"), title, base}
	seen := map[string]struct{}{}
	var dedup []string
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(c)]; ok {
			continue
		}
		seen[strings.ToLower(c)] = struct{}{}
		dedup = append(dedup, c)
	}
	for _, c := range dedup {
		if _, err := i.db.Exec(
			`UPDATE links SET target_path = ? WHERE (target_path IS NULL OR target_path = '') AND lower(target) = lower(?)`,
			notePath, c,
		); err != nil {
			return err
		}
	}
	_ = noteID
	return nil
}

func (i *Index) upsertLocked(n NoteDoc) (int64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	tx, err := i.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	title := n.Title
	links, tags, frontTitle := parser.Extract([]byte(n.Body))
	if frontTitle != "" {
		title = frontTitle
	}

	var oldID sql.NullInt64
	_ = tx.QueryRow(`SELECT id FROM notes WHERE path = ?`, n.Path).Scan(&oldID)
	if oldID.Valid {
		if _, err := tx.Exec(`DELETE FROM notes_fts WHERE rowid = ?`, oldID.Int64); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(`
        INSERT INTO notes(path, title, mtime, size) VALUES(?, ?, ?, ?)
        ON CONFLICT(path) DO UPDATE SET title=excluded.title, mtime=excluded.mtime, size=excluded.size
    `, n.Path, title, n.ModTime, n.Size); err != nil {
		return 0, err
	}

	var id int64
	if err := tx.QueryRow(`SELECT id FROM notes WHERE path = ?`, n.Path).Scan(&id); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`DELETE FROM links WHERE src_id = ?`, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE note_id = ?`, id); err != nil {
		return 0, err
	}

	for _, l := range links {
		if _, err := tx.Exec(`INSERT INTO links(src_id, target, target_path, alias) VALUES(?,?,?,?)`,
			id, l.Target, nil, l.Alias); err != nil {
			return 0, err
		}
	}
	for _, t := range tags {
		if _, err := tx.Exec(`INSERT INTO tags(note_id, tag) VALUES(?,?)`, id, t); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO notes_fts(rowid, title, body) VALUES(?,?,?)`,
		id, title, n.Body,
	); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (i *Index) Delete(path string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var id sql.NullInt64
	if err := tx.QueryRow(`SELECT id FROM notes WHERE path = ?`, path).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if id.Valid {
		if _, err := tx.Exec(`DELETE FROM notes_fts WHERE rowid = ?`, id.Int64); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM notes WHERE path = ?`, path); err != nil {
		return err
	}
	// clear target_id references pointing at this path
	if _, err := tx.Exec(`UPDATE links SET target_path = NULL WHERE target_path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveLinksFor re-resolves outgoing links from a single note.
func (i *Index) ResolveLinksFor(noteID int64) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	rows, err := i.db.Query(`SELECT rowid, target FROM links WHERE src_id = ?`, noteID)
	if err != nil {
		return err
	}
	var pending []struct {
		rowid  int64
		target string
	}
	for rows.Next() {
		var rid int64
		var tgt string
		if err := rows.Scan(&rid, &tgt); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, struct {
			rowid  int64
			target string
		}{rid, tgt})
	}
	rows.Close()

	for _, p := range pending {
		resolved := i.resolveTargetLocked(p.target)
		if _, err := i.db.Exec(`UPDATE links SET target_path = ? WHERE rowid = ?`, nullable(resolved), p.rowid); err != nil {
			return err
		}
	}
	return nil
}

// resolveTargetLocked maps a [[wiki-link]] target to a note path.
// Matches by exact path, title (case-insensitive), or basename (case-insensitive).
func (i *Index) resolveTargetLocked(target string) string {
	t := strings.TrimSpace(target)
	if t == "" {
		return ""
	}
	// 1. exact path match (with or without .md)
	tryPaths := []string{t, t + ".md"}
	for _, p := range tryPaths {
		var got string
		if err := i.db.QueryRow(`SELECT path FROM notes WHERE path = ?`, p).Scan(&got); err == nil {
			return got
		}
	}
	// 2. title match
	var got string
	if err := i.db.QueryRow(`SELECT path FROM notes WHERE lower(title) = lower(?) LIMIT 1`, t).Scan(&got); err == nil {
		return got
	}
	// 3. basename match
	if err := i.db.QueryRow(
		`SELECT path FROM notes WHERE lower(path) LIKE ? OR lower(path) = ? LIMIT 1`,
		"%/"+strings.ToLower(t)+".md", strings.ToLower(t)+".md",
	).Scan(&got); err == nil {
		return got
	}
	return ""
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ResolveAll re-resolves every link in the index. Useful after bulk scan.
func (i *Index) ResolveAll() error {
	i.mu.Lock()
	rows, err := i.db.Query(`SELECT DISTINCT src_id FROM links`)
	i.mu.Unlock()
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := i.ResolveLinksFor(id); err != nil {
			return err
		}
	}
	return nil
}
