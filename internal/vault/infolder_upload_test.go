package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// An upload's folder is in the IN folder, hidden; a file of it takes its
// place with a rename, never over a folder, over a file only with
// replace; a free name is a copy's (IMP-172 phase 5).
func TestINStagingAndPlace(t *testing.T) {
	v := New(t.TempDir())
	v.SetINProjects(func(p string) bool { return p == "p" })
	if err := os.MkdirAll(filepath.Join(v.Root, "p", "IN", "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := v.INStaging("p", "../../x", true); !errors.Is(err, ErrNotIN) {
		t.Fatalf("an id that is a path: %v", err)
	}
	if _, err := v.INStaging("q", "0123456789abcdef", true); !errors.Is(err, ErrNotIN) {
		t.Fatalf("a project without IN: %v", err)
	}
	id := "0123456789abcdef"
	dir, err := v.INStaging("p", id, true)
	if err != nil || dir != filepath.Join(v.Root, "p", "IN", INUploads, id) {
		t.Fatalf("%s %v", dir, err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("0.part", "one")
	e, err := v.INPlace("p", id, "0.part", "p/IN/a/b.txt", false)
	if err != nil || e.Path != "p/IN/a/b.txt" || e.Size != 3 {
		t.Fatalf("placed: %+v %v", e, err)
	}
	write("1.part", "two")
	if _, err := v.INPlace("p", id, "1.part", "p/IN/a/b.txt", false); err == nil {
		t.Fatal("over a file without replace")
	}
	if _, err := v.INPlace("p", id, "1.part", "p/IN/d", true); err == nil {
		t.Fatal("over a folder")
	}
	if _, err := v.INPlace("p", id, "../../../a/b.txt", "p/IN/x", false); err == nil {
		t.Fatal("a staged path out of the folder")
	}
	if _, err := v.INPlace("p", id, "1.part", "p/IN/.hidden", false); err == nil {
		t.Fatal("a hidden target")
	}
	if _, err := v.INPlace("p", id, "1.part", "p/a.md", false); !errors.Is(err, ErrNotIN) {
		t.Fatalf("out of IN: %v", err)
	}
	if e, err := v.INPlace("p", id, "1.part", "p/IN/a/b.txt", true); err != nil || e.Size != 3 {
		t.Fatalf("replace: %+v %v", e, err)
	}
	for want, to := range map[string]string{"p/IN/a/b (2).txt": "p/IN/a/b.txt", "p/IN/d (2)": "p/IN/d", "p/IN/new.txt": "p/IN/new.txt"} {
		if got, err := v.INFreeName(to); err != nil || got != want {
			t.Errorf("INFreeName(%s) = %s %v, want %s", to, got, err, want)
		}
	}
	if ids, err := v.INStagingIDs("p"); err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("ids: %v %v", ids, err)
	}
	if list, _ := v.INReadDir("p/IN"); len(list) != 2 {
		t.Fatalf("the uploads folder shows in IN: %+v", list)
	}
	if err := v.INRemoveStaging("p", id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, "p", "IN", INUploads)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the empty uploads folder stays: %v", err)
	}
	if err := v.INRemoveStaging("p", id); err != nil {
		t.Fatalf("a folder gone: %v", err)
	}
}
