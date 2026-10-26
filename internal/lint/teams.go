package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/parser"
)

// checkTeams reports the ```team blocks (IMP-195) that do not read as a
// team, on the line of their problem, the notes type: team without exactly
// one block, and a role whose agent is no note of the vault.
func checkTeams(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	const hint = "the format: docs/agents.md; the problem's line is in the note"
	paths := map[string]bool{}
	for _, n := range notes {
		paths[n.Path] = true
	}
	// isNote tells a note of the project, or of another project by the index.
	isNote := func(p string) (bool, error) {
		if paths[p] {
			return true, nil
		}
		row, err := l.index.Note(p)
		return row != nil, err
	}
	var issues []Issue
	for _, n := range notes {
		if !strings.HasSuffix(strings.ToLower(n.Path), ".md") {
			continue
		}
		blocks := fleet.Find(n.Content)
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), fleet.TeamType) && len(blocks) != 1 {
			issues = append(issues, Issue{
				Severity: SeverityWarning, File: n.Path, Rule: "team-invalid",
				Message: fmt.Sprintf("a note type: team holds one ```team block, this one %d", len(blocks)),
				FixHint: hint,
			})
		}
		for _, b := range blocks {
			team, problems := fleet.Parse(b.Src, n.Path)
			for _, p := range problems {
				line := b.Line
				if p.Line > 0 {
					line += p.Line
				}
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: line, Rule: "team-invalid",
					Message: fmt.Sprintf("the team at line %d: %s", b.Line, p.Msg),
					FixHint: hint,
				})
			}
			if len(problems) > 0 {
				continue
			}
			for _, r := range team.Roles {
				if r.Agent == "" {
					continue
				}
				ok, err := isNote(r.Agent)
				if err != nil {
					return nil, err
				}
				if !ok {
					issues = append(issues, Issue{
						Severity: SeverityWarning, File: n.Path, Line: b.Line, Rule: "team-invalid",
						Message: fmt.Sprintf("the team at line %d: role %s: agent %s is no note of the vault", b.Line, r.Name, r.Agent),
						FixHint: hint,
					})
				}
			}
		}
	}
	return issues, nil
}
