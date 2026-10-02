package projects

import (
	"fmt"
	"maps"
	"strings"

	"github.com/gosidian/gosidian/internal/authz"
)

// Access is everything the store holds for one project: its flags, with the
// visibility resolved (a restore must not fall back to whatever the default
// is by then), its member grants and its team grants (team id -> level).
// Deleting a project saves it with the trashed folder, and restoring the
// folder puts it back, so the trash undoes a delete without exposing a
// private project under the default visibility (IMP-124).
type Access struct {
	Flags      Flags             `json:"flags"`
	Members    []ProjectMember   `json:"members,omitempty"`
	TeamGrants map[string]string `json:"team_grants,omitempty"`
}

// Access returns the project's entry as an Access. The legacy Public flag is
// folded into the resolved visibility.
func (s *Store) Access(name string) Access {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	a := Access{Flags: s.data[name]}
	a.Flags.Visibility = s.visibilityLocked(name)
	a.Flags.Public = false
	a.Members = append([]ProjectMember(nil), s.members[name]...)
	for id, t := range s.teams {
		if lvl, ok := t.Grants[name]; ok {
			if a.TeamGrants == nil {
				a.TeamGrants = map[string]string{}
			}
			a.TeamGrants[id] = lvl
		}
	}
	return a
}

// SetAccess replaces the project's entry with a: whatever the name held
// before (an entry left by a project that is gone) is dropped, not merged.
// Grants of teams that no longer exist are skipped. If the save fails the
// maps are put back.
func (s *Store) SetAccess(name string, a Access) error {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid project name")
	}
	if a.Flags.Visibility != "" && !ValidVisibility(a.Flags.Visibility) {
		return fmt.Errorf("invalid visibility %q", a.Flags.Visibility)
	}
	for _, m := range a.Members {
		if m.UserID == "" || !ValidLevel(m.Level) {
			return fmt.Errorf("invalid member grant %q:%q", m.UserID, m.Level)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	restore := s.snapshotLocked(name)
	if a.Flags == (Flags{}) {
		delete(s.data, name)
	} else {
		s.data[name] = a.Flags
	}
	if len(a.Members) == 0 {
		delete(s.members, name)
	} else {
		if s.members == nil {
			s.members = map[string][]ProjectMember{}
		}
		s.members[name] = append([]ProjectMember(nil), a.Members...)
	}
	for id, t := range s.teams {
		lvl, granted := a.TeamGrants[id]
		_, had := t.Grants[name]
		if !had && !granted {
			continue
		}
		t.Grants = maps.Clone(t.Grants)
		delete(t.Grants, name)
		if granted && ValidLevel(lvl) {
			if t.Grants == nil {
				t.Grants = map[string]string{}
			}
			t.Grants[name] = lvl
		}
		s.teams[id] = t
	}
	if err := s.save(); err != nil {
		restore()
		return err
	}
	return nil
}

// AccessConfigWith is AccessConfig with project name answered from a
// instead of the store: who could read or administer a project that now
// sits in the trash. Team membership still comes from the store.
func (s *Store) AccessConfigWith(name string, a Access) authz.AccessConfig {
	live := s.AccessConfig()
	vis := a.Flags.Visibility
	if vis == "" {
		vis = VisibilityPrivate
	}
	return authz.AccessConfig{
		Visibility: func(project string) string {
			if project == name {
				return vis
			}
			return live.Visibility(project)
		},
		Grants: func(userID, project string) []authz.GrantSource {
			if project != name {
				return live.Grants(userID, project)
			}
			var out []authz.GrantSource
			for _, m := range a.Members {
				if m.UserID == userID {
					out = append(out, authz.GrantSource{Level: authz.ParseLevel(m.Level), Via: "grant:" + m.Level})
				}
			}
			for id, lvl := range a.TeamGrants {
				if s == nil {
					break
				}
				t, ok := s.Team(id)
				if !ok {
					continue
				}
				for _, u := range t.Users {
					if u == userID {
						out = append(out, authz.GrantSource{Level: authz.ParseLevel(lvl), Via: "team:" + t.Name + ":" + lvl})
						break
					}
				}
			}
			return out
		},
	}
}
