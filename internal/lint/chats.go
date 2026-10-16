package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/chatbot"
	"github.com/gosidian/gosidian/internal/parser"
)

// checkChats reports the ```chat blocks (IMP-191) that do not read as a
// bot, on the line of their problem, the notes type: chat without exactly
// one block, and a block whose pipeline is no note of the project.
func checkChats(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	const hint = "the format: docs/chat.md; the problem's line is in the note"
	paths := map[string]bool{}
	for _, n := range notes {
		paths[n.Path] = true
	}
	var issues []Issue
	for _, n := range notes {
		if !strings.HasSuffix(strings.ToLower(n.Path), ".md") {
			continue
		}
		blocks := chatbot.Find(n.Content)
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), chatbot.NoteType) && len(blocks) != 1 {
			issues = append(issues, Issue{
				Severity: SeverityWarning, File: n.Path, Rule: "chat-invalid",
				Message: fmt.Sprintf("a note type: chat holds one ```chat block, this one %d", len(blocks)),
				FixHint: hint,
			})
		}
		for _, b := range blocks {
			spec, problems := chatbot.Parse(b.Src, n.Path)
			for _, p := range problems {
				line := b.Line
				if p.Line > 0 {
					line += p.Line
				}
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: line, Rule: "chat-invalid",
					Message: fmt.Sprintf("the bot at line %d: %s", b.Line, p.Msg),
					FixHint: hint,
				})
			}
			if len(problems) == 0 && !paths[project+"/"+spec.Pipeline] {
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: b.Line, Rule: "chat-invalid",
					Message: fmt.Sprintf("the bot at line %d: pipeline %s is no note of the project", b.Line, spec.Pipeline),
					FixHint: hint,
				})
			}
		}
	}
	return issues, nil
}
