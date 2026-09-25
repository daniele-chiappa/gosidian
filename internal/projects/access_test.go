package projects

import (
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/webauth"
)

// No store wired: every project internal, no grants — members read, guests
// see nothing, only the owner writes.
func TestAccessConfig_NilStoreIsInternalReadOnly(t *testing.T) {
	var s *Store
	cfg := s.AccessConfig()
	member := authz.Principal{UserID: "u", Role: webauth.RoleMember}
	guest := authz.Principal{UserID: "g", Role: webauth.RoleGuest}
	owner := authz.Principal{UserID: "o", Role: webauth.RoleOwner}
	if !member.CanAccessProject("p", cfg) || member.CanWriteProject("p", cfg) {
		t.Error("nil store: member reads every project, writes none")
	}
	if guest.CanAccessProject("p", cfg) {
		t.Error("nil store: nothing is public, guest must see nothing")
	}
	if !owner.CanAdminProject("p", cfg) {
		t.Error("nil store: owner keeps full access")
	}
}

// The closures read the store on every call: visibility and grants changed
// after the config was built are honoured.
func TestAccessConfig_ReflectsStoreLive(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	member := authz.Principal{UserID: "u", Role: webauth.RoleMember}
	guest := authz.Principal{UserID: "g", Role: webauth.RoleGuest}
	cfg := s.AccessConfig()

	if member.CanAccessProject("p", cfg) {
		t.Fatal("private by default: no access without a grant")
	}
	if err := s.Set("p", Flags{Visibility: VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	if !member.CanAccessProject("p", cfg) || member.CanWriteProject("p", cfg) {
		t.Fatal("internal: member reads, does not write")
	}
	if guest.CanAccessProject("p", cfg) {
		t.Fatal("internal: guest does not read")
	}
	if err := s.SetMember("p", "u", LevelWrite); err != nil {
		t.Fatal(err)
	}
	if !member.CanWriteProject("p", cfg) || member.CanAdminProject("p", cfg) {
		t.Fatal("write grant: write yes, admin no")
	}
	if err := s.SetMember("p", "u", LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if !member.CanAdminProject("p", cfg) {
		t.Fatal("admin grant: admin yes")
	}
	if err := s.Set("pub", Flags{Visibility: VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	if !guest.CanAccessProject("pub", cfg) || guest.CanWriteProject("pub", cfg) {
		t.Fatal("public project: guest reads, never writes")
	}
	if err := s.SetMember("pub", "g", LevelWrite); err != nil {
		t.Fatal(err)
	}
	if guest.CanWriteProject("pub", cfg) {
		t.Fatal("guest role is the ceiling: a write grant still reads only")
	}
}
