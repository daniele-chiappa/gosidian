package views

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gosidian/gosidian/internal/dbschema"
)

// Row views (IMP-139): the views a database declares in its schema
// (row_views), which every row shows below its body. Each is a view spec in
// which `this` is the row, so `related contains this` lists the notes whose
// related field points at it and `links contains this` all those that link
// to it. Nothing is written to the row: the web UI shows them after the
// note, and an agent gets them with render_views.

// RowResult is a computed row view: its title, and the result or why it
// could not be computed.
type RowResult struct {
	Title  string
	Result *Result
	Err    error
}

// ComputeRowViews computes the row views of a database for the row that c
// describes (c.This): each with q, in the order declared.
func ComputeRowViews(rvs []dbschema.RowView, c Context, q QueryFunc) []RowResult {
	out := make([]RowResult, 0, len(rvs))
	for _, rv := range rvs {
		r, err := compute(rv.Spec, c, q)
		out = append(out, RowResult{Title: rv.Title, Result: r, Err: err})
	}
	return out
}

// Markers of the row views appended to a row for an agent: they say the
// text is computed, and let a copy pasted into the note be dropped.
const (
	rowViewsOpen  = "<!-- gosidian:row-views — computed when the note is read, from the row_views of %s; not part of the file, never copy it into the note -->"
	rowViewsClose = "<!-- /gosidian:row-views -->"
)

var persistedRowViewsRe = regexp.MustCompile(`(?s)\n?<!-- gosidian:row-views .*?` + regexp.QuoteMeta(rowViewsClose) + `\n?`)

// StripRowViews drops row views copied into a note by mistake, so they are
// not shown twice.
func StripRowViews(body []byte) []byte {
	return persistedRowViewsRe.ReplaceAll(body, []byte("\n"))
}

// RowViewsMarkdown renders computed row views for an agent, to append after
// the row's body: each title in bold, then its rows as a ```view block shows
// them (an empty view says so), all between markers. database is the
// database note that declares them. "" when there are none.
func RowViewsMarkdown(database string, rs []RowResult) string {
	if len(rs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + fmt.Sprintf(rowViewsOpen, database) + "\n")
	for _, r := range rs {
		b.WriteString("\n**" + r.Title + "**\n\n")
		if r.Err != nil {
			b.WriteString(warning(r.Err))
			continue
		}
		b.WriteString(r.Result.Markdown())
	}
	b.WriteString("\n" + rowViewsClose + "\n")
	return b.String()
}

// RowViewData is a computed row view in the form of the web UI: its title,
// the view as data (as POST /preview returns a note's views), and its rows
// as markdown, which the web UI renders for a list.
type RowViewData struct {
	Title    string `json:"title"`
	View     Data   `json:"view"`
	Markdown string `json:"markdown"`
}

// RowViewsData returns computed row views in the form of the web UI.
func RowViewsData(rs []RowResult, c Context) []RowViewData {
	out := make([]RowViewData, 0, len(rs))
	for _, r := range rs {
		if r.Err != nil {
			out = append(out, RowViewData{Title: r.Title, View: Data{Error: r.Err.Error()}, Markdown: warning(r.Err)})
			continue
		}
		out = append(out, RowViewData{Title: r.Title, View: r.Result.Data(c), Markdown: r.Result.Markdown()})
	}
	return out
}
