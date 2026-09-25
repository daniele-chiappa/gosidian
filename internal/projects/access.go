package projects

import "github.com/gosidian/gosidian/internal/authz"

// AccessConfig builds the inputs the shared authz predicate needs from this
// store: the visibility lookup and the grant lookup, with the stored level
// strings mapped onto authz.Level here so authz stays free of storage
// concerns. Every surface that answers "may this account read/write/admin
// project X" (REST, SSE, MCP) must build its config through this one
// method, so a rule added here applies everywhere at once.
//
// A nil store means "no projects store wired" (tests, minimal setups): every
// project counts as internal and nobody holds a grant, so member-tier
// accounts read everything, guests see nothing, and only the owner writes.
func (s *Store) AccessConfig() authz.AccessConfig {
	if s == nil {
		return authz.AccessConfig{
			Visibility: func(string) string { return VisibilityInternal },
		}
	}
	return authz.AccessConfig{
		Visibility: s.Visibility,
		GrantLevel: func(userID, project string) authz.Level {
			lvl, ok := s.MemberLevel(project, userID)
			if !ok {
				return authz.LevelNone
			}
			return authz.ParseLevel(lvl)
		},
	}
}
