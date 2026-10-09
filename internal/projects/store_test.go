package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.Get("alpha"); got != (Flags{}) {
		t.Errorf("missing project should yield zero Flags, got %+v", got)
	}
	if err := s.Set("alpha", Flags{SkipGitSync: true}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("beta", Flags{HiddenFromMCP: true, SkipGitSync: true}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := s.Get("alpha"); !got.SkipGitSync || got.HiddenFromMCP {
		t.Errorf("alpha = %+v", got)
	}
	if got := s.Get("beta"); !got.SkipGitSync || !got.HiddenFromMCP {
		t.Errorf("beta = %+v", got)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open reload: %v", err)
	}
	if got := s2.Get("alpha"); !got.SkipGitSync {
		t.Errorf("reload lost alpha: %+v", got)
	}
	if got := s2.Get("beta"); !got.SkipGitSync || !got.HiddenFromMCP {
		t.Errorf("reload lost beta: %+v", got)
	}
}

func TestStore_FileLayout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	if err := s.Set("alpha", Flags{HiddenFromMCP: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sf storeFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatal(err)
	}
	if !sf.Projects["alpha"].HiddenFromMCP {
		t.Errorf("on-disk alpha not hidden: %+v", sf.Projects)
	}
	if sf.Projects["alpha"].SkipGitSync {
		t.Errorf("zero field leaked: %+v", sf.Projects["alpha"])
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file perms = %o, want 0600", st.Mode().Perm())
	}
}

func TestStore_DeleteAndZeroSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	if err := s.Set("alpha", Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("alpha", Flags{}); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("alpha"); got != (Flags{}) {
		t.Errorf("zero Set should remove entry, got %+v", got)
	}
	if err := s.Set("beta", Flags{HiddenFromMCP: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("beta"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("beta"); got != (Flags{}) {
		t.Errorf("Delete didn't remove entry: %+v", got)
	}
	if err := s.Delete("nonexistent"); err != nil {
		t.Errorf("Delete missing should be no-op: %v", err)
	}
}

func TestStore_Rename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	_ = s.Set("alpha", Flags{SkipGitSync: true, HiddenFromMCP: true})
	if err := s.Rename("alpha", "alpha2"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("alpha"); got != (Flags{}) {
		t.Errorf("old still present: %+v", got)
	}
	if got := s.Get("alpha2"); !got.SkipGitSync || !got.HiddenFromMCP {
		t.Errorf("new lost flags: %+v", got)
	}
	// A source without an entry still stamps the target as just named.
	if err := s.Rename("nonexistent", "whatever"); err != nil {
		t.Errorf("rename of a name without entry: %v", err)
	}
	if s.Get("whatever").Since == 0 {
		t.Error("rename did not record when the target took its name")
	}
}

func TestStore_All(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	_ = s.Set("zeta", Flags{HiddenFromMCP: true})
	_ = s.Set("alpha", Flags{SkipGitSync: true})
	_ = s.Set("mid", Flags{SkipGitSync: true, HiddenFromMCP: true})
	all := s.All()
	want := []Entry{
		{Name: "alpha", Flags: Flags{SkipGitSync: true}},
		{Name: "mid", Flags: Flags{SkipGitSync: true, HiddenFromMCP: true}},
		{Name: "zeta", Flags: Flags{HiddenFromMCP: true}},
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("All() = %+v, want %+v", all, want)
	}
}

func TestStore_SkipNamesForGit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	_ = s.Set("a", Flags{SkipGitSync: true})
	_ = s.Set("b", Flags{HiddenFromMCP: true})
	_ = s.Set("c", Flags{SkipGitSync: true, HiddenFromMCP: true})
	got := s.SkipNamesForGit()
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SkipNamesForGit = %v, want %v", got, want)
	}
}

func TestStore_ReloadIfStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	_ = s.Set("alpha", Flags{SkipGitSync: true})

	external := storeFile{Projects: map[string]Flags{
		"alpha": {SkipGitSync: false, HiddenFromMCP: true},
		"beta":  {SkipGitSync: true},
	}}
	raw, _ := json.MarshalIndent(external, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// bump mtime by enough to be observable on coarse-grained filesystems
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	if got := s.Get("alpha"); got.SkipGitSync || !got.HiddenFromMCP {
		t.Errorf("stale alpha not reloaded: %+v", got)
	}
	if got := s.Get("beta"); !got.SkipGitSync {
		t.Errorf("stale beta not reloaded: %+v", got)
	}
}

func TestStore_InvalidName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")
	s, _ := Open(path)
	for _, bad := range []string{"", "with/slash", "with\\back"} {
		if err := s.Set(bad, Flags{SkipGitSync: true}); err == nil {
			t.Errorf("Set(%q) should reject", bad)
		}
		if err := s.Rename("anything", bad); err == nil {
			t.Errorf("Rename to %q should reject", bad)
		}
	}
}

func TestStore_OpenMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does", "not", "exist", "projects.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open missing file should succeed: %v", err)
	}
	if got := s.Get("anything"); got != (Flags{}) {
		t.Errorf("missing file should yield zero Flags, got %+v", got)
	}
}

// The access lookups check the file at most once per staleCheckEvery: they
// run once per project and note of a request, and each stat'ed the file
// (IMP-159). A plain read and every write still check at once.
func TestStore_AccessLookupsCheckTheFileOncePerInterval(t *testing.T) {
	prev := staleCheckEvery
	staleCheckEvery = time.Hour
	t.Cleanup(func() { staleCheckEvery = prev })
	path := filepath.Join(t.TempDir(), "projects.json")
	s, _ := Open(path)
	_ = s.Set("p", Flags{Visibility: VisibilityPrivate})
	if s.Visibility("p") != VisibilityPrivate {
		t.Fatal("setup")
	}
	raw, _ := json.MarshalIndent(storeFile{Projects: map[string]Flags{"p": {Visibility: VisibilityPublic}}}, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)
	if s.Visibility("p") != VisibilityPrivate {
		t.Error("the access lookup stat'ed the file within the interval")
	}
	if s.Get("p").Visibility != VisibilityPublic {
		t.Error("a plain read must still see the change at once")
	}
	if s.Visibility("p") != VisibilityPublic {
		t.Error("after a reload the lookup sees the change")
	}
}
