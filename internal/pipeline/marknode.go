package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/marks"
)

// note.mark (IMP-188): a set of marks on a note — coloured spans with a
// label and a text, which the web UI shows beside it, never in it. A run
// replaces its set; a set without marks goes.

// Marker is what a host offers to keep the marks of the notes.
type Marker interface {
	PutMarks(project string, set marks.Set) error
}

func noteMark(_ context.Context, env *Env, with map[string]any) (any, error) {
	p, err := required(with, "path")
	if err != nil {
		return nil, err
	}
	rel, err := env.projectPath(p)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(rel, ".md") {
		rel += ".md"
	}
	name, err := required(with, "set")
	if err != nil {
		return nil, err
	}
	var list []marks.Mark
	if v, ok := with["marks"]; ok && v != nil {
		if _, ok := v.([]any); !ok {
			return nil, errors.New("with.marks: a list of {base or section and block, color, label, text}")
		}
		b, _ := json.Marshal(v)
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("with.marks: %w", err)
		}
	}
	marker, ok := env.Engine.host.(Marker)
	if !ok {
		return nil, errors.New("this server keeps no marks")
	}
	note, err := env.Engine.host.ReadNote(env.Project, rel)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(note.Raw)
	// The store checks the set, under the caps of the config.
	set := marks.Set{Path: rel, Name: name, Pipeline: env.Run.Ref, Run: env.Run.ID, Hash: hex.EncodeToString(sum[:]), Marks: list}
	if err := marker.PutMarks(env.Project, set); err != nil {
		return nil, err
	}
	return map[string]any{"path": rel, "set": name, "count": len(list)}, nil
}
