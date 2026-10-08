package vault

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A state dir set inside the vault under a visible name is hidden like a dot
// folder: not addressable, not listed, not a project, and no project
// operation takes it along (BUG-098).
func TestVault_StateDirInsideIsHidden(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "Alpha/a.md", "# a")
	write(t, v.Root, "Alpha/attachments/a.png", "PNG")
	write(t, v.Root, "state/notes.md", "# not a note")
	write(t, v.Root, "state/tokens.json", "{}")
	write(t, v.Root, "state/attachments/s.png", "PNG")
	v.SetStateDir(filepath.Join(v.Root, "state"))

	if got := v.StateDirInside(); got != "state" {
		t.Fatalf("StateDirInside = %q", got)
	}
	for _, p := range []string{"state", "state/notes.md", "state/tokens.json", "/state/x.md"} {
		if _, err := v.Rel(p); err == nil {
			t.Errorf("Rel(%q) should fail", p)
		}
	}
	if _, err := v.Rel("stateful/a.md"); err != nil {
		t.Errorf("Rel of a sibling with the same prefix: %v", err)
	}
	if notes, _ := v.List(); !slices.Equal(notes, []string{"Alpha/a.md"}) {
		t.Errorf("List = %v", notes)
	}
	projs, _ := v.Projects()
	if len(projs) != 1 || projs[0].Name != "Alpha" {
		t.Errorf("Projects = %v", projs)
	}
	if _, err := v.ProjectNotes("state"); err == nil {
		t.Error("ProjectNotes of the state dir should fail")
	}
	atts, _ := v.ListAttachments("", map[string]bool{".png": true})
	if len(atts) != 1 || atts[0].Path != "Alpha/attachments/a.png" {
		t.Errorf("ListAttachments = %v", atts)
	}
	if _, err := v.ListAttachments("state", map[string]bool{".png": true}); err == nil {
		t.Error("ListAttachments of the state dir should fail")
	}
	if _, err := v.DeleteProject("state"); err == nil {
		t.Error("DeleteProject removed the state dir")
	}
	if err := v.RenameProject(openIndex(t), "state", "moved"); err == nil {
		t.Error("RenameProject moved the state dir")
	}
	if _, err := v.CreateProject("state"); err == nil {
		t.Error("CreateProject took the state dir's name")
	}
	if !v.Exists("Alpha/a.md") || v.Exists("state/notes.md") {
		t.Error("Exists does not follow Rel")
	}
}

// A state dir deeper in a project, even under a hidden name, makes the
// project and its folders hold it: they are not moved or removed.
func TestVault_FolderHoldingTheStateDir(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "Alpha/a.md", "# a")
	write(t, v.Root, "Alpha/sub/.state/tokens.json", "{}")
	v.SetStateDir(filepath.Join(v.Root, "Alpha", "sub", ".state"))

	for _, rel := range []string{"Alpha", "Alpha/sub"} {
		if !v.HoldsStateDir(rel) {
			t.Errorf("HoldsStateDir(%q) = false", rel)
		}
	}
	for _, rel := range []string{"Alpha/sub/.state", "Alph", "Beta"} {
		if v.HoldsStateDir(rel) {
			t.Errorf("HoldsStateDir(%q) = true", rel)
		}
	}
	if _, err := v.CheckProject("Alpha"); err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("CheckProject of the holder: %v", err)
	}
	if _, err := v.DeleteProject("Alpha"); err == nil {
		t.Error("DeleteProject removed the folder holding the state dir")
	}
	if notes, _ := v.List(); !slices.Equal(notes, []string{"Alpha/a.md"}) {
		t.Errorf("List = %v", notes)
	}
}

// Outside the vault, or at its default place, the state dir changes nothing.
func TestVault_StateDirOutside(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "state/a.md", "# a")
	v.SetStateDir(filepath.Join(t.TempDir(), "state"))
	if v.StateDirInside() != "" {
		t.Errorf("StateDirInside = %q for a dir outside", v.StateDirInside())
	}
	if _, err := v.Rel("state/a.md"); err != nil {
		t.Errorf("Rel: %v", err)
	}
	v.SetStateDir(v.Root)
	if v.StateDirInside() != "" {
		t.Error("the vault root counted as a state dir inside the vault")
	}
	v.SetStateDir(filepath.Join(v.Root, ".gosidian"))
	if _, err := v.CheckProject("state"); err != nil {
		t.Errorf("CheckProject with the default state dir: %v", err)
	}
}
