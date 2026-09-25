package projects

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Team groups accounts so one grant per project covers all of them (IMP-101
// phase 2). A team's grant on a project is one more source for an account's
// effective level, alongside the direct grant; the highest wins and the role
// stays the ceiling. Teams live in projects.json next to the grants so a
// single store (and a single reload) answers every access question.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Users are account ids. Order is insertion order; callers sort for display.
	Users []string `json:"users,omitempty"`
	// Grants maps a project to the level every user of the team holds on it.
	Grants    map[string]string `json:"grants,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// TeamGrant is a team's grant on one project, resolved for display.
type TeamGrant struct {
	TeamID   string
	TeamName string
	Level    string
}

// maxTeamNameLen bounds a team name; long enough for "backend-contractors",
// short enough for a badge.
const maxTeamNameLen = 64

func newTeamID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a platform emergency; a time-derived id keeps
		// the store usable rather than panicking in a request.
		return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
	}
	return hex.EncodeToString(b[:])
}

// normalizeTeamName trims and validates a team name.
func normalizeTeamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("team name required")
	}
	if len(name) > maxTeamNameLen {
		return "", fmt.Errorf("team name longer than %d characters", maxTeamNameLen)
	}
	if strings.ContainsAny(name, "/\\\n\r\t") {
		return "", fmt.Errorf("team name contains an invalid character")
	}
	return name, nil
}

// cloneTeam deep-copies a team so callers can mutate the result freely.
func cloneTeam(t Team) Team {
	cp := t
	cp.Users = append([]string(nil), t.Users...)
	cp.Grants = make(map[string]string, len(t.Grants))
	for k, v := range t.Grants {
		cp.Grants[k] = v
	}
	return cp
}

// teamNameTakenLocked reports whether another team already uses the name
// (case-insensitive). Caller holds s.mu.
func (s *Store) teamNameTakenLocked(name, exceptID string) bool {
	for id, t := range s.teams {
		if id != exceptID && strings.EqualFold(t.Name, name) {
			return true
		}
	}
	return false
}

// Teams returns every team, sorted by name (copies).
func (s *Store) Teams() []Team {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := make([]Team, 0, len(s.teams))
	for _, t := range s.teams {
		out = append(out, cloneTeam(t))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Team returns a copy of the team with the given id.
func (s *Store) Team(id string) (Team, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	t, ok := s.teams[id]
	if !ok {
		return Team{}, false
	}
	return cloneTeam(t), true
}

// CreateTeam adds an empty team. Names are unique, case-insensitively.
func (s *Store) CreateTeam(name, description string) (Team, error) {
	name, err := normalizeTeamName(name)
	if err != nil {
		return Team{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if s.teamNameTakenLocked(name, "") {
		return Team{}, fmt.Errorf("a team named %q already exists", name)
	}
	if s.teams == nil {
		s.teams = map[string]Team{}
	}
	id := newTeamID()
	for _, taken := s.teams[id]; taken; _, taken = s.teams[id] {
		id = newTeamID()
	}
	t := Team{ID: id, Name: name, Description: strings.TrimSpace(description), Grants: map[string]string{}, CreatedAt: time.Now().UTC()}
	s.teams[id] = t
	if err := s.save(); err != nil {
		return Team{}, err
	}
	return cloneTeam(t), nil
}

// UpdateTeam renames a team and/or changes its description; nil leaves a
// field untouched.
func (s *Store) UpdateTeam(id string, name, description *string) (Team, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	t, ok := s.teams[id]
	if !ok {
		return Team{}, fmt.Errorf("team not found")
	}
	if name != nil {
		n, err := normalizeTeamName(*name)
		if err != nil {
			return Team{}, err
		}
		if s.teamNameTakenLocked(n, id) {
			return Team{}, fmt.Errorf("a team named %q already exists", n)
		}
		t.Name = n
	}
	if description != nil {
		t.Description = strings.TrimSpace(*description)
	}
	s.teams[id] = t
	if err := s.save(); err != nil {
		return Team{}, err
	}
	return cloneTeam(t), nil
}

// DeleteTeam removes a team and, with it, every grant it carried.
func (s *Store) DeleteTeam(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if _, ok := s.teams[id]; !ok {
		return fmt.Errorf("team not found")
	}
	delete(s.teams, id)
	return s.save()
}

// AddTeamUser adds an account to a team (no-op if already in).
func (s *Store) AddTeamUser(id, userID string) error {
	if userID == "" {
		return fmt.Errorf("user id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	t, ok := s.teams[id]
	if !ok {
		return fmt.Errorf("team not found")
	}
	for _, u := range t.Users {
		if u == userID {
			return nil
		}
	}
	t.Users = append(t.Users, userID)
	s.teams[id] = t
	return s.save()
}

// RemoveTeamUser drops an account from a team (no-op if absent).
func (s *Store) RemoveTeamUser(id, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	t, ok := s.teams[id]
	if !ok {
		return fmt.Errorf("team not found")
	}
	kept := t.Users[:0:0]
	for _, u := range t.Users {
		if u != userID {
			kept = append(kept, u)
		}
	}
	if len(kept) == len(t.Users) {
		return nil
	}
	t.Users = kept
	s.teams[id] = t
	return s.save()
}

// SetTeamGrant gives every user of the team the level on the project.
func (s *Store) SetTeamGrant(id, project, level string) error {
	if project == "" || strings.ContainsAny(project, "/\\") {
		return fmt.Errorf("invalid project name")
	}
	if !ValidLevel(level) {
		return fmt.Errorf("invalid level %q", level)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	t, ok := s.teams[id]
	if !ok {
		return fmt.Errorf("team not found")
	}
	if t.Grants == nil {
		t.Grants = map[string]string{}
	}
	t.Grants[project] = level
	s.teams[id] = t
	return s.save()
}

// RemoveTeamGrant drops the team's grant on the project (no-op if absent).
func (s *Store) RemoveTeamGrant(id, project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	t, ok := s.teams[id]
	if !ok {
		return fmt.Errorf("team not found")
	}
	if _, has := t.Grants[project]; !has {
		return nil
	}
	delete(t.Grants, project)
	s.teams[id] = t
	return s.save()
}

// TeamGrantsOn lists the teams holding a grant on the project, by name.
func (s *Store) TeamGrantsOn(project string) []TeamGrant {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	var out []TeamGrant
	for _, t := range s.teams {
		if lvl, ok := t.Grants[project]; ok {
			out = append(out, TeamGrant{TeamID: t.ID, TeamName: t.Name, Level: lvl})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].TeamName) < strings.ToLower(out[j].TeamName) })
	return out
}

// TeamsCount returns how many teams hold a grant on the project.
func (s *Store) TeamsCount(project string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	n := 0
	for _, t := range s.teams {
		if _, ok := t.Grants[project]; ok {
			n++
		}
	}
	return n
}

// TeamLevelsFor lists the grants the account inherits on the project from
// the teams it belongs to, by team name. Used by the authz layer.
func (s *Store) TeamLevelsFor(userID, project string) []TeamGrant {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	return s.teamLevelsForLocked(userID, project)
}

func (s *Store) teamLevelsForLocked(userID, project string) []TeamGrant {
	var out []TeamGrant
	for _, t := range s.teams {
		lvl, ok := t.Grants[project]
		if !ok {
			continue
		}
		for _, u := range t.Users {
			if u == userID {
				out = append(out, TeamGrant{TeamID: t.ID, TeamName: t.Name, Level: lvl})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].TeamName) < strings.ToLower(out[j].TeamName) })
	return out
}

// TeamsOf lists the teams the account belongs to, by name.
func (s *Store) TeamsOf(userID string) []Team {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	var out []Team
	for _, t := range s.teams {
		for _, u := range t.Users {
			if u == userID {
				out = append(out, cloneTeam(t))
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
