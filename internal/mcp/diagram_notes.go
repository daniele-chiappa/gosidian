package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/diagram"
)

// maxDiagramProblemsNoted caps the problems of one diagram in a call's note.
const maxDiagramProblemsNoted = 5

// noteDiagramProblems leaves a note on the current tool call for each
// ```diagram block of the note just written that does not read (ADR-036),
// with the problems on the note's own lines. Like the schema notes, it
// warns and does not refuse: a diagram half written must not stop a note,
// and the lint rule diagram-invalid lists it until it is fixed. A Mermaid
// twin left older than its diagram gets a note too (diagram-twin-stale).
func (s *Server) noteDiagramProblems(ctx context.Context, rel string, content []byte) {
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		return
	}
	for _, b := range diagram.Find(content) {
		_, problems := diagram.Parse(b.Src)
		if len(problems) == 0 {
			continue
		}
		msgs := make([]string, 0, maxDiagramProblemsNoted+1)
		for i, p := range problems {
			if i == maxDiagramProblemsNoted {
				msgs = append(msgs, fmt.Sprintf("%d more", len(problems)-i))
				break
			}
			if p.Line > 0 {
				msgs = append(msgs, fmt.Sprintf("line %d: %s", b.Line+p.Line, p.Msg))
			} else {
				msgs = append(msgs, p.Msg)
			}
		}
		addCallNote(ctx, fmt.Sprintf(
			"%s: the ```diagram block at line %d does not read as a diagram (%s). The note is saved as written; fix the block with memory_edit (the format and its kinds: flow, tree, mindmap, fishbone, venn, matrix, gantt).",
			rel, b.Line, strings.Join(msgs, "; ")))
	}
	for _, t := range diagram.Twins(content) {
		if t.State == diagram.TwinStale {
			addCallNote(ctx, fmt.Sprintf(
				"%s: the ```mermaid block at line %d is the twin of the diagram at line %d, made from an earlier version of it: Obsidian shows the old drawing. Saving the diagram in the web UI's editor writes the twin again; or delete the twin block.",
				rel, t.Line, t.DiagramLine))
		}
	}
}
