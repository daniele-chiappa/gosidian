package index

import (
	"database/sql"
	"strings"
	"time"
)

// Authors (IMP-127 iteration 3, phase 2): who created and who last modified
// each note through gosidian, read off the audit log. The authors table
// holds them per path; queries see them as the fields created_by and
// modified_by (note_fields rows of source audit) of the notes whose
// frontmatter has no field of that name, so fields, where, sort, views and
// counts take them like any field. Only the writes gosidian audits count: a
// git pull or an editor outside it changes no author.

// Author fields as queries name them: who, and when (an ISO date-time,
// which compares and sorts as a date).
const (
	CreatedByField  = "created_by"
	ModifiedByField = "modified_by"
	CreatedAtField  = "created_at"
	ModifiedAtField = "modified_at"
)

// authorColumns maps each author field to its column of the authors table,
// and says whether it is a date.
var authorColumns = []struct {
	key, col string
	date     bool
}{
	{CreatedByField, "created_by", false},
	{ModifiedByField, "modified_by", false},
	{CreatedAtField, "created_at", true},
	{ModifiedAtField, "modified_at", true},
}

// Author actions, named as the audit log names them.
const (
	AuthorCreate        = "create"
	AuthorUpdate        = "update"
	AuthorAppend        = "append"
	AuthorDelete        = "delete"
	AuthorRename        = "rename"
	AuthorDeleteProject = "delete_project"
	AuthorRenameProject = "rename_project"
)

// AuthorEvent is a write of the audit log as the authors table reads it: By
// is who wrote, as shown (a token's name and account, or a username); Path
// is a note, or a project for the project actions; To is the new path or
// name of a rename.
type AuthorEvent struct {
	TS     time.Time
	Action string
	Path   string
	To     string
	By     string
}

// Author is the authors row of one note.
type Author struct {
	CreatedBy, CreatedAt, ModifiedBy, ModifiedAt string
}

type authorTable map[string]*Author

// apply folds one event into t: a create sets the creator, every write the
// last modifier, a rename moves the row (the renamer modifies), a delete
// drops it, and the project actions do the same for every row of the
// project. Other actions are ignored. Only a create names the creator: a
// note whose creation the log does not hold (written before the audit, or
// outside gosidian) has none, rather than its first writer passed off as
// one.
func (t authorTable) apply(e AuthorEvent) {
	at := e.TS.UTC().Format(time.RFC3339)
	switch e.Action {
	case AuthorCreate, AuthorUpdate, AuthorAppend:
		a := t[e.Path]
		if a == nil {
			a = &Author{}
			t[e.Path] = a
		}
		if e.Action == AuthorCreate {
			a.CreatedBy, a.CreatedAt = e.By, at
		}
		a.ModifiedBy, a.ModifiedAt = e.By, at
	case AuthorRename:
		if a := t[e.Path]; a != nil && e.To != "" {
			delete(t, e.Path)
			a.ModifiedBy, a.ModifiedAt = e.By, at
			t[e.To] = a
		}
	case AuthorDelete:
		delete(t, e.Path)
	case AuthorDeleteProject:
		prefix := e.Path + "/"
		for p := range t {
			if strings.HasPrefix(p, prefix) {
				delete(t, p)
			}
		}
	case AuthorRenameProject:
		if e.To == "" {
			return
		}
		prefix := e.Path + "/"
		for p, a := range t {
			if strings.HasPrefix(p, prefix) {
				delete(t, p)
				t[e.To+"/"+strings.TrimPrefix(p, prefix)] = a
			}
		}
	}
}

// RebuildAuthors replaces the authors table with the replay of events, in
// the order of the log, and refreshes the author fields of every note.
// Called at start, before the first query.
func (i *Index) RebuildAuthors(events []AuthorEvent) error {
	t := authorTable{}
	for _, e := range events {
		t.apply(e)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.Exec(`DELETE FROM authors`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO authors(path, created_by, created_at, modified_by, modified_at) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for p, a := range t {
		if _, err := stmt.Exec(p, a.CreatedBy, a.CreatedAt, a.ModifiedBy, a.ModifiedAt); err != nil {
			return err
		}
	}
	if err := syncAuthorFields(tx, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordAuthor applies one event of the audit log as it is written, and
// refreshes the author fields of the notes it touches.
func (i *Index) RecordAuthor(e AuthorEvent) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	// The rows the event can touch, loaded into a small table, applied,
	// and written back.
	var like string
	paths := []string{e.Path}
	switch e.Action {
	case AuthorCreate, AuthorUpdate, AuthorAppend, AuthorDelete:
	case AuthorRename:
		paths = append(paths, e.To)
	case AuthorDeleteProject, AuthorRenameProject:
		like = likeUnder(e.Path)
	default:
		return nil
	}
	t := authorTable{}
	q := `SELECT path, created_by, created_at, modified_by, modified_at FROM authors WHERE path IN (` + placeholders(len(paths)) + `)`
	args := make([]any, 0, len(paths)+1)
	for _, p := range paths {
		args = append(args, p)
	}
	if like != "" {
		q += ` OR path LIKE ? ESCAPE '\'`
		args = append(args, like)
	}
	rows, err := tx.Query(q, args...)
	if err != nil {
		return err
	}
	before := []string{}
	for rows.Next() {
		var p string
		a := &Author{}
		if err := rows.Scan(&p, &a.CreatedBy, &a.CreatedAt, &a.ModifiedBy, &a.ModifiedAt); err != nil {
			rows.Close()
			return err
		}
		t[p] = a
		before = append(before, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	t.apply(e)
	touched := append(before, paths...)
	if e.Action == AuthorRenameProject {
		for p := range t {
			touched = append(touched, p)
		}
	}
	for _, p := range touched {
		if _, err := tx.Exec(`DELETE FROM authors WHERE path = ?`, p); err != nil {
			return err
		}
	}
	for p, a := range t {
		if _, err := tx.Exec(`INSERT INTO authors(path, created_by, created_at, modified_by, modified_at) VALUES(?,?,?,?,?)`,
			p, a.CreatedBy, a.CreatedAt, a.ModifiedBy, a.ModifiedAt); err != nil {
			return err
		}
	}
	for _, p := range touched {
		if err := syncAuthorFields(tx, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AuthorOf returns the authors row of a note, and whether it has one.
func (i *Index) AuthorOf(path string) (Author, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	var a Author
	err := i.db.QueryRow(`SELECT created_by, created_at, modified_by, modified_at FROM authors WHERE path = ?`, path).
		Scan(&a.CreatedBy, &a.CreatedAt, &a.ModifiedBy, &a.ModifiedAt)
	if err == sql.ErrNoRows {
		return Author{}, false, nil
	}
	return a, err == nil, err
}

// syncAuthorFields rewrites the author fields (note_fields rows of source
// audit) of the note at path, or of every note when path is "": created_by,
// modified_by, created_at and modified_at from the authors table, unless the
// frontmatter has a field of that name, which wins.
func syncAuthorFields(tx *sql.Tx, path string) error {
	where, args := "", []any{}
	if path != "" {
		where, args = ` AND n.path = ?`, []any{path}
	}
	del := `DELETE FROM note_fields WHERE source = 'audit'`
	if path != "" {
		del += ` AND note_id IN (SELECT id FROM notes n WHERE 1=1` + where + `)`
	}
	if _, err := tx.Exec(del, args...); err != nil {
		return err
	}
	for _, ac := range authorColumns {
		date := "NULL"
		if ac.date {
			date = "a." + ac.col
		}
		q := `INSERT INTO note_fields(note_id, key, value, num, date, source)
SELECT n.id, ?, a.` + ac.col + `, NULL, ` + date + `, 'audit' FROM notes n JOIN authors a ON a.path = n.path
WHERE a.` + ac.col + ` <> '' AND NOT EXISTS (SELECT 1 FROM note_fields f WHERE f.note_id = n.id AND f.key = ? AND f.source <> 'audit')` + where
		if _, err := tx.Exec(q, append([]any{ac.key, ac.key}, args...)...); err != nil {
			return err
		}
	}
	return nil
}
