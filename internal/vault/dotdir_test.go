package vault

import "testing"

// ADR-020: the vault layer must not address hidden entries — any path
// segment starting with "." (the .gosidian/ credential store, .git/, dotfiles)
// is rejected exactly like "..", so no consumer of Rel/Abs can reach them.
func TestVault_RelRejectsDotSegments(t *testing.T) {
	v := newTestVault(t)
	bad := []string{
		".gosidian/auth.json", ".gosidian", ".git/config", ".gitignore",
		"proj/.hidden/x.md", "proj/.draft.md", "proj/sub/.git/config", "/.gosidian/tokens.json",
	}
	for _, b := range bad {
		if _, err := v.Rel(b); err == nil {
			t.Errorf("Rel(%q) should fail", b)
		}
	}
	good := []string{"proj/note.md", "proj/sub.dir/note.md", "proj/attachments/a.png", "proj/v1.2/n.md"}
	for _, g := range good {
		if _, err := v.Rel(g); err != nil {
			t.Errorf("Rel(%q) should pass: %v", g, err)
		}
	}
}
