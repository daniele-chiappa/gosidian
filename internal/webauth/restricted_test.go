package webauth

import (
	"path/filepath"
	"testing"
)

// New accounts start restricted (grants only) and able to create projects;
// the owner is neither restricted nor limitable.
func TestRestrictedAndCapabilities(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Setup("owner", "supersecret", false, "test"); err != nil {
		t.Fatal(err)
	}
	owner := s.FirstOwner()
	if owner == nil || owner.Restricted || !owner.CanCreateProjects() {
		t.Fatalf("owner = %+v", owner)
	}
	if err := s.SetRestricted(owner.ID, true); err == nil {
		t.Error("the owner cannot be restricted")
	}
	if err := s.SetCanCreateProjects(owner.ID, false); err == nil {
		t.Error("the owner always creates projects")
	}

	var created []User
	s.SetOnUserCreated(func(u User) { created = append(created, u) })

	m, err := s.AddUser("mia", "mia-pass-1234", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Restricted || !m.CanCreateProjects() {
		t.Errorf("new member: restricted=%v canCreate=%v, want true/true", m.Restricted, m.CanCreateProjects())
	}
	g, err := s.AddLDAPUser("gus")
	if err != nil {
		t.Fatal(err)
	}
	if !g.Restricted || g.CanCreateProjects() {
		t.Errorf("new LDAP guest: restricted=%v canCreate=%v, want true/false", g.Restricted, g.CanCreateProjects())
	}
	if len(created) != 2 || created[0].Username != "mia" || created[1].Username != "gus" {
		t.Errorf("creation hook calls = %+v", created)
	}
	if _, err := s.AddUser("mia", "another-pass-1", RoleMember); err == nil || len(created) != 2 {
		t.Error("a duplicate must fail without firing the hook")
	}

	if err := s.SetRestricted(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCanCreateProjects(m.ID, false); err != nil {
		t.Fatal(err)
	}
	again, _ := s.UserByID(m.ID)
	if again.Restricted || again.CanCreateProjects() {
		t.Errorf("after toggles: %+v", again)
	}
	if err := s.SetRestricted("ghost", true); err == nil {
		t.Error("unknown user must fail")
	}

	// Persisted.
	s2, _ := Open(s.path)
	again2, _ := s2.UserByID(m.ID)
	if again2.Restricted || again2.CanCreateProjects() {
		t.Errorf("not persisted: %+v", again2)
	}
	g2, _ := s2.UserByID(g.ID)
	if !g2.Restricted {
		t.Error("restricted flag not persisted for the LDAP account")
	}
}
