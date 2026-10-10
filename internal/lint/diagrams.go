package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/diagram"
	"github.com/gosidian/gosidian/internal/parser"
)

// maxDiagramIssues caps the issues of one diagram: past a few, the block
// wants rewriting more than a list.
const maxDiagramIssues = 10

// checkDiagrams reports the ```diagram blocks (ADR-036) that do not read as
// a diagram of the format, on the line of their problem, and the notes
// type: diagram without exactly one block.
func checkDiagrams(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	const hint = "the format, its kinds and examples: docs/diagrams.md; the problem's line is in the note"
	var issues []Issue
	for _, n := range notes {
		if !strings.HasSuffix(strings.ToLower(n.Path), ".md") {
			continue
		}
		blocks := diagram.Find(n.Content)
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), diagram.NoteType) && len(blocks) != 1 {
			issues = append(issues, Issue{
				Severity: SeverityWarning, File: n.Path, Rule: "diagram-invalid",
				Message: fmt.Sprintf("a note type: diagram holds one ```diagram block, this one %d", len(blocks)),
				FixHint: hint,
			})
		}
		for _, b := range blocks {
			_, problems := diagram.Parse(b.Src)
			for i, p := range problems {
				if i == maxDiagramIssues {
					issues = append(issues, Issue{
						Severity: SeverityWarning, File: n.Path, Line: b.Line, Rule: "diagram-invalid",
						Message: fmt.Sprintf("the diagram at line %d has %d more problems", b.Line, len(problems)-i),
						FixHint: hint,
					})
					break
				}
				line := b.Line
				if p.Line > 0 {
					line += p.Line
				}
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: line, Rule: "diagram-invalid",
					Message: fmt.Sprintf("the diagram at line %d: %s", b.Line, p.Msg),
					FixHint: hint,
				})
			}
		}
	}
	return issues, nil
}
