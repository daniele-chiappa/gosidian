package index

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// NotesByName finds the notes whose title or file name (the last part of
// the path, without its extension) contains q, ignoring case: what a
// wikilink names (IMP-218, F-27). The body and the folders do not count.
// A title that starts with q comes first, then a file name that starts
// with it, then a title that contains it, then a file name; within each,
// by title and path. projects scopes it as RecentNotesIn does: nil every
// note, an empty list none.
func (i *Index) NotesByName(q string, projects []string, limit int) ([]RecentNote, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" || (projects != nil && len(projects) == 0) {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	scope, args := scopeClause(projects, nil)
	query := `SELECT n.path, n.title, n.mtime FROM notes n WHERE 1=1` + scope
	// SQLite's LIKE folds ASCII only: an ASCII q narrows the rows in the
	// query, any other is matched below on every note in scope. The rows
	// are ranked here, so the cost grows with the notes that match: on
	// 24,000 notes 4 ms for a text no note has, 26 ms for one letter.
	if isASCII(needle) {
		like := "%" + likeEscaper.Replace(needle) + "%"
		query += ` AND (n.title LIKE ? ESCAPE '\' OR n.path LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	rows, err := i.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	all, err := scanRecent(rows)
	if err != nil {
		return nil, err
	}
	type named struct {
		note RecentNote
		rank int
		key  string
	}
	var found []named
	for _, n := range all {
		title := strings.ToLower(n.Title)
		name := strings.ToLower(stripNoteExt(path.Base(n.Path)))
		rank := 0
		switch {
		case strings.HasPrefix(title, needle):
		case strings.HasPrefix(name, needle):
			rank = 1
		case strings.Contains(title, needle):
			rank = 2
		case strings.Contains(name, needle):
			rank = 3
		default:
			continue
		}
		found = append(found, named{note: n, rank: rank, key: cmp.Or(title, name)})
	}
	slices.SortFunc(found, func(a, b named) int {
		return cmp.Or(cmp.Compare(a.rank, b.rank), cmp.Compare(a.key, b.key), cmp.Compare(a.note.Path, b.note.Path))
	})
	out := make([]RecentNote, 0, min(len(found), limit))
	for _, f := range found[:min(len(found), limit)] {
		out = append(out, f.note)
	}
	return out, nil
}

func isASCII(s string) bool {
	for k := 0; k < len(s); k++ {
		if s[k] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
