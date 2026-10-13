package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/graphspec"
)

// noteGraphProblems leaves a note on the current tool call for each
// ```graph block of the note just written that does not read (IMP-183),
// as noteDiagramProblems does: it warns and does not refuse, and the lint
// rule graph-invalid lists it until it is fixed.
func (s *Server) noteGraphProblems(ctx context.Context, rel string, content []byte) {
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		return
	}
	for _, b := range graphspec.Find(content) {
		_, problems := graphspec.Parse(b.Src)
		if len(problems) == 0 {
			continue
		}
		msgs := make([]string, 0, len(problems))
		for _, p := range problems {
			if p.Line > 0 {
				msgs = append(msgs, fmt.Sprintf("line %d: %s", b.Line+p.Line, p.Msg))
			} else {
				msgs = append(msgs, p.Msg)
			}
		}
		addCallNote(ctx, fmt.Sprintf(
			"%s: the ```graph block at line %d does not read as a view of the graph (%s). The note is saved as written; fix the block with memory_edit (fields: source, with, project, tag, focus, depth, min_degree, limit, color, mode).",
			rel, b.Line, strings.Join(msgs, "; ")))
	}
}
