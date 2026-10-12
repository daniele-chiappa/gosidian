// Package infolder keeps the state of the projects' IN folders (IMP-172,
// ADR-037): which files each holds, with their size, hash and status, and
// the batches they arrived in. The files live in the vault, under
// <project>/IN/; their state lives in a SQLite file of the state dir,
// in.db, which a scan of the folders brings in line with the disk.
package infolder

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Statuses of a file.
const (
	// StatusNew: arrived or changed, and no run took it yet.
	StatusNew = "new"
	// StatusWorking: a run took it.
	StatusWorking = "working"
	// StatusDone: the run that took it ended well, or the pipeline said so.
	StatusDone = "done"
	// StatusError: the run that took it failed, or the pipeline said so.
	StatusError = "error"
	// StatusIgnored: a pipeline, or someone, set it aside.
	StatusIgnored = "ignored"
)

// ValidStatus reports whether s is a status a caller may set.
func ValidStatus(s string) bool {
	switch s {
	case StatusNew, StatusWorking, StatusDone, StatusError, StatusIgnored:
		return true
	}
	return false
}

// SourceLocal is the source of the files on the server's disk.
const SourceLocal = "local"

// File is a file of an IN folder.
type File struct {
	Project string `json:"project"`
	Path    string `json:"path"` // vault-relative: <project>/IN/…
	Name    string `json:"name"`
	Stem    string `json:"stem"`
	Ext     string `json:"ext"`
	Dir     string `json:"dir"` // the folder, relative to IN ("" at its root)
	// Size is -1 for a file of a source whose size is known only when it
	// is read (a native Google document, exported).
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	SHA256   string    `json:"sha256,omitempty"`
	Status   string    `json:"status"`
	Run      string    `json:"run,omitempty"`
	Source   string    `json:"source"`
	// ETag is a remote file's version, as its source gives it (IMP-172
	// phase 2); "" for a local file.
	ETag      string    `json:"etag,omitempty"`
	Batch     string    `json:"batch,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	Updated   time.Time `json:"updated"`
}

// Remote reports whether the file is in a bound source's folder.
func (f File) Remote() bool { return f.Source != "" && f.Source != SourceLocal }

// Version is what tells the file's content apart: its SHA-256, or, for a
// remote file whose source gives none, its etag.
func (f File) Version() string {
	if f.SHA256 != "" {
		return f.SHA256
	}
	return f.ETag
}

// Batch is the files that arrived, or changed, together in a folder of an
// IN folder: its first-level folder ("" for the files at its root). It
// closes when nothing changed there for a while, or a marker says so.
type Batch struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"`
	Dir        string    `json:"dir"`
	Opened     time.Time `json:"opened"`
	LastChange time.Time `json:"last_change"`
	Closed     time.Time `json:"closed,omitzero"`
	Files      int       `json:"files"`
	Bytes      int64     `json:"bytes"`
}

const schema = `
CREATE TABLE IF NOT EXISTS files (
	project    TEXT NOT NULL,
	path       TEXT NOT NULL,
	dir        TEXT NOT NULL,
	size       INTEGER NOT NULL,
	mtime      INTEGER NOT NULL,
	sha256     TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL DEFAULT 'new',
	run        TEXT NOT NULL DEFAULT '',
	source     TEXT NOT NULL DEFAULT 'local',
	batch      TEXT NOT NULL DEFAULT '',
	first_seen INTEGER NOT NULL,
	updated    INTEGER NOT NULL,
	PRIMARY KEY (project, path)
);
CREATE INDEX IF NOT EXISTS files_dir ON files(project, dir);
CREATE INDEX IF NOT EXISTS files_run ON files(run) WHERE run != '';
CREATE INDEX IF NOT EXISTS files_batch ON files(batch) WHERE batch != '';
CREATE TABLE IF NOT EXISTS batches (
	id          TEXT PRIMARY KEY,
	project     TEXT NOT NULL,
	dir         TEXT NOT NULL,
	opened      INTEGER NOT NULL,
	last_change INTEGER NOT NULL,
	closed      INTEGER NOT NULL DEFAULT 0,
	files       INTEGER NOT NULL DEFAULT 0,
	bytes       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS batches_open ON batches(project, dir) WHERE closed = 0;
CREATE TABLE IF NOT EXISTS sources (
	project TEXT NOT NULL,
	folder  TEXT NOT NULL,
	source  TEXT NOT NULL,
	cursor  TEXT NOT NULL DEFAULT '',
	checked INTEGER NOT NULL DEFAULT 0,
	listed  INTEGER NOT NULL DEFAULT 0,
	error   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (project, folder)
);
`

// migrations bring a database of an earlier version in line: the columns
// added since, each with what makes it.
var migrations = []struct{ table, column, ddl string }{
	{"files", "etag", `ALTER TABLE files ADD COLUMN etag TEXT NOT NULL DEFAULT ''`},
}

func migrate(db *sql.DB) error {
	for _, m := range migrations {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, m.table, m.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(m.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// Store is in.db. Writes are serialized: SQLite has one writer.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenStore opens (creating it if needed) the database at path. It is not
// derived data: a file's status says what a pipeline did with it.
func OpenStore(p string) (*Store, error) {
	db, err := sql.Open("sqlite", p+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("in.db: schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("in.db: migration: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// ErrNotFound is returned for a file or a batch the store does not hold.
var ErrNotFound = errors.New("not found")

func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// fileOf completes a row with what its path says.
func fileOf(f File) File {
	f.Name = path.Base(f.Path)
	f.Ext = strings.ToLower(strings.TrimPrefix(path.Ext(f.Name), "."))
	f.Stem = strings.TrimSuffix(f.Name, path.Ext(f.Name))
	return f
}

const fileCols = `project, path, dir, size, mtime, sha256, status, run, source, batch, first_seen, updated, etag`

type scanner interface{ Scan(dest ...any) error }

func scanFile(r scanner) (File, error) {
	var f File
	var mtime, first, updated int64
	if err := r.Scan(&f.Project, &f.Path, &f.Dir, &f.Size, &mtime, &f.SHA256, &f.Status, &f.Run, &f.Source, &f.Batch, &first, &updated, &f.ETag); err != nil {
		return File{}, err
	}
	f.Modified, f.FirstSeen, f.Updated = fromNanos(mtime), fromNanos(first), fromNanos(updated)
	return fileOf(f), nil
}

func scanBatch(r scanner) (Batch, error) {
	var b Batch
	var opened, last, closed int64
	if err := r.Scan(&b.ID, &b.Project, &b.Dir, &opened, &last, &closed, &b.Files, &b.Bytes); err != nil {
		return Batch{}, err
	}
	b.Opened, b.LastChange, b.Closed = fromNanos(opened), fromNanos(last), fromNanos(closed)
	return b, nil
}

const batchCols = `id, project, dir, opened, last_change, closed, files, bytes`

// File returns the file at the vault-relative path p of a project.
func (s *Store) File(project, p string) (File, error) {
	f, err := scanFile(s.db.QueryRow(`SELECT `+fileCols+` FROM files WHERE project = ? AND path = ?`, project, p))
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	return f, err
}

// projectFiles returns every file of a project, by path.
func (s *Store) projectFiles(project string) (map[string]File, error) {
	rows, err := s.db.Query(`SELECT `+fileCols+` FROM files WHERE project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out[f.Path] = f
	}
	return out, rows.Err()
}

// Query selects files of a project.
type Query struct {
	// Folder is relative to the IN folder; "" is all of it.
	Folder string
	// Shallow keeps the files right inside Folder, without its subfolders.
	Shallow bool
	// Match is a glob on the file name (*.pdf, fattura_??.xml).
	Match string
	// Kinds are extensions, without the dot.
	Kinds  []string
	Status string
	Batch  string
	// Limit caps the page: default 500, at most 5000.
	Limit int
	// Cursor is the Next of the page before.
	Cursor string
}

// Page is a page of files, by path.
type Page struct {
	Files []File `json:"files"`
	// Next is the cursor of the next page, "" at the last one.
	Next string `json:"next"`
}

// Limits of a page.
const (
	DefaultLimit = 500
	MaxLimit     = 5000
)

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// List returns a page of the files of a project that q selects.
func (s *Store) List(project string, q Query) (Page, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	if q.Match != "" {
		if _, err := path.Match(q.Match, ""); err != nil {
			return Page{}, fmt.Errorf("match %q: %w", q.Match, err)
		}
	}
	kinds := map[string]bool{}
	for _, k := range q.Kinds {
		kinds[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(k), "."))] = true
	}
	folder := strings.Trim(q.Folder, "/")
	where := []string{"project = ?", "path > ?"}
	args := []any{project, q.Cursor}
	switch {
	case folder != "" && q.Shallow:
		where = append(where, "dir = ?")
		args = append(args, folder)
	case folder != "":
		where = append(where, `(dir = ? OR dir LIKE ? ESCAPE '\')`)
		args = append(args, folder, escapeLike(folder)+"/%")
	case q.Shallow:
		where = append(where, "dir = ''")
	}
	if q.Status != "" {
		where = append(where, "status = ?")
		args = append(args, q.Status)
	}
	if q.Batch != "" {
		where = append(where, "batch = ?")
		args = append(args, q.Batch)
	}
	stmt := `SELECT ` + fileCols + ` FROM files WHERE ` + strings.Join(where, " AND ") + ` ORDER BY path LIMIT ?`
	var page Page
	cursor := q.Cursor
	for {
		args[1] = cursor
		rows, err := s.db.Query(stmt, append(args, 1000)...)
		if err != nil {
			return Page{}, err
		}
		n := 0
		for rows.Next() {
			f, err := scanFile(rows)
			if err != nil {
				rows.Close()
				return Page{}, err
			}
			n++
			cursor = f.Path
			if len(kinds) > 0 && !kinds[f.Ext] {
				continue
			}
			if q.Match != "" {
				if ok, _ := path.Match(q.Match, f.Name); !ok {
					continue
				}
			}
			if len(page.Files) == limit {
				// One more matches: there is a next page, after the last one kept.
				page.Next = page.Files[limit-1].Path
				rows.Close()
				return page, nil
			}
			page.Files = append(page.Files, f)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Page{}, err
		}
		if n < 1000 {
			return page, nil
		}
	}
}

// Folder is a subfolder of a folder of an IN folder, with what it holds.
type Folder struct {
	Path  string `json:"path"` // relative to IN
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	New   int    `json:"new"`
}

// Folders returns the subfolders right inside a folder of a project's IN
// folder ("" for its root) that hold files, each with the files, bytes and
// new files under it, by name.
func (s *Store) Folders(project, folder string) ([]Folder, error) {
	folder = strings.Trim(folder, "/")
	stmt := `SELECT dir, count(*), coalesce(sum(max(size, 0)), 0), coalesce(sum(status = 'new'), 0) FROM files WHERE project = ? AND dir != ''`
	args := []any{project}
	if folder != "" {
		stmt += ` AND dir LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(folder)+"/%")
	}
	rows, err := s.db.Query(stmt+` GROUP BY dir`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*Folder{}
	var names []string
	for rows.Next() {
		var dir string
		var files, fresh int
		var bytes int64
		if err := rows.Scan(&dir, &files, &bytes, &fresh); err != nil {
			return nil, err
		}
		rest := dir
		if folder != "" {
			rest = strings.TrimPrefix(dir, folder+"/")
		}
		child, _, _ := strings.Cut(rest, "/")
		p := child
		if folder != "" {
			p = folder + "/" + child
		}
		f := byName[p]
		if f == nil {
			f = &Folder{Path: p}
			byName[p] = f
			names = append(names, p)
		}
		f.Files += files
		f.Bytes += bytes
		f.New += fresh
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]Folder, 0, len(names))
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out, nil
}

// Counts is what a project's IN folder holds.
type Counts struct {
	Files   int   `json:"files"`
	Bytes   int64 `json:"bytes"`
	New     int   `json:"new"`
	Working int   `json:"working"`
	Errors  int   `json:"errors"`
}

// Counts returns what a project's IN folder holds.
func (s *Store) Counts(project string) (Counts, error) {
	var c Counts
	err := s.db.QueryRow(`SELECT count(*), coalesce(sum(max(size, 0)), 0), coalesce(sum(status = 'new'), 0),
		coalesce(sum(status = 'working'), 0), coalesce(sum(status = 'error'), 0) FROM files WHERE project = ?`, project).
		Scan(&c.Files, &c.Bytes, &c.New, &c.Working, &c.Errors)
	return c, err
}

// SetStatus sets the status of files of a project, and the run that set it
// ("" leaves the run as it is).
func (s *Store) SetStatus(project string, paths []string, status, run string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		if run == "" {
			_, err = tx.Exec(`UPDATE files SET status = ?, updated = ? WHERE project = ? AND path = ?`, status, nanos(now), project, p)
		} else {
			_, err = tx.Exec(`UPDATE files SET status = ?, run = ?, updated = ? WHERE project = ? AND path = ?`, status, run, nanos(now), project, p)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RunEnded sets done or error on the files a run took and left working,
// and returns their projects.
func (s *Store) RunEnded(run string, ok bool, now time.Time) ([]string, error) {
	status := StatusDone
	if !ok {
		status = StatusError
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var projects []string
	rows, err := s.db.Query(`SELECT DISTINCT project FROM files WHERE run = ? AND status = ?`, run, StatusWorking)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		projects = append(projects, p)
	}
	rows.Close()
	if len(projects) == 0 {
		return nil, nil
	}
	_, err = s.db.Exec(`UPDATE files SET status = ?, updated = ? WHERE run = ? AND status = ?`, status, nanos(now), run, StatusWorking)
	return projects, err
}

// Moved follows a move inside a project's IN folder: the file at from, or
// the files under the folder from, now at to. Status, hash and run stay.
func (s *Store) Moved(project, from, to string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT path FROM files WHERE project = ? AND (path = ? OR path LIKE ? ESCAPE '\')`, project, from, escapeLike(from)+"/%")
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, p)
	}
	rows.Close()
	root := project + "/IN/"
	for _, p := range paths {
		np := to + strings.TrimPrefix(p, from)
		if _, err := tx.Exec(`DELETE FROM files WHERE project = ? AND path = ?`, project, np); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE files SET path = ?, dir = ?, updated = ? WHERE project = ? AND path = ?`,
			np, dirOf(root, np), nanos(now), project, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Removed forgets the file at p, or the files under the folder p.
func (s *Store) Removed(project, p string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM files WHERE project = ? AND (path = ? OR path LIKE ? ESCAPE '\')`, project, p, escapeLike(p)+"/%")
	return err
}

// ProjectRenamed follows a project's rename.
func (s *Store) ProjectRenamed(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM files WHERE project = ?`, to); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM batches WHERE project = ?`, to); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sources WHERE project = ?`, to); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT path FROM files WHERE project = ?`, from)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, p)
	}
	rows.Close()
	for _, p := range paths {
		if _, err := tx.Exec(`UPDATE files SET project = ?, path = ? WHERE project = ? AND path = ?`,
			to, to+strings.TrimPrefix(p, from), from, p); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE batches SET project = ? WHERE project = ?`, to, from); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sources SET project = ? WHERE project = ?`, to, from); err != nil {
		return err
	}
	return tx.Commit()
}

// ProjectRemoved forgets a project's files, batches and sources.
func (s *Store) ProjectRemoved(project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range []string{"files", "batches", "sources"} {
		if _, err := s.db.Exec(`DELETE FROM `+t+` WHERE project = ?`, project); err != nil {
			return err
		}
	}
	return nil
}

// Batch returns a batch.
func (s *Store) Batch(id string) (Batch, error) {
	b, err := scanBatch(s.db.QueryRow(`SELECT `+batchCols+` FROM batches WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	return b, err
}

// openBatches returns the open batches of a project, by folder.
func (s *Store) openBatches(project string) (map[string]Batch, error) {
	rows, err := s.db.Query(`SELECT `+batchCols+` FROM batches WHERE project = ? AND closed = 0`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Batch{}
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out[b.Dir] = b
	}
	return out, rows.Err()
}

// dirOf is the folder of the file at the vault-relative path p, relative
// to the IN folder whose path, with a trailing slash, is root.
func dirOf(root, p string) string {
	d := path.Dir(strings.TrimPrefix(p, root))
	if d == "." {
		return ""
	}
	return d
}

// groupOf is the first-level folder of a file's folder: its batch's.
func groupOf(dir string) string {
	g, _, _ := strings.Cut(dir, "/")
	return g
}

// SourceRow is what in.db keeps of a bound source's folder: where its
// changes were last read from, and how its last check went.
type SourceRow struct {
	Project string
	Folder  string
	Source  string // <plugin>/<source>
	Cursor  string
	Checked time.Time
	Listed  time.Time
	Error   string
}

// sourceRows returns the source folders of a project ("" for all), by
// project and folder.
func (s *Store) sourceRows(project string) (map[string]SourceRow, error) {
	stmt := `SELECT project, folder, source, cursor, checked, listed, error FROM sources`
	var args []any
	if project != "" {
		stmt += ` WHERE project = ?`
		args = append(args, project)
	}
	rows, err := s.db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SourceRow{}
	for rows.Next() {
		var r SourceRow
		var checked, listed int64
		if err := rows.Scan(&r.Project, &r.Folder, &r.Source, &r.Cursor, &checked, &listed, &r.Error); err != nil {
			return nil, err
		}
		r.Checked, r.Listed = fromNanos(checked), fromNanos(listed)
		out[r.Project+"/"+r.Folder] = r
	}
	return out, rows.Err()
}

// setSourceRow records a source folder's state.
func (s *Store) setSourceRow(r SourceRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO sources (project, folder, source, cursor, checked, listed, error) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (project, folder) DO UPDATE SET source = excluded.source, cursor = excluded.cursor,
		checked = excluded.checked, listed = excluded.listed, error = excluded.error`,
		r.Project, r.Folder, r.Source, r.Cursor, nanos(r.Checked), nanos(r.Listed), r.Error)
	return err
}

// dropSource forgets a source folder: its row, its files, its open
// batches.
func (s *Store) dropSource(project, folder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prefix := escapeLike(INPrefix(project, folder))
	if _, err := tx.Exec(`DELETE FROM files WHERE project = ? AND path LIKE ? ESCAPE '\'`, project, prefix+"%"); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM batches WHERE project = ? AND closed = 0 AND (dir = ? OR dir LIKE ? ESCAPE '\')`,
		project, folder, escapeLike(folder)+"/%"); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sources WHERE project = ? AND folder = ?`, project, folder); err != nil {
		return err
	}
	return tx.Commit()
}

// folderFiles returns the files under a folder of a project's IN folder,
// by path.
func (s *Store) folderFiles(project, folder string) (map[string]File, error) {
	rows, err := s.db.Query(`SELECT `+fileCols+` FROM files WHERE project = ? AND path LIKE ? ESCAPE '\'`,
		project, escapeLike(INPrefix(project, folder))+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]File{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out[f.Path] = f
	}
	return out, rows.Err()
}

// hasUnder reports whether the store holds a file under the folder p.
func (s *Store) hasUnder(project, p string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM files WHERE project = ? AND path LIKE ? ESCAPE '\' LIMIT 1)`,
		project, escapeLike(p)+"/%").Scan(&n)
	return n > 0, err
}

// INPrefix is the vault path, with a trailing slash, of a folder at the
// root of a project's IN folder.
func INPrefix(project, folder string) string {
	return project + "/IN/" + folder + "/"
}
