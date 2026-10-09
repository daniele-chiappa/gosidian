package authz

import (
	"errors"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/webauth"
)

// ErrNoAccess is a token whose account reads none of its projects.
var ErrNoAccess = errors.New("the account that owns this token has no readable project")

// NarrowToken is an MCP token cut down to what the account that owns it
// may do now, p (the owner's principal): its project list, or every
// project (projects) when it has none, kept to the ones p reads, the write
// scope dropped when p writes none of them and narrowed when p writes
// some. An owner account keeps the token whole. The result is a copy.
//
// It is the one rule for the MCP server (every request) and the
// attachments an MCP token downloads from /vault-files/, which kept a copy
// of it (IMP-161).
func NarrowToken(tok *auth.Token, p Principal, cfg AccessConfig, projects func() ([]string, error)) (*auth.Token, error) {
	if p.Role == webauth.RoleOwner {
		return tok, nil
	}
	candidates := tok.ProjectList()
	if len(candidates) == 0 {
		names, err := projects()
		if err != nil {
			return nil, err
		}
		candidates = names
	}
	readable := make([]string, 0, len(candidates))
	writable := map[string]bool{}
	for _, name := range candidates {
		if !p.CanAccessProject(name, cfg) {
			continue
		}
		readable = append(readable, name)
		if p.CanWriteProject(name, cfg) {
			writable[name] = true
		}
	}
	if len(readable) == 0 {
		return nil, ErrNoAccess
	}
	eff := *tok
	eff.Project = ""
	eff.Projects = readable
	switch {
	case len(writable) == 0:
		eff.Scopes = withoutScope(tok.Scopes, auth.ScopeWrite)
	case len(writable) < len(readable):
		return eff.WithWriteFilter(func(project string) bool { return writable[project] }), nil
	}
	return &eff, nil
}

func withoutScope(scopes []string, drop string) []string {
	out := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		if sc != drop {
			out = append(out, sc)
		}
	}
	return out
}
