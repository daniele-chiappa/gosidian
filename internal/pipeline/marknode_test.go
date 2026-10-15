package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/marks"
)

// PutMarks makes the fake a Marker.
func (h *fakeHost) PutMarks(project string, set marks.Set) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !strings.HasPrefix(set.Path, project+"/") {
		return ErrNoNote
	}
	if err := set.Check(marks.DefaultLimits); err != nil {
		return err
	}
	if h.marks == nil {
		h.marks = map[string]marks.Set{}
	}
	h.marks[set.Path+"#"+set.Name] = set
	return nil
}

func TestNoteMark(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/docs/a.md"] = "# A\n\n## B\n\nText.\n"
	env := &Env{Project: "p", Engine: e}
	env.Run.Ref, env.Run.ID = "p/pipelines/cut.md", "r1"
	out, err := noteMark(context.Background(), env, map[string]any{"path": "docs/a", "set": "taglio", "marks": []any{
		map[string]any{"base": "aa.bb", "color": "red", "label": "senza descrizione", "text": "L'immagine 2"},
		map[string]any{"section": "B", "block": 1, "color": "orange"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.(map[string]any); m["path"] != "p/docs/a.md" || m["count"] != 2 {
		t.Errorf("output %v", m)
	}
	set := host.marks["p/docs/a.md#taglio"]
	if set.Pipeline != "p/pipelines/cut.md" || set.Run != "r1" || len(set.Hash) != 64 || len(set.Marks) != 2 || set.Marks[1].Block != 1 || set.Marks[0].Label != "senza descrizione" {
		t.Errorf("set %+v", set)
	}
	for name, with := range map[string]map[string]any{
		"color":   {"path": "docs/a.md", "set": "taglio", "marks": []any{map[string]any{"color": "pink"}}},
		"set":     {"path": "docs/a.md", "set": "Taglio!"},
		"marks":   {"path": "docs/a.md", "set": "taglio", "marks": "red"},
		"missing": {"path": "docs/none.md", "set": "taglio"},
	} {
		if _, err := noteMark(context.Background(), env, with); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
