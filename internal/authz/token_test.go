package authz

import (
	"errors"
	"slices"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// One rule narrows an MCP token to its owner's live access, for the MCP
// server and /vault-files/ alike (IMP-161).
func TestNarrowToken(t *testing.T) {
	cfg := AccessConfig{
		Visibility: func(string) string { return "private" },
		Grants: func(_, project string) []GrantSource {
			switch project {
			case "r":
				return []GrantSource{{Level: LevelRead}}
			case "w":
				return []GrantSource{{Level: LevelWrite}}
			}
			return nil
		},
	}
	all := func() ([]string, error) { return []string{"r", "w", "x"}, nil }
	member := Principal{UserID: "u", Role: webauth.RoleMember}
	tok := &auth.Token{Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}

	eff, err := NarrowToken(tok, member, cfg, all)
	if err != nil || !slices.Equal(eff.Projects, []string{"r", "w"}) || eff.AllowsWrite("r/n.md") || !eff.AllowsWrite("w/n.md") {
		t.Errorf("unscoped token of a member = %+v, %v", eff, err)
	}
	eff, err = NarrowToken(&auth.Token{Projects: []string{"r"}, Scopes: tok.Scopes}, member, cfg, all)
	if err != nil || slices.Contains(eff.Scopes, auth.ScopeWrite) {
		t.Errorf("token on a read project keeps write: %+v, %v", eff, err)
	}
	if _, err := NarrowToken(&auth.Token{Projects: []string{"x"}, Scopes: tok.Scopes}, member, cfg, all); !errors.Is(err, ErrNoAccess) {
		t.Errorf("token on no readable project: %v", err)
	}
	if eff, _ := NarrowToken(tok, Principal{Role: webauth.RoleOwner}, cfg, all); eff != tok {
		t.Error("the owner's token is not kept whole")
	}
}
