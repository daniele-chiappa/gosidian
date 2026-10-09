package vault

import "testing"

// Project names that are not a portable folder name are refused: control
// characters (a NUL made every os call fail, after the rename had written
// the access store), a trailing dot, the Windows device names (IMP-125).
func TestCheckProject_Portable(t *testing.T) {
	v := newTestVault(t)
	refused := []string{
		"x\x00y", "tab\there", "bell\x07", "del\x7f",
		"ok.", "ok..",
		"CON", "con", "Nul", "aux.txt", "PRN.md", "COM1", "lpt9", "COM0.log",
	}
	for _, name := range refused {
		if got, err := v.CheckProject(name); err == nil {
			t.Errorf("CheckProject(%q) = %q, want an error", name, got)
		}
	}
	allowed := []string{"gosidian", "my project", "COM10", "console", "nullable", "LPT", "a.b", "über", "  trimmed  "}
	for _, name := range allowed {
		if _, err := v.CheckProject(name); err != nil {
			t.Errorf("CheckProject(%q): %v", name, err)
		}
	}
}

// A project whose name is no longer portable, made before the rule, is
// still deleted and renamed: refused there too, it could not even leave the
// trash (BUG-115, S5-10). As a new name it is refused.
func TestCheckProject_ExistingNames(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "con/a.md", "# a")
	write(t, v.Root, "Misc./b.md", "# b")
	if _, err := v.CheckExistingProject("con"); err != nil {
		t.Errorf("CheckExistingProject(con): %v", err)
	}
	if err := v.RenameProject(openIndex(t), "con", "console"); err != nil {
		t.Errorf("rename of an existing con: %v", err)
	}
	if _, err := v.DeleteProject("Misc."); err != nil {
		t.Errorf("delete of an existing Misc.: %v", err)
	}
	if _, err := v.CreateProject("aux"); err == nil {
		t.Error("a new project named aux was created")
	}
	if err := v.RenameProject(openIndex(t), "console", "nul"); err == nil {
		t.Error("a rename to nul was accepted")
	}
	if _, err := v.CheckExistingProject("../x"); err == nil {
		t.Error("an escape passed as an existing name")
	}
}
