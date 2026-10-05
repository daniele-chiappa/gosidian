package views

import (
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/parser"
)

// Snapshots (IMP-127 iteration 3, phase 3): a note frozen as it reads at one
// moment, saved beside it as a dated note, so that the history of what its
// views showed is kept by the vault's git and can be read back. The note
// itself does not change.

// FreezeStats counts what Freeze replaced.
type FreezeStats struct {
	Views  int `json:"views"`
	Values int `json:"values"`
	Embeds int `json:"embeds"`
}

// Freeze renders a note body as a fixed document: its embeds included, each
// view replaced by its result, each value by its number with its expression
// kept as text, between double backticks, which no read computes. Nothing
// in the result is computed again when it is read.
func Freeze(body []byte, c Context, q QueryFunc) ([]byte, FreezeStats) {
	var st FreezeStats
	st.Embeds = CountEmbeds(body, c)
	body, _ = ExpandEmbeds(body, false, c)

	blocks := FindBlocks(body)
	if len(blocks) > 0 {
		var out bytes.Buffer
		last := 0
		for _, b := range blocks {
			res := ""
			if r, err := compute(b.Spec, c, q); err != nil {
				res = warning(err)
			} else {
				res = r.Markdown()
			}
			out.Write(body[last:b.Start])
			out.WriteString(res)
			last = b.End
		}
		out.Write(body[last:])
		body = out.Bytes()
		st.Views = len(blocks)
	}

	body = valueCopyRe.ReplaceAll(body, []byte("$1"))
	vals := FindValues(body)
	if len(vals) > 0 {
		var out bytes.Buffer
		last := 0
		for _, v := range vals {
			span := string(body[v.Start:v.End])
			res := ""
			if n, err := countValue(v.Expr, c, q); err != nil {
				res = "⚠️ count: " + strings.ReplaceAll(err.Error(), "\n", " ")
			} else {
				res = strconv.Itoa(n) + " (`` " + span + " ``)"
			}
			out.Write(body[last:v.Start])
			out.WriteString(res)
			last = v.End
		}
		out.Write(body[last:])
		body = out.Bytes()
		st.Values = len(vals)
	}
	return body, st
}

// SnapshotPath is where a snapshot of the note at rel taken at now goes:
// <folder>/<name>.snapshots/YYYY-MM-DD.md, with the time added when that
// name is taken (a second snapshot of the day), and the seconds after that.
func SnapshotPath(rel string, now time.Time, exists func(string) bool) string {
	dir := path.Join(path.Dir(rel), strings.TrimSuffix(path.Base(rel), path.Ext(rel))+".snapshots")
	now = now.UTC()
	for _, layout := range []string{"2006-01-02", "2006-01-02-1504", "2006-01-02-150405"} {
		p := dir + "/" + now.Format(layout) + ".md"
		if !exists(p) {
			return p
		}
	}
	return dir + "/" + now.Format("2006-01-02-150405.000000000") + ".md"
}

// SnapshotNote is the content of a snapshot of the note at rel, titled
// title, of project: a frontmatter that says what it is (type: snapshot,
// the source, the date, the project's tag only, so the snapshot of a plan
// is no plan), a line that says it, then the frozen body.
func SnapshotNote(rel, title, project string, frozen []byte, now time.Time) []byte {
	now = now.UTC()
	link := "[[" + strings.TrimSuffix(rel, ".md") + "]]"
	// The note's own title, from its frontmatter, over the one given (the
	// vault's, which may be the file name).
	if t, ok := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(frozen))["title"].(string); ok && strings.TrimSpace(t) != "" {
		title = strings.TrimSpace(t)
	}
	if title == "" {
		title = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(title+" — snapshot "+now.Format("2006-01-02 15:04")))
	b.WriteString("type: snapshot\n")
	fmt.Fprintf(&b, "source: %s\n", yamlQuote(link))
	fmt.Fprintf(&b, "date: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "tags: [%s]\n", project)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "> Snapshot of %s taken %s UTC: its views, embeds and counts are frozen as they were then.\n\n", link, now.Format("2006-01-02 15:04"))
	b.WriteString(strings.TrimLeft(parser.BodyAfterFrontmatter(frozen), "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// yamlQuote writes s as a double-quoted YAML scalar.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ").Replace(s) + `"`
}
