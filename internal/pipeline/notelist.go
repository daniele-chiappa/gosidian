package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// note.list (IMP-186): the notes of a folder of the project, from the
// index, with memory_query's conditions, a page at a time as in.list
// gives the files of the IN folder. With it a pipeline works on notes it
// did not make: the articles of a folder whose images it describes again.

// NoteQuery is what note.list asks the host.
type NoteQuery struct {
	// Folder is a vault path inside the project, or the project itself.
	Folder    string
	Recursive bool
	// Where holds memory_query's conditions as the pipeline wrote them, a
	// list of {field, op, value}; nil for none.
	Where  any
	Sort   string
	Fields []string
	Limit  int
	Offset int
}

// NoteRow is one note note.list gives.
type NoteRow struct {
	Path   string         `json:"path"`
	Title  string         `json:"title"`
	Fields map[string]any `json:"fields,omitempty"`
}

// NoteLister is what a host offers, beyond Host, to note.list: the notes
// of the project matching q, a page of them, and how many match in all.
type NoteLister interface {
	ListNotes(project string, q NoteQuery) (rows []NoteRow, total int, err error)
}

// A page of note.list.
const (
	noteListDefault = 500
	noteListMax     = 5000
)

func noteList(_ context.Context, env *Env, with map[string]any) (any, error) {
	lister, ok := env.Engine.host.(NoteLister)
	if !ok {
		return nil, errors.New("this server does not list notes to the pipelines")
	}
	q := NoteQuery{
		Folder:    env.Project,
		Recursive: true,
		Where:     with["where"],
		Sort:      strings.TrimSpace(str(with, "sort")),
		Fields:    strList(with["fields"]),
	}
	if f := strings.Trim(strings.TrimSpace(str(with, "folder")), "/"); f != "" && f != env.Project {
		rel, err := env.projectPath(f)
		if err != nil {
			return nil, err
		}
		q.Folder = rel
	}
	if r, ok := with["recursive"].(bool); ok {
		q.Recursive = r
	}
	if q.Sort == "" {
		q.Sort = "path" // a stable order, so the pages follow each other
	}
	q.Limit = int(number(with, "limit", noteListDefault))
	if q.Limit <= 0 {
		q.Limit = noteListDefault
	}
	q.Limit = min(q.Limit, noteListMax)
	if c := strings.TrimSpace(str(with, "cursor")); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("with.cursor %q is not the next of a page", c)
		}
		q.Offset = n
	}
	rows, total, err := lister.ListNotes(env.Project, q)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []NoteRow{}
	}
	next := ""
	if end := q.Offset + len(rows); len(rows) > 0 && end < total {
		next = strconv.Itoa(end)
	}
	return map[string]any{"notes": rows, "count": len(rows), "total": total, "next": next}, nil
}
