package projectops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/trash"
)

func (f fixture) token(t *testing.T, name string, scope ...string) {
	t.Helper()
	if _, _, err := f.tokens.Create(name, scope, []string{auth.ScopeRead}, 0, ""); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) bin() *trash.Bin { return trash.New(f.v.Root, -1) }

// Create starts from a fresh entry: what a deleted project left under the
// name (visibility, member grants) does not reach the new project, and a
// name MCP tokens are scoped to is refused before anything changes
// (IMP-124).
func TestCreate_FreshEntryAndScopedNames(t *testing.T) {
	f := newFixture(t)
	if err := f.ps.Set("ghost", projects.Flags{Visibility: projects.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	if err := f.ps.SetMember("ghost", "stranger", projects.LevelAdmin); err != nil {
		t.Fatal(err)
	}
	name, err := Create(f.v, f.ps, f.tokens, " ghost ", projects.VisibilityPrivate, "alice")
	if err != nil || name != "ghost" {
		t.Fatalf("Create = %q, %v", name, err)
	}
	if f.ps.Visibility("ghost") != projects.VisibilityPrivate {
		t.Errorf("visibility = %q, want private", f.ps.Visibility("ghost"))
	}
	if _, ok := f.ps.MemberLevel("ghost", "stranger"); ok {
		t.Error("the leftover grant reached the new project")
	}
	if lvl, _ := f.ps.MemberLevel("ghost", "alice"); lvl != projects.LevelAdmin {
		t.Errorf("creator grant = %q, want admin", lvl)
	}
	since := f.ps.Get("ghost").Since
	if since == 0 {
		t.Error("Create did not record when the project took its name")
	}
	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "ghost", "ghost2"); err != nil {
		t.Fatal(err)
	}
	if got := f.ps.Get("ghost2").Since; got <= since {
		t.Errorf("rename kept Since %d, want a later one than %d", got, since)
	}
	if _, err := Rename(f.v, f.idx, f.ps, f.tokens, "ghost2", "ghost"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(f.v, f.ps, f.tokens, "ghost", "", ""); !errors.Is(err, ErrExists) {
		t.Errorf("second create = %v, want ErrExists", err)
	}

	f.token(t, "early", "taken")
	if _, err := Create(f.v, f.ps, f.tokens, "taken", "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create onto a scoped name = %v, want ErrInvalid", err)
	}
	if folderExists(f.v, "taken") || f.ps.Access("taken").Members != nil {
		t.Error("a refused create left a folder or an entry")
	}

	// Without a visibility the store default applies.
	if err := f.ps.SetDefaultVisibility(projects.VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(f.v, f.ps, f.tokens, "plain", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := f.ps.Get("plain").Visibility; got != projects.VisibilityInternal {
		t.Errorf("pinned visibility = %q, want internal", got)
	}
}

// Delete moves the project to the trash with its access, drops its index
// and access entries, revokes the tokens scoped to it alone and narrows the
// others; RestoreProject brings it back as it was, notes indexed.
func TestDeleteAndRestore_CarryAccess(t *testing.T) {
	f := newFixture(t)
	bin := f.bin()
	f.note(t, "p/a.md")
	if err := f.ps.SetAccess("p", projects.Access{
		Flags:   projects.Flags{Visibility: projects.VisibilityPrivate},
		Members: []projects.ProjectMember{{UserID: "alice", Level: projects.LevelAdmin}},
	}); err != nil {
		t.Fatal(err)
	}
	f.token(t, "only-p", "p")
	f.token(t, "p-and-q", "p", "q")
	f.token(t, "q", "q")

	res, err := Delete(f.v, f.idx, f.ps, f.tokens, bin, "p")
	if err != nil {
		t.Fatal(err)
	}
	if res.TrashID == "" || res.TokensRevoked != 1 || res.TokensNarrowed != 1 || !slices.Equal(res.Removed, []string{"p/a.md"}) {
		t.Errorf("DeleteResult = %+v", res)
	}
	if folderExists(f.v, "p") {
		t.Error("the folder is still in the vault")
	}
	if a := f.ps.Access("p"); len(a.Members) != 0 || f.ps.Get("p") != (projects.Flags{}) {
		t.Errorf("access entry left behind: %+v", a)
	}
	if outs, _ := f.idx.Outlinks("p/a.md"); outs != nil {
		t.Errorf("index still has p/a.md: %+v", outs)
	}
	scopes := f.scopes(t)
	if _, ok := scopes["only-p"]; ok {
		t.Error("the token scoped to p alone was not revoked")
	}
	if !slices.Equal(scopes["p-and-q"], []string{"q"}) || !slices.Equal(scopes["q"], []string{"q"}) {
		t.Errorf("scopes after delete = %v", scopes)
	}
	saved, ok, err := TrashedAccess(bin, res.TrashID)
	if err != nil || !ok || saved.Flags.Visibility != projects.VisibilityPrivate {
		t.Fatalf("TrashedAccess = %+v, %v, %v", saved, ok, err)
	}

	rr, err := RestoreProject(f.v, f.idx, f.ps, f.tokens, bin, res.TrashID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if !rr.AccessRestored || rr.Name != "p" || !slices.Equal(rr.Restored, []string{"p/a.md"}) {
		t.Errorf("RestoreResult = %+v", rr)
	}
	if f.ps.Visibility("p") != projects.VisibilityPrivate {
		t.Errorf("restored visibility = %q, want private", f.ps.Visibility("p"))
	}
	if lvl, _ := f.ps.MemberLevel("p", "alice"); lvl != projects.LevelAdmin {
		t.Errorf("alice after restore = %q, want admin", lvl)
	}
	if _, ok := f.ps.MemberLevel("p", "bob"); ok {
		t.Error("the restorer gained a grant on a project restored with its access")
	}
	if hits, _ := f.idx.NotesByPrefix("p"); len(hits) != 1 {
		t.Errorf("restored notes in the index = %d, want 1", len(hits))
	}
}

// A project trashed without its access (before IMP-124) comes back private
// to the restorer, never under the default visibility; a name tokens are
// scoped to, or one a project holds again, is refused and the entry stays.
func TestRestoreProject_WithoutAccessAndRefusals(t *testing.T) {
	f := newFixture(t)
	bin := f.bin()
	if err := f.ps.SetDefaultVisibility(projects.VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	f.note(t, "old/n.md")
	id, _, err := bin.DiscardProject("old", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.note(t, "dup/n.md")
	dupID, _, err := bin.DiscardProject("dup", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.note(t, "dup/other.md") // a new project took the name

	f.token(t, "early", "old")
	if _, err := RestoreProject(f.v, f.idx, f.ps, f.tokens, bin, id, "carol"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("restore onto a scoped name = %v, want ErrInvalid", err)
	}
	if _, err := RestoreProject(f.v, f.idx, f.ps, f.tokens, bin, dupID, "carol"); !errors.Is(err, ErrExists) {
		t.Fatalf("restore onto an existing project = %v, want ErrExists", err)
	}
	if _, err := os.Stat(filepath.Join(f.v.Root, ".gosidian", "trash", id)); err != nil {
		t.Fatalf("a refused restore lost the trash entry: %v", err)
	}

	if _, _, err := f.tokens.RemoveProject("old"); err != nil {
		t.Fatal(err)
	}
	rr, err := RestoreProject(f.v, f.idx, f.ps, f.tokens, bin, id, "carol")
	if err != nil {
		t.Fatal(err)
	}
	if rr.AccessRestored {
		t.Error("AccessRestored with no saved access")
	}
	if f.ps.Visibility("old") != projects.VisibilityPrivate {
		t.Errorf("visibility = %q, want private (not the internal default)", f.ps.Visibility("old"))
	}
	if lvl, _ := f.ps.MemberLevel("old", "carol"); lvl != projects.LevelAdmin {
		t.Errorf("restorer grant = %q, want admin", lvl)
	}
}

// Without a trash the folder is removed from disk, and the rest follows.
func TestDelete_WithoutTrash(t *testing.T) {
	f := newFixture(t)
	f.note(t, "p/a.md")
	f.token(t, "only-p", "p")
	res, err := Delete(f.v, f.idx, f.ps, f.tokens, nil, "p")
	if err != nil || res.TrashID != "" || res.TokensRevoked != 1 {
		t.Fatalf("Delete = %+v, %v", res, err)
	}
	if folderExists(f.v, "p") {
		t.Error("folder still there")
	}
	if _, err := Delete(f.v, f.idx, f.ps, f.tokens, nil, "p"); !errors.Is(err, ErrInvalid) {
		t.Errorf("deleting a missing project = %v, want ErrInvalid", err)
	}
}

// An older project restored after a later one with the same name trashed
// notes and went to the trash too does not inherit those notes: it counts
// as named at the restore (Since = now), so they stay the owner's.
func TestRestoreProject_LaterIncarnationNotesStayAway(t *testing.T) {
	f := newFixture(t)
	bin := f.bin()
	if _, err := Create(f.v, f.ps, f.tokens, "p", projects.VisibilityPrivate, "alice"); err != nil {
		t.Fatal(err)
	}
	f.note(t, "p/alice.md")
	first, err := Delete(f.v, f.idx, f.ps, f.tokens, bin, "p")
	if err != nil {
		t.Fatal(err)
	}
	saved, _, _ := TrashedAccess(bin, first.TrashID)

	if _, err := Create(f.v, f.ps, f.tokens, "p", projects.VisibilityPrivate, "bob"); err != nil {
		t.Fatal(err)
	}
	f.note(t, "p/bob.md")
	if _, err := bin.DiscardNote("p/bob.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(f.v, f.idx, f.ps, f.tokens, bin, "p"); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreProject(f.v, f.idx, f.ps, f.tokens, bin, first.TrashID, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := f.ps.Get("p").Since; got <= saved.Flags.Since {
		t.Errorf("restored Since = %d, want later than the saved %d", got, saved.Flags.Since)
	}
	var bobNote trash.Entry
	entries, _ := bin.List()
	for _, e := range entries {
		if e.OriginPath == "p/bob.md" {
			bobNote = e
		}
	}
	if bobNote.ID == "" || bobNote.DiscardedAt.UnixNano() >= f.ps.Get("p").Since {
		t.Errorf("bob's note (%+v) is not older than the restored project's Since", bobNote)
	}
}
