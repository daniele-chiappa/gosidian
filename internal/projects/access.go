package projects

import "github.com/gosidian/gosidian/internal/authz"

// AccessConfig builds the inputs the shared authz predicate needs from this
// store: whether per-project membership is enforced (member_scope = members),
// the Public flag lookup, and the membership lookups with the level→bool
// mapping resolved here so authz stays free of storage concerns. Every
// surface that answers "may this account read/write project X" (REST, SSE,
// MCP) must build its config through this one method, so a rule added here
// applies everywhere at once.
//
// A nil store yields the legacy behaviour: nothing public, nobody a member,
// enforcement off — owner/member see everything, guests nothing.
func (s *Store) AccessConfig() authz.AccessConfig {
	if s == nil {
		return authz.AccessConfig{}
	}
	return authz.AccessConfig{
		Enforced: s.MemberScope() == MemberScopeMembers,
		IsPublic: s.IsPublic,
		IsMember: func(userID, project string) bool {
			_, ok := s.MemberLevel(project, userID)
			return ok
		},
		MemberCanWrite: func(userID, project string) bool {
			lvl, ok := s.MemberLevel(project, userID)
			return ok && lvl == LevelWrite
		},
	}
}
