package projects

import "github.com/gosidian/gosidian/internal/authz"

// AccessConfig builds the inputs the shared authz predicate needs from this
// store: the visibility lookup and the grant sources, with the stored level
// strings mapped onto authz.Level here so authz stays free of storage
// concerns. Every surface that answers "may this account read/write/admin
// project X" (REST, SSE, MCP) must build its config through this one
// method, so a rule added here applies everywhere at once.
//
// Grant sources are the account's direct grant ("grant:<level>") and one
// entry per team it belongs to that holds a grant on the project
// ("team:<name>:<level>"); the predicate takes the highest.
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
		Grants: func(userID, project string) []authz.GrantSource {
			var out []authz.GrantSource
			if lvl, ok := s.MemberLevel(project, userID); ok {
				out = append(out, authz.GrantSource{Level: authz.ParseLevel(lvl), Via: "grant:" + lvl})
			}
			for _, tg := range s.TeamLevelsFor(userID, project) {
				out = append(out, authz.GrantSource{Level: authz.ParseLevel(tg.Level), Via: "team:" + tg.TeamName + ":" + tg.Level})
			}
			return out
		},
	}
}
