package authz

import (
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/webauth"
)

// mk builds an AccessConfig from plain maps: vis is project → visibility
// (missing = private), grants is "user|project" → level.
func mk(vis map[string]string, grants map[string]string) AccessConfig {
	return AccessConfig{
		Visibility: func(p string) string {
			if v, ok := vis[p]; ok {
				return v
			}
			return VisibilityPrivate
		},
		Grants: func(u, p string) []GrantSource {
			l := ParseLevel(grants[u+"|"+p])
			if l == LevelNone {
				return nil
			}
			return []GrantSource{{Level: l, Via: "grant:" + l.String()}}
		},
	}
}

// Several sources: the highest wins and every one is reported.
func TestExplain_TeamSources(t *testing.T) {
	cfg := AccessConfig{
		Visibility: func(string) string { return VisibilityPrivate },
		Grants: func(u, p string) []GrantSource {
			return []GrantSource{
				{Level: LevelRead, Via: "grant:read"},
				{Level: LevelWrite, Via: "team:devs:write"},
				{Level: LevelNone, Via: "ignored"},
			}
		},
	}
	lvl, via := (Principal{"u", webauth.RoleMember}).Explain("P", cfg)
	if lvl != LevelWrite || strings.Join(via, ",") != "grant:read,team:devs:write" {
		t.Errorf("level %s via %v", lvl, via)
	}
	if lvl := (Principal{"g", webauth.RoleGuest}).Level("P", cfg); lvl != LevelRead {
		t.Errorf("guest with a team write grant must be capped to read, got %s", lvl)
	}
}

func TestCapabilities(t *testing.T) {
	cases := []struct {
		role                webauth.Role
		write, admin, guest bool
	}{
		{webauth.RoleOwner, true, true, false},
		{webauth.RoleMember, true, false, false},
		{webauth.RoleGuest, false, false, true},
	}
	for _, c := range cases {
		p := Principal{Role: c.role}
		if p.CanWrite() != c.write {
			t.Errorf("%s CanWrite=%v want %v", c.role, p.CanWrite(), c.write)
		}
		if p.CanAdmin() != c.admin {
			t.Errorf("%s CanAdmin=%v want %v", c.role, p.CanAdmin(), c.admin)
		}
		if p.IsGuest() != c.guest {
			t.Errorf("%s IsGuest=%v want %v", c.role, p.IsGuest(), c.guest)
		}
	}
}

// The whole model in one table: role × visibility × grant → level.
func TestLevel_Matrix(t *testing.T) {
	vis := map[string]string{"Pub": VisibilityPublic, "Int": VisibilityInternal, "Priv": VisibilityPrivate}
	grants := map[string]string{
		"alice|Priv": "read", "bob|Priv": "write", "carol|Priv": "admin",
		"bob|Int": "write", "bob|Pub": "admin",
		"gwrite|Priv": "write", "gread|Priv": "read",
		"zed|Priv": "admin",
	}
	cfg := mk(vis, grants)
	owner := webauth.RoleOwner
	member := webauth.RoleMember
	guest := webauth.RoleGuest
	unknown := webauth.Role("")

	cases := []struct {
		name    string
		p       Principal
		project string
		want    Level
	}{
		{"owner-private", Principal{"o", owner}, "Priv", LevelAdmin},
		{"owner-unknown-project", Principal{"o", owner}, "Nope", LevelAdmin},

		{"member-public", Principal{"x", member}, "Pub", LevelRead},
		{"member-internal", Principal{"x", member}, "Int", LevelRead},
		{"member-private", Principal{"x", member}, "Priv", LevelNone},
		{"member-unknown-project", Principal{"x", member}, "Nope", LevelNone},
		{"member-private-read-grant", Principal{"alice", member}, "Priv", LevelRead},
		{"member-private-write-grant", Principal{"bob", member}, "Priv", LevelWrite},
		{"member-private-admin-grant", Principal{"carol", member}, "Priv", LevelAdmin},
		{"member-internal-write-grant", Principal{"bob", member}, "Int", LevelWrite},
		{"member-public-admin-grant", Principal{"bob", member}, "Pub", LevelAdmin},

		{"guest-public", Principal{"g", guest}, "Pub", LevelRead},
		{"guest-internal", Principal{"g", guest}, "Int", LevelNone},
		{"guest-private", Principal{"g", guest}, "Priv", LevelNone},
		{"guest-private-read-grant", Principal{"gread", guest}, "Priv", LevelRead},
		{"guest-private-write-grant-capped", Principal{"gwrite", guest}, "Priv", LevelRead},

		{"unknown-role-public", Principal{"u", unknown}, "Pub", LevelRead},
		{"unknown-role-internal", Principal{"u", unknown}, "Int", LevelNone},
		{"unknown-role-grant-ignored", Principal{"zed", unknown}, "Priv", LevelNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.Level(c.project, cfg); got != c.want {
				t.Errorf("Level = %s want %s", got, c.want)
			}
		})
	}
}

// The three helpers are thresholds on Level.
func TestThresholds(t *testing.T) {
	cfg := mk(map[string]string{"Int": VisibilityInternal}, map[string]string{"w|Int": "write", "a|Int": "admin"})
	member := webauth.RoleMember
	check := func(p Principal, access, write, admin bool) {
		t.Helper()
		if got := p.CanAccessProject("Int", cfg); got != access {
			t.Errorf("%s CanAccessProject=%v want %v", p.UserID, got, access)
		}
		if got := p.CanWriteProject("Int", cfg); got != write {
			t.Errorf("%s CanWriteProject=%v want %v", p.UserID, got, write)
		}
		if got := p.CanAdminProject("Int", cfg); got != admin {
			t.Errorf("%s CanAdminProject=%v want %v", p.UserID, got, admin)
		}
	}
	check(Principal{"r", member}, true, false, false)
	check(Principal{"w", member}, true, true, false)
	check(Principal{"a", member}, true, true, true)
	check(Principal{"o", webauth.RoleOwner}, true, true, true)
}

// A zero-value config fails closed: everything private, no grants — only the
// owner sees anything.
func TestNilConfigFailsClosed(t *testing.T) {
	for _, role := range []webauth.Role{webauth.RoleMember, webauth.RoleGuest, webauth.Role("")} {
		if (Principal{"u", role}).CanAccessProject("any", AccessConfig{}) {
			t.Errorf("%q must not read with a nil config", role)
		}
	}
	if !(Principal{"o", webauth.RoleOwner}).CanAdminProject("any", AccessConfig{}) {
		t.Error("owner must remain admin with a nil config")
	}
}

// Explain names every contributor, so the access views can say why.
func TestExplain_Reasons(t *testing.T) {
	cfg := mk(map[string]string{"Pub": VisibilityPublic}, map[string]string{"bob|Pub": "write"})
	lvl, via := (Principal{"bob", webauth.RoleMember}).Explain("Pub", cfg)
	if lvl != LevelWrite || strings.Join(via, ",") != "public,grant:write" {
		t.Errorf("bob: %s via %v", lvl, via)
	}
	lvl, via = (Principal{"o", webauth.RoleOwner}).Explain("Pub", cfg)
	if lvl != LevelAdmin || strings.Join(via, ",") != "owner" {
		t.Errorf("owner: %s via %v", lvl, via)
	}
	lvl, via = (Principal{"x", webauth.RoleMember}).Explain("Other", cfg)
	if lvl != LevelNone || len(via) != 0 {
		t.Errorf("no access: %s via %v", lvl, via)
	}
}

func TestLevelStrings(t *testing.T) {
	for _, l := range []Level{LevelNone, LevelRead, LevelWrite, LevelAdmin} {
		if l != LevelNone && ParseLevel(l.String()) != l {
			t.Errorf("round trip failed for %s", l)
		}
	}
	if ParseLevel("root") != LevelNone {
		t.Error("unknown level must parse as none")
	}
}
