package marks

import (
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "marks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStore(t *testing.T) {
	s := open(t)
	cut := Set{Path: "rag/docs/à b.md", Name: "taglio", Pipeline: "rag/pipelines/taglia.md", Run: "r1", Hash: "h1",
		Marks: []Mark{{Base: "aa.bb", Color: "red", Label: "senza descrizione", Text: "L'immagine 3 non ha descrizione"}, {Section: "A > B", Block: 2, Color: "orange"}}}
	if err := s.Put(cut); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Set{Path: cut.Path, Name: "review", Hash: "h1", Marks: []Mark{{Section: "A", Color: "green", Label: "rivista"}}}); err != nil {
		t.Fatal(err)
	}
	sets, err := s.Sets(cut.Path)
	if err != nil || len(sets) != 2 || sets[1].Name != "taglio" || len(sets[1].Marks) != 2 || sets[1].Marks[1].Block != 2 || sets[1].Run != "r1" || sets[1].Created.IsZero() {
		t.Fatalf("sets %+v %v", sets, err)
	}
	// A run replaces its set; no marks removes it.
	cut.Run, cut.Marks = "r2", cut.Marks[:1]
	if err := s.Put(cut); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Set{Path: cut.Path, Name: "review", Hash: "h1"}); err != nil {
		t.Fatal(err)
	}
	if sets, _ := s.Sets(cut.Path); len(sets) != 1 || sets[0].Run != "r2" || len(sets[0].Marks) != 1 {
		t.Fatalf("replaced %+v", sets)
	}
	// Renames and deletes follow the notes, one or under a folder.
	if err := s.Renamed(cut.Path, "rag/docs/c.md"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Set{Path: "rag/docs/sub/d.md", Name: "taglio", Hash: "h", Marks: []Mark{{Color: "blue"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Renamed("rag/docs/", "rag/manuali/"); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]int{"rag/docs/c.md": 0, "rag/manuali/c.md": 1, "rag/manuali/sub/d.md": 1} {
		if sets, _ := s.Sets(p); len(sets) != want {
			t.Errorf("%s: %d sets, want %d", p, len(sets), want)
		}
	}
	if err := s.Deleted("rag/manuali/c.md"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted("rag/"); err != nil {
		t.Fatal(err)
	}
	if sets, _ := s.Sets("rag/manuali/sub/d.md"); len(sets) != 0 {
		t.Errorf("after deleting the project: %+v", sets)
	}
}

func TestSet_Check(t *testing.T) {
	ok := Set{Path: "p/a.md", Name: "cut", Marks: []Mark{{Color: "cyan"}}}
	d := DefaultLimits
	if err := ok.Check(d); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Set{
		"name":  {Name: "Cut!", Marks: ok.Marks},
		"color": {Name: "cut", Marks: []Mark{{Color: "pink"}}},
		"label": {Name: "cut", Marks: []Mark{{Color: "red", Label: strings.Repeat("x", d.Label+1)}}},
		"block": {Name: "cut", Marks: []Mark{{Color: "red", Block: -1}}},
		"many":  {Name: "cut", Marks: make([]Mark, d.Marks+1)},
	} {
		if s.Check(d) == nil {
			t.Errorf("%s: no error", name)
		}
	}
	st := open(t)
	for i := range d.Sets {
		if err := st.Put(Set{Path: "p/a.md", Name: "s" + string(rune('a'+i)), Hash: "h", Marks: ok.Marks}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Put(Set{Path: "p/a.md", Name: "one-more", Hash: "h", Marks: ok.Marks}); err == nil {
		t.Error("a set past the cap")
	}
}

// The config raises the caps, and a 0 keeps the default.
func TestStore_Limits(t *testing.T) {
	st := open(t)
	st.SetLimits(Limits{Sets: 1, Text: 1000})
	long := Set{Path: "p/a.md", Name: "a", Hash: "h", Marks: []Mark{{Color: "red", Text: strings.Repeat("x", 800)}}}
	if err := st.Put(long); err != nil {
		t.Fatalf("a text of 800 under a cap of 1000: %v", err)
	}
	if err := st.Put(Set{Path: "p/a.md", Name: "b", Hash: "h", Marks: []Mark{{Color: "red"}}}); err == nil {
		t.Error("a second set past a cap of 1")
	}
	if st.limits.Marks != DefaultLimits.Marks || st.limits.Label != DefaultLimits.Label {
		t.Errorf("limits %+v", st.limits)
	}
}

// A folder whose name is not ASCII moves and goes with its notes: SQLite
// counts its prefix in characters.
func TestStore_FolderNotASCII(t *testing.T) {
	s := open(t)
	if err := s.Put(Set{Path: "rag/contabilità/a.md", Name: "taglio", Hash: "h", Marks: []Mark{{Color: "blue"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Renamed("rag/contabilità/", "rag/conti/"); err != nil {
		t.Fatal(err)
	}
	if sets, _ := s.Sets("rag/conti/a.md"); len(sets) != 1 {
		t.Fatalf("renamed: %+v", sets)
	}
	if err := s.Renamed("rag/conti/", "rag/società/"); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted("rag/società/"); err != nil {
		t.Fatal(err)
	}
	if sets, _ := s.Sets("rag/società/a.md"); len(sets) != 0 {
		t.Errorf("after deleting the folder: %+v", sets)
	}
}
