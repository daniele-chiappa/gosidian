package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/graphspec"
	"github.com/gosidian/gosidian/internal/parser"
)

// checkGraphs reports the ```graph blocks (IMP-183) that do not read as a
// saved view of the graph, on the line of their problem, and the notes
// type: graph without exactly one block.
func checkGraphs(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	const hint = "the format: docs/vault/graphs.md; the problem's line is in the note"
	var issues []Issue
	for _, n := range notes {
		if !strings.HasSuffix(strings.ToLower(n.Path), ".md") {
			continue
		}
		blocks := graphspec.Find(n.Content)
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), graphspec.NoteType) && len(blocks) != 1 {
			issues = append(issues, Issue{
				Severity: SeverityWarning, File: n.Path, Rule: "graph-invalid",
				Message: fmt.Sprintf("a note type: graph holds one ```graph block, this one %d", len(blocks)),
				FixHint: hint,
			})
		}
		for _, b := range blocks {
			_, problems := graphspec.Parse(b.Src)
			for _, p := range problems {
				line := b.Line
				if p.Line > 0 {
					line += p.Line
				}
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: line, Rule: "graph-invalid",
					Message: fmt.Sprintf("the graph at line %d: %s", b.Line, p.Msg),
					FixHint: hint,
				})
			}
		}
	}
	return issues, nil
}
