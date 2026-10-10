package lint

import (
	"context"

	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/pipeline"
)

// WithPipelineTypes sets the node types a pipeline may use (the engine's,
// with its plugins); without it the base node types.
func (l *Linter) WithPipelineTypes(types pipeline.Types) *Linter {
	l.pipelineTypes = types
	return l
}

// checkPipelines reports the pipeline notes (type: pipeline, IMP-167) the
// server would refuse to run: no ```pipeline block, a block that does not
// parse, a node type that does not exist, a reference to a node that does
// not run before. It checks whether the server runs pipelines at all.
func checkPipelines(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	types := l.pipelineTypes
	if types == nil {
		types = pipeline.NewRegistry()
	}
	var issues []Issue
	for _, n := range notes {
		if !noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), pipeline.NoteType) {
			continue
		}
		p := pipeline.FromNote(n.Path, project, n.Title, n.Content, types)
		if p.Err == nil {
			continue
		}
		issues = append(issues, Issue{
			Severity: SeverityWarning,
			File:     n.Path,
			Rule:     "pipeline-invalid",
			Message:  "the pipeline cannot run: " + p.Err.Error(),
			FixHint:  "fix the ```pipeline block (format and node types: docs/pipelines.md); memory_pipelines shows the same problem",
		})
	}
	return issues, nil
}
