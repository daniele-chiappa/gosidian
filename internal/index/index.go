package index

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strconv"
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
	// synchronous(NORMAL): in WAL mode a commit no longer fsyncs, only a
	// checkpoint does. A power loss can drop the last commits but never
	// corrupts the file, and the index is derived — the boot scan
	// (Vault.ScanInto) re-reads every note and drops the ones gone, so lost
	// commits heal at the next start. Under the default FULL every commit
	// fsynced, which made the boot scan (thousands of small commits) take
	// minutes on slow disks.
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	migrated, err := migrate(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := checkContentVersion(db, migrated); err != nil {
		db.Close()
		return nil, fmt.Errorf("content version: %w", err)
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
// like every other table; v3 (IMP-104) adds notes.hash and the meta table, so
// the boot scan can skip the notes whose content has not changed; v4
// (IMP-127 iteration 2) adds links.field, the frontmatter key of a link; v5
// (IMP-127 iteration 3) adds the authors table.
const schemaVersion = 5

// ContentVersion identifies what an upsert extracts from a note: links,
// tags, title, importance, note_fields and the FTS columns. Bump it whenever
// a parser or extraction change would store different rows for the same
// bytes: at the next start every hash is cleared and the boot scan
// re-indexes the whole vault, instead of skipping the unchanged notes and
// keeping rows extracted by the old code. TestContentVersion_Golden fails
// when the extraction output changes without a bump. Link resolution is not
// covered: the boot scan runs ResolveAll every time.
const ContentVersion = 5

// migrate brings an index file to schemaVersion and reports whether it had
// to. The index is a cache of the vault — the boot scan re-upserts every
// note — so a shape change drops and recreates a table instead of
// converting its rows.
func migrate(db *sql.DB) (bool, error) {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return false, err
	}
	if v < 1 {
		if _, err := db.Exec(`DROP TABLE IF EXISTS notes_fts`); err != nil {
			return false, err
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return false, err
	}
	migrated := v < schemaVersion
	if migrated {
		if err := addMissingColumns(db); err != nil {
			return false, err
		}
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
			return false, err
		}
	}
	// After the columns exist: the trigger reads notes.hash.
	if _, err := db.Exec(hashGuardSQL); err != nil {
		return false, err
	}
	return migrated, nil
}

// hashGuardSQL covers a rollback. A release from before v3 rewrites a
// note's rows on upsert but never touches notes.hash, so after an upgrade
// the stale hash would vouch for rows this release did not extract, and
// the boot scan would skip the note for good. Every upsert sets importance
// and only a v3 upsert sets hash, so an update of importance that leaves the
// hash as it was clears it: the next boot scan re-indexes the note. The
// price is one extra re-index at the next start for a note re-upserted with
// the same content (the watcher after an API write).
const hashGuardSQL = `CREATE TRIGGER IF NOT EXISTS notes_hash_guard
AFTER UPDATE OF importance ON notes
WHEN NEW.hash IS OLD.hash AND NEW.hash IS NOT NULL
BEGIN
    UPDATE notes SET hash = NULL WHERE id = NEW.id;
END`

// addMissingColumns adds the columns that CREATE TABLE IF NOT EXISTS leaves
// out of a table created by an older schema.
func addMissingColumns(db *sql.DB) error {
	for _, col := range []struct{ table, name, def string }{
		{"notes", "importance", "INTEGER NOT NULL DEFAULT 3"},
		{"notes", "hash", "TEXT"},
		{"links", "field", "TEXT"},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, col.table, col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + col.table + ` ADD COLUMN ` + col.name + ` ` + col.def); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkContentVersion clears every note hash when the rows may have been
// extracted differently from what this build would store: after a schema
// migration (a new table is empty for the notes the scan would skip) or when
// ContentVersion changed. A cleared hash is the only "re-index me" signal the
// boot scan reads, so a crash halfway through the re-index leaves the rest
// cleared and the next start picks them up.
func checkContentVersion(db *sql.DB, migrated bool) error {
	want := strconv.Itoa(ContentVersion)
	var got string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = 'content_version'`).Scan(&got)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if !migrated && got == want {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE notes SET hash = NULL`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('content_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, want); err != nil {
		return err
	}
	return tx.Commit()
}

// ContentHash is the stamp stored with an indexed note: the boot scan
// re-indexes a note only when the hash of what it would upsert differs.
// The title is part of it because the vault derives it (from the path), so
// it can change while the bytes do not.
func ContentHash(n NoteDoc) string {
	h := sha256.New()
	h.Write([]byte(n.Title))
	h.Write([]byte{0})
	h.Write([]byte(n.Body))
	return hex.EncodeToString(h.Sum(nil))
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
// notes_fts. Existing rows for the same path are replaced. The note's links
// are stored resolved, and the links elsewhere that now match it are
// resolved, in the same transaction: a reader never sees the note with its
// links unresolved, which lint reported as broken right after a write
// (BUG-079).
func (i *Index) Upsert(n NoteDoc) error {
	_, err := i.upsertLocked(n, true)
	return err
}

// UpsertUnresolved stores the note like Upsert but leaves link resolution to
// a ResolveAll once the batch is in. For bulk loads: per-note resolution
// rescans the links table (inbound resolution matches on lower(target),
// which no index serves), so a full scan resolving note by note grows with
// notes × links, while one ResolveAll at the end reaches the same result.
func (i *Index) UpsertUnresolved(n NoteDoc) error {
	_, err := i.upsertLocked(n, false)
	return err
}

// Stamp is what the boot scan compares to decide whether a note needs
// re-indexing. An empty Hash means it does (see checkContentVersion).
type Stamp struct {
	Hash    string
	ModTime int64
	Size    int64
}

// Stamps returns the stamp of every indexed note, keyed by path, in one
// query: the boot scan reads it once instead of querying note by note.
func (i *Index) Stamps() (map[string]Stamp, error) {
	rows, err := i.db.Query(`SELECT path, COALESCE(hash, ''), mtime, size FROM notes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]Stamp)
	for rows.Next() {
		var p string
		var s Stamp
		if err := rows.Scan(&p, &s.Hash, &s.ModTime, &s.Size); err != nil {
			return nil, err
		}
		out[p] = s
	}
	return out, rows.Err()
}

// Touch updates the mtime and size of a note whose content is unchanged
// (a file rewritten with the same bytes, a git checkout): recency and
// memory_stale read mtime, so it must follow the file even when the scan
// skips the re-index.
func (i *Index) Touch(path string, modTime, size int64) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	_, err := i.db.Exec(`UPDATE notes SET mtime = ?, size = ? WHERE path = ?`, modTime, size, path)
	return err
}

// likeEscaper escapes the LIKE wildcards of a link target, which are literal
// characters there (used with ESCAPE '\').
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// resolveInbound resolves the unresolved links elsewhere that the note now
// answers: every unresolved link whose target contains the note's basename
// or one of its titles goes through resolveTarget again, so
// [[plans/<base>]], [[<base>#heading]] and [[<frontmatter title>]] resolve
// as if the note had existed first. Matching the exact target only left
// those unresolved until the next boot. It runs inside the Upsert
// transaction.
func resolveInbound(tx *sql.Tx, notePath string, titles ...string) error {
	base := notePath
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	var conds []string
	var args []any
	seen := map[string]bool{}
	for _, s := range append([]string{stripNoteExt(base)}, titles...) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		conds = append(conds, `lower(target) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscaper.Replace(s)+"%")
	}
	return reresolveLinks(tx,
		`SELECT rowid, target FROM links WHERE (target_path IS NULL OR target_path = '') AND (`+strings.Join(conds, " OR ")+`)`,
		args...)
}

// reresolveLinks runs resolveTarget again, through tx, on the links the
// query selects (rowid, target) and stores the result, NULL when the target
// reaches no note.
func reresolveLinks(tx *sql.Tx, query string, args ...any) error {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return err
	}
	type link struct {
		rowid  int64
		target string
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.rowid, &l.target); err != nil {
			rows.Close()
			return err
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range links {
		if _, err := tx.Exec(`UPDATE links SET target_path = ? WHERE rowid = ?`, nullable(resolveTarget(tx, l.target)), l.rowid); err != nil {
			return err
		}
	}
	return nil
}

// upsertLocked writes the note in one transaction. With resolve, its links
// are stored with their target_path and the inbound links are resolved
// before the commit; without, the links stay unresolved for a ResolveAll.
func (i *Index) upsertLocked(n NoteDoc, resolve bool) (int64, error) {
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
	// The links of the frontmatter's values count as links too, each with
	// its field (IMP-127 iteration 2): backlinks, graph and views see them.
	links = append(links, parser.FrontmatterLinks(meta)...)

	var oldID sql.NullInt64
	_ = tx.QueryRow(`SELECT id FROM notes WHERE path = ?`, n.Path).Scan(&oldID)
	if oldID.Valid {
		if _, err := tx.Exec(`DELETE FROM notes_fts WHERE rowid = ?`, oldID.Int64); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(`
        INSERT INTO notes(path, title, mtime, size, importance, hash) VALUES(?, ?, ?, ?, ?, ?)
        ON CONFLICT(path) DO UPDATE SET title=excluded.title, mtime=excluded.mtime,
            size=excluded.size, importance=excluded.importance, hash=excluded.hash
    `, n.Path, title, n.ModTime, n.Size, parser.Importance(meta), ContentHash(n)); err != nil {
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
		var targetPath any
		if resolve {
			// Read through tx: it sees this note's new row, so a link to
			// the note itself (by path, title or basename) resolves too.
			targetPath = nullable(resolveTarget(tx, l.Target))
		}
		if _, err := tx.Exec(`INSERT INTO links(src_id, target, target_path, alias, field) VALUES(?,?,?,?,?)`,
			id, l.Target, targetPath, l.Alias, nullable(l.Field)); err != nil {
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
	// Who created and last modified it, from the audit log (authors.go).
	if err := syncAuthorFields(tx, n.Path); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(
		`INSERT INTO notes_fts(rowid, title, meta, body) VALUES(?,?,?,?)`,
		id, title, meta, ftsBody,
	); err != nil {
		return 0, err
	}

	if resolve {
		if err := resolveInbound(tx, n.Path, n.Title, title); err != nil {
			return 0, err
		}
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
	// The links that reached this note resolve again without it: another
	// note may answer them, as the note's new path does in a rename, which
	// indexes the new path before deleting the old one. Clearing them left
	// them unresolved until the next boot (BUG-081).
	if err := reresolveLinks(tx, `SELECT rowid, target FROM links WHERE target_path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

// Resolve returns the note path the index links target to, or "" when the
// target reaches no note: the resolution of the links table.
func (i *Index) Resolve(target string) string {
	return resolveTarget(i.db, target)
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
	if len(pending) == 0 {
		return nil
	}

	// Resolve first (reads), then write every link in one transaction: one
	// commit per note instead of one per link, which the boot scan and
	// ResolveAll repeat for the whole vault.
	resolved := make([]string, len(pending))
	for k, p := range pending {
		resolved[k] = resolveTarget(i.db, p.target)
	}
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, p := range pending {
		if _, err := tx.Exec(`UPDATE links SET target_path = ? WHERE rowid = ?`, nullable(resolved[k]), p.rowid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// rowQuerier is what resolveTarget reads through: the database, or the
// transaction of an Upsert, which sees the note being written.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// resolveTarget maps a [[wiki-link]] target to a note path.
// Matches by exact path, title (case-insensitive), or basename (case-insensitive).
func resolveTarget(q rowQuerier, target string) string {
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
		if err := q.QueryRow(`SELECT path FROM notes WHERE path = ?`, p).Scan(&got); err == nil {
			return got
		}
	}
	// 2. title match
	var got string
	if err := q.QueryRow(`SELECT path FROM notes WHERE lower(title) = lower(?) LIMIT 1`, t).Scan(&got); err == nil {
		return got
	}
	// 3. basename match across note extensions (.md before .html). "_" and
	// "%" are literal characters in a link target, so they are escaped for
	// LIKE; ORDER BY keeps a multi-match deterministic.
	lowerT := strings.ToLower(t)
	likeT := likeEscaper.Replace(lowerT)
	for _, e := range noteExts {
		if err := q.QueryRow(
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
