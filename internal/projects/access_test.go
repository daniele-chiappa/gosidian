package projects

import (
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/webauth"
)

func TestAccessConfig_NilStoreIsLegacy(t *testing.T) {
	var s *Store
	cfg := s.AccessConfig()
	member := authz.Principal{UserID: "u", Role: webauth.RoleMember}
	guest := authz.Principal{UserID: "g", Role: webauth.RoleGuest}
	if !member.CanAccessProject("p", cfg) {
		t.Error("nil store: member must keep legacy access to every project")
	}
	if guest.CanAccessProject("p", cfg) {
		t.Error("nil store: nothing is public, guest must see nothing")
	}
}

func TestAccessConfig_ReflectsStoreLive(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	member := authz.Principal{UserID: "u", Role: webauth.RoleMember}
	cfg := s.AccessConfig()

	if !member.CanAccessProject("p", cfg) || !member.CanWriteProject("p", cfg) {
		t.Fatal("legacy mode: member reads and writes everywhere")
	}
	if err := s.SetMemberScope(MemberScopeMembers); err != nil {
		t.Fatal(err)
	}
	// Enforced is sampled when the config is built (callers build one per
	// request); the membership closures read the store on every call.
	cfg = s.AccessConfig()
	if member.CanAccessProject("p", cfg) {
		t.Fatal("enforced without membership: no access")
	}
	if err := s.SetMember("p", "u", LevelRead); err != nil {
		t.Fatal(err)
	}
	if !member.CanAccessProject("p", cfg) || member.CanWriteProject("p", cfg) {
		t.Fatal("read membership: read yes, write no")
	}
	if err := s.SetMember("p", "u", LevelWrite); err != nil {
		t.Fatal(err)
	}
	if !member.CanWriteProject("p", cfg) {
		t.Fatal("write membership: write yes")
	}
	if err := s.Set("pub", Flags{Public: true}); err != nil {
		t.Fatal(err)
	}
	if !member.CanAccessProject("pub", cfg) || member.CanWriteProject("pub", cfg) {
		t.Fatal("public project: readable without membership, not writable")
	}
}
