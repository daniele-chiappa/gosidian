// Package authz centralizes gosidian's access decisions so the HTTP API
// (internal/api/v1), the SSE stream and the MCP server (internal/mcp) share
// one source of truth for "who may see/do what". Spreading these checks
// across individual handlers is a security hazard — a single forgotten
// endpoint leaks data — so every project-scoped read and every mutation
// funnels through here.
//
// Model (v2.30, IMP-101 / ADR-026): a principal's level on a project is the
// highest of what the project's visibility gives (read, or nothing) and what
// an explicit grant gives (read, write or admin), capped by the role. The
// owner is admin everywhere; a guest never exceeds read; an unrecognized
// role only ever reads public projects.
package authz

import "github.com/gosidian/gosidian/internal/webauth"

// Principal is the authenticated identity behind a request, distilled to the
// fields that drive authorization. It is built from a SPA session user (HTTP)
// or from the webauth user that owns an MCP token.
type Principal struct {
	UserID string
	Role   webauth.Role
}

// CanWrite reports whether the principal's ROLE allows mutations at all
// (owner and member). It is the ceiling, not the decision: whether a given
// project may be written is CanWriteProject.
func (p Principal) CanWrite() bool { return p.Role.CanWrite() }

// CanAdmin reports whether the principal may perform owner-only administration
// (users, tokens, invites, audit, global settings).
func (p Principal) CanAdmin() bool { return p.Role.CanAdmin() }

// IsGuest reports whether the principal holds the restricted guest role.
func (p Principal) IsGuest() bool { return p.Role.IsGuest() }

// CanSeeAllProjects reports whether the principal holds a role of the member
// tier or above (owner or member). Kept for role-tier gates such as the
// settings page; project visibility itself is decided by Level.
func (p Principal) CanSeeAllProjects() bool {
	return p.Role == webauth.RoleOwner || p.Role == webauth.RoleMember
}

// Level is a principal's effective access to one project, in increasing
// order. Comparisons (>=) are the whole API: CanAccessProject is >= LevelRead.
type Level int

const (
	LevelNone Level = iota
	LevelRead
	LevelWrite
	LevelAdmin
)

// String renders the level with the vocabulary the projects store and the
// API use ("none", "read", "write", "admin").
func (l Level) String() string {
	switch l {
	case LevelRead:
		return "read"
	case LevelWrite:
		return "write"
	case LevelAdmin:
		return "admin"
	}
	return "none"
}

// ParseLevel maps a stored grant level to a Level; unknown values are none.
func ParseLevel(s string) Level {
	switch s {
	case "read":
		return LevelRead
	case "write":
		return LevelWrite
	case "admin":
		return LevelAdmin
	}
	return LevelNone
}

// Visibility values as the projects store spells them. Duplicated here (not
// imported) so this package stays free of storage concerns.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
	VisibilityPrivate  = "private"
)

// GrantSource is one explicit grant the principal holds on a project — a
// direct grant or one inherited from a team — with the label the access
// views show for it ("grant:write", "team:devs:read").
type GrantSource struct {
	Level Level
	Via   string
}

// AccessConfig carries the per-request lookups the predicate needs beyond
// the principal: a project's visibility and the principal's explicit grants.
// Both are resolved by the caller that owns the projects store
// (projects.Store.AccessConfig) so the same config serves the API, the SSE
// stream and the MCP runtime. nil lookups fail closed: everything private,
// no grants.
type AccessConfig struct {
	// Visibility returns public | internal | private for a project.
	Visibility func(project string) string
	// Grants returns every explicit grant userID holds on project, direct or
	// through a team; the highest level wins.
	Grants func(userID, project string) []GrantSource
}

// Explain computes the principal's level on the project and the reasons
// that produced it ("owner", "public", "internal", "grant:<level>",
// "team:<name>:<level>"), for the access views that show a user why they
// see a project.
func (p Principal) Explain(project string, cfg AccessConfig) (Level, []string) {
	if p.Role == webauth.RoleOwner {
		return LevelAdmin, []string{"owner"}
	}
	vis := VisibilityPrivate
	if cfg.Visibility != nil {
		vis = cfg.Visibility(project)
	}
	lvl := LevelNone
	var via []string
	switch vis {
	case VisibilityPublic:
		lvl = LevelRead
		via = append(via, "public")
	case VisibilityInternal:
		if p.Role == webauth.RoleMember {
			lvl = LevelRead
			via = append(via, "internal")
		}
	}
	// Grants apply to the known non-owner roles only: a zero-value or unknown
	// role fails closed and keeps at most the public read above.
	if (p.Role == webauth.RoleMember || p.Role == webauth.RoleGuest) && cfg.Grants != nil && p.UserID != "" {
		for _, g := range cfg.Grants(p.UserID, project) {
			if g.Level <= LevelNone {
				continue
			}
			if g.Level > lvl {
				lvl = g.Level
			}
			via = append(via, g.Via)
		}
	}
	// Role ceiling: guests read at most, whatever the grant says.
	if p.Role == webauth.RoleGuest && lvl > LevelRead {
		lvl = LevelRead
	}
	return lvl, via
}

// Level is Explain without the reasons.
func (p Principal) Level(project string, cfg AccessConfig) Level {
	lvl, _ := p.Explain(project, cfg)
	return lvl
}

// CanAccessProject reports whether the principal may read the named project.
func (p Principal) CanAccessProject(project string, cfg AccessConfig) bool {
	return p.Level(project, cfg) >= LevelRead
}

// CanWriteProject reports whether the principal may create, edit and delete
// notes and attachments in the project.
func (p Principal) CanWriteProject(project string, cfg AccessConfig) bool {
	return p.Level(project, cfg) >= LevelWrite
}

// CanAdminProject reports whether the principal may change the project's
// settings (visibility, flags, rename, delete).
func (p Principal) CanAdminProject(project string, cfg AccessConfig) bool {
	return p.Level(project, cfg) >= LevelAdmin
}
