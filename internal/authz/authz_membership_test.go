package authz

import (
	"testing"

	"github.com/gosidian/gosidian/internal/webauth"
)

// cfg builds an AccessConfig from simple maps. members keys are "user|project".
func cfg(enforced bool, public map[string]bool, members map[string]string) AccessConfig {
	return AccessConfig{
		Enforced:       enforced,
		IsPublic:       func(p string) bool { return public[p] },
		IsMember:       func(u, p string) bool { _, ok := members[u+"|"+p]; return ok },
		MemberCanWrite: func(u, p string) bool { return members[u+"|"+p] == "write" },
	}
}

func TestCanAccessProject_Matrix(t *testing.T) {
	public := map[string]bool{"Pub": true}
	members := map[string]string{"alice|Priv": "read", "bob|Priv": "write"}

	cases := []struct {
		name     string
		p        Principal
		enforced bool
		project  string
		access   bool
		write    bool
	}{
		// Owner: everything, both modes.
		{"owner-priv-legacy", Principal{"o", webauth.RoleOwner}, false, "Priv", true, true},
		{"owner-priv-enforced", Principal{"o", webauth.RoleOwner}, true, "Priv", true, true},

		// Member legacy: sees & writes all.
		{"member-priv-legacy", Principal{"x", webauth.RoleMember}, false, "Priv", true, true},

		// Member enforced, NOT a member of Priv: no access, no write.
		{"member-nonmember-enforced", Principal{"x", webauth.RoleMember}, true, "Priv", false, false},
		// Member enforced, read membership: access yes, write no.
		{"member-read-enforced", Principal{"alice", webauth.RoleMember}, true, "Priv", true, false},
		// Member enforced, write membership: access + write.
		{"member-write-enforced", Principal{"bob", webauth.RoleMember}, true, "Priv", true, true},
		// Public project readable by a non-member even when enforced, but writing
		// it still needs a membership (public = read-share, not write-for-all).
		{"member-public-enforced", Principal{"x", webauth.RoleMember}, true, "Pub", true, false},

		// Guest: public read only; never writes even with a (write) membership.
		{"guest-public", Principal{"g", webauth.RoleGuest}, false, "Pub", true, false},
		{"guest-private", Principal{"g", webauth.RoleGuest}, false, "Priv", false, false},
		{"guest-member-read-enforced", Principal{"alice", webauth.RoleGuest}, true, "Priv", true, false},
		{"guest-member-write-enforced", Principal{"bob", webauth.RoleGuest}, true, "Priv", true, false},

		// Zero-value role fails closed.
		{"zero-role", Principal{}, true, "Priv", false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cf := cfg(c.enforced, public, members)
			if got := c.p.CanAccessProject(c.project, cf); got != c.access {
				t.Errorf("CanAccessProject = %v want %v", got, c.access)
			}
			if got := c.p.CanWriteProject(c.project, cf); got != c.write {
				t.Errorf("CanWriteProject = %v want %v", got, c.write)
			}
		})
	}
}
