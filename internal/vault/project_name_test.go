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
