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
	mu    sync.Mutex
	db    *sql.DB
	stems *stemmer // query-side Porter stems, see stem.go
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	stems, err := newStemmer()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("stemmer: %w", err)
	}
	return &Index{db: db, stems: stems}, nil
}

// schemaVersion is stored in PRAGMA user_version. v1 (IMP-095) splits the
// frontmatter out of the FTS body into its own weighted column and adds
// notes.importance; v2 (IMP-099) adds note_fields, which the boot scan fills
// like every other table.
const schemaVersion = 2

// migrate brings an index file to schemaVersion. The index is a cache of the
// vault — the boot scan re-upserts every note — so a shape change drops and
// recreates a table instead of converting its rows.
func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v < 1 {
		if _, err := db.Exec(`DROP TABLE IF EXISTS notes_fts`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	// CREATE TABLE IF NOT EXISTS leaves a pre-v1 notes table as it was.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notes') WHERE name = 'importance'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(`ALTER TABLE notes ADD COLUMN importance INTEGER NOT NULL DEFAULT 3`); err != nil {
			return err
		}
	}
	_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
	return err
}

func (i *Index) Close() error {
	i.stems.close()
	return i.db.Close()
}

// noteExts mirrors vault.noteExtensions. Duplicated rather than imported
// because internal/vault imports internal/index — the reverse would cycle.
var noteExts = []string{".md", ".html"}

// stripNoteExt drops a trailing note extension (.md/.html) from p, leaving any
// other suffix untouched.
func stripNoteExt(p string) string {
	low := strings.ToLower(p)
	for _, e := range noteExts {
		if strings.HasSuffix(low, e) {
			return p[:len(p)-len(e)]
		}
	}
	return p
}

// extractForPath dispatches link/tag/title/fts-body extraction by note kind.
// HTML notes use parser.ExtractHTML and index its plain-text projection (not the
// raw markup) for FTS; markdown notes index the body after the frontmatter,
// which goes to its own FTS column (see upsertLocked).
func extractForPath(path, body string) (links []parser.WikiLinkRef, tags []string, title, ftsBody string) {
	if strings.HasSuffix(strings.ToLower(path), ".html") {
		return parser.ExtractHTML([]byte(body))
	}
	links, tags, title = parser.Extract([]byte(body))
	return links, tags, title, parser.BodyAfterFrontmatter([]byte(body))
}

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
	base = stripNoteExt(base)

	// Candidate target strings that should resolve to this note.
	candidates := []string{notePath, stripNoteExt(notePath), title, base}
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
	links, tags, frontTitle, ftsBody := extractForPath(n.Path, n.Body)
	if frontTitle != "" {
		title = frontTitle
	}
	meta := parser.FrontmatterRawForPath(n.Path, []byte(n.Body))

	var oldID sql.NullInt64
	_ = tx.QueryRow(`SELECT id FROM notes WHERE path = ?`, n.Path).Scan(&oldID)
	if oldID.Valid {
		if _, err := tx.Exec(`DELETE FROM notes_fts WHERE rowid = ?`, oldID.Int64); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(`
        INSERT INTO notes(path, title, mtime, size, importance) VALUES(?, ?, ?, ?, ?)
        ON CONFLICT(path) DO UPDATE SET title=excluded.title, mtime=excluded.mtime,
            size=excluded.size, importance=excluded.importance
    `, n.Path, title, n.ModTime, n.Size, parser.Importance(meta)); err != nil {
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
	if _, err := tx.Exec(`DELETE FROM note_fields WHERE note_id = ?`, id); err != nil {
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

	for _, f := range extractFields(meta, tags) {
		if _, err := tx.Exec(`INSERT INTO note_fields(note_id, key, value, num, date, source) VALUES(?,?,?,?,?,?)`,
			id, f.key, f.value, f.num, f.date, f.source); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO notes_fts(rowid, title, meta, body) VALUES(?,?,?,?)`,
		id, title, meta, ftsBody,
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
	// [[note#heading]] links to a heading INSIDE the note (Obsidian
	// semantics): resolve the path part, the fragment is presentation-level.
	// Without this strip every fragment link stayed unresolved — invisible to
	// backlinks/graph and flagged broken by lint (BUG-025). The markdown
	// renderer already strips it independently (parser/markdown.go).
	if idx := strings.IndexByte(t, '#'); idx >= 0 {
		t = strings.TrimSpace(t[:idx])
	}
	if t == "" {
		// pure [[#heading]] self-link: no cross-note edge to record.
		return ""
	}
	// 1. exact path match (verbatim, then with each note extension; markdown
	// wins ties because .md precedes .html in noteExts).
	tryPaths := []string{t}
	for _, e := range noteExts {
		tryPaths = append(tryPaths, t+e)
	}
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
	// 3. basename match across note extensions (.md before .html). "_" and
	// "%" are literal characters in a link target, so they are escaped for
	// LIKE; ORDER BY keeps a multi-match deterministic.
	lowerT := strings.ToLower(t)
	likeT := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(lowerT)
	for _, e := range noteExts {
		if err := i.db.QueryRow(
			`SELECT path FROM notes WHERE lower(path) LIKE ? ESCAPE '\' OR lower(path) = ? ORDER BY path LIMIT 1`,
			"%/"+likeT+e, lowerT+e,
		).Scan(&got); err == nil {
			return got
		}
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
