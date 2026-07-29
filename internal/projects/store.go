// Package projects persists per-project flags (skip git sync, hidden from
// MCP, public/private visibility) in <vault>/.gosidian/projects.json. The store is concurrent-safe and
// reloads transparently when the underlying file's mtime changes, so flags
// written from the CLI or another process become effective without a
// restart (mirrors the pattern of internal/auth.Store).
//
// Default behaviour: a project without an entry yields zero-value Flags
// (false/false), preserving the current "everything in, everything visible"
// invariant for projects that pre-existed this feature.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Flags are the configurable per-project knobs. JSON keys use snake_case
// for human-edited file ergonomics.
type Flags struct {
	SkipGitSync   bool `json:"skip_git_sync,omitempty"`
	HiddenFromMCP bool `json:"hidden_from_mcp,omitempty"`
	// Public marks a project as visible to guest-role users (read-only).
	// Default false = private (only owner/member). "Public" here means visible
	// to all authenticated users including guests — not anonymous/world-readable.
	Public bool `json:"public,omitempty"`
	// UseGlobals opts the project into the shared "global" projects: when set,
	// the project's session bootstrap merges in the global skills/agents
	// (local entries override global ones with the same title). Default false.
	UseGlobals bool `json:"use_globals,omitempty"`
	// UseAnchors opts the project into local agent-anchor materialisation: when
	// set (and the master switch is on), the session bootstrap returns the set
	// of agent anchors to write/reconcile in the agent's cwd for the active CLI
	// profile. Default false. See plan 20260630-agent-anchors.
	UseAnchors bool `json:"use_anchors,omitempty"`
	// UseTagVocabulary opts the project into the per-project lint tag
	// vocabulary declared in <project>/memory/conventions.md frontmatter
	// (`tag_vocabulary:`, exact tags or "ns:*" wildcards). When unset the
	// declaration is inert and memory_lint checks the built-in closed
	// vocabulary only. Default false. See IMP-075.
	UseTagVocabulary bool `json:"use_tag_vocabulary,omitempty"`
}

// Entry is a (name, flags) pair returned by All().
type Entry struct {
	Name string
	Flags
}

// Permission levels for a project membership. Stored verbatim in
// projects.json; the authz layer maps them to access/write decisions.
const (
	LevelRead  = "read"
	LevelWrite = "write"
)

// ProjectMember grants a specific user access to a (private) project at a
// permission level. The role is still the ceiling: a guest with a "write"
// membership stays read-only. Persisted in a separate map from Flags so Flags
// stays a comparable struct (Set relies on `f == Flags{}`).
type ProjectMember struct {
	UserID string `json:"user_id"`
	Level  string `json:"level"` // read | write
}

// Member-scope modes (global). MemberScopeAll is the legacy default: owner and
// member see every project. MemberScopeMembers gates private projects behind
// explicit per-project membership — members and guests then see only the
// projects they belong to, plus public ones.
const (
	MemberScopeAll     = "all"
	MemberScopeMembers = "members"
)

// ValidLevel reports whether s is an accepted membership level.
func ValidLevel(s string) bool { return s == LevelRead || s == LevelWrite }

// Store is a concurrent-safe per-project flags store backed by a JSON file.
// Like auth.Store, it re-reads the file when its mtime changes so out-of-band
// edits become effective without a restart.
type Store struct {
	path        string
	mu          sync.RWMutex
	data        map[string]Flags
	members     map[string][]ProjectMember // project -> members (per-project ACL)
	memberScope string                     // "" / all (legacy) | members
	mtime       time.Time
}

type storeFile struct {
	Projects    map[string]Flags           `json:"projects"`
	Members     map[string][]ProjectMember `json:"members,omitempty"`
	MemberScope string                     `json:"member_scope,omitempty"`
}

// Open loads the store from the given file path. A missing file is not an
// error — it returns an empty store, and the file is created lazily on the
// first Set/Delete/Rename.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: map[string]Flags{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the on-disk path the store reads/writes.
func (s *Store) Path() string { return s.path }

// load replaces the in-memory snapshot with what's on disk. Caller must hold
// s.mu in write mode or be in an initialization context.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.data = map[string]Flags{}
			s.members = map[string][]ProjectMember{}
			s.memberScope = ""
			s.mtime = time.Time{}
			return nil
		}
		return err
	}
	var sf storeFile
	if len(data) > 0 {
		if err := json.Unmarshal(data, &sf); err != nil {
			return fmt.Errorf("parse projects file: %w", err)
		}
	}
	if sf.Projects == nil {
		sf.Projects = map[string]Flags{}
	}
	if sf.Members == nil {
		sf.Members = map[string][]ProjectMember{}
	}
	s.data = sf.Projects
	s.members = sf.Members
	s.memberScope = sf.MemberScope
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// reloadIfStale re-reads the file when its mtime (or existence) diverges from
// the last-loaded snapshot. Caller must hold s.mu in write mode.
func (s *Store) reloadIfStale() {
	st, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !s.mtime.IsZero() || len(s.data) > 0 || len(s.members) > 0 || s.memberScope != "" {
				s.data = map[string]Flags{}
				s.members = map[string][]ProjectMember{}
				s.memberScope = ""
				s.mtime = time.Time{}
			}
		}
		return
	}
	if st.ModTime().Equal(s.mtime) {
		return
	}
	_ = s.load()
}

// save writes the current in-memory snapshot atomically (write+rename, 0o600).
// Caller must hold s.mu in write mode.
func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(storeFile{Projects: s.data, Members: s.members, MemberScope: s.memberScope}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

// Get returns the flags for a project. Unknown projects yield zero-value
// flags (backward-compatible default).
func (s *Store) Get(name string) Flags {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	return s.data[name]
}

// Set persists the flags for a project. If both fields are zero the entry is
// removed instead, keeping projects.json minimal.
func (s *Store) Set(name string, f Flags) error {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid project name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if f == (Flags{}) {
		delete(s.data, name)
	} else {
		s.data[name] = f
	}
	return s.save()
}

// Delete removes any entry for the project. No-op if absent.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	_, hadFlags := s.data[name]
	_, hadMembers := s.members[name]
	if !hadFlags && !hadMembers {
		return nil
	}
	delete(s.data, name)
	delete(s.members, name)
	return s.save()
}

// Rename atomically moves an entry from oldName to newName. No-op if oldName
// has no entry. If newName already has an entry it's overwritten.
func (s *Store) Rename(oldName, newName string) error {
	if newName == "" || strings.ContainsAny(newName, "/\\") {
		return fmt.Errorf("invalid project name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	f, hadFlags := s.data[oldName]
	m, hadMembers := s.members[oldName]
	if !hadFlags && !hadMembers {
		return nil
	}
	if hadFlags {
		delete(s.data, oldName)
		s.data[newName] = f
	}
	if hadMembers {
		delete(s.members, oldName)
		if s.members == nil {
			s.members = map[string][]ProjectMember{}
		}
		s.members[newName] = m
	}
	return s.save()
}

// All returns every entry, sorted by Name. Stable ordering is convenient for
// UI rendering and deterministic tests.
func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := make([]Entry, 0, len(s.data))
	for n, f := range s.data {
		out = append(out, Entry{Name: n, Flags: f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SkipNamesForGit returns the set of project names with SkipGitSync=true,
// sorted. Used by gitsync to render the managed block of .gitignore.
func (s *Store) SkipNamesForGit() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := make([]string, 0)
	for n, f := range s.data {
		if f.SkipGitSync {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// IsPublic reports whether the project is flagged Public=true (visible to
// guests). Unknown projects default to private.
func (s *Store) IsPublic(name string) bool {
	return s.Get(name).Public
}

// UsesGlobals reports whether the project opted into the shared global skills/
// agents. Unknown projects default to false.
func (s *Store) UsesGlobals(name string) bool {
	return s.Get(name).UseGlobals
}

// UsesAnchors reports whether the project opted into local agent-anchor
// materialisation. Unknown projects default to false.
func (s *Store) UsesAnchors(name string) bool {
	return s.Get(name).UseAnchors
}

// UsesTagVocabulary reports whether the project opted into the per-project
// lint tag vocabulary declared in memory/conventions.md (IMP-075). Unknown
// projects default to false.
func (s *Store) UsesTagVocabulary(name string) bool {
	return s.Get(name).UseTagVocabulary
}

// PublicNames returns the set of project names flagged Public=true, sorted.
// Used by the authz layer to compute the guest-visible project set.
func (s *Store) PublicNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	out := make([]string, 0)
	for n, f := range s.data {
		if f.Public {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// MemberLevel returns the membership level a user holds on a project, and
// whether such a membership exists. Used by the authz layer.
func (s *Store) MemberLevel(project, userID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	for _, m := range s.members[project] {
		if m.UserID == userID {
			return m.Level, true
		}
	}
	return "", false
}

// MembersOf returns a copy of the members of a project, sorted by user id.
func (s *Store) MembersOf(project string) []ProjectMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	src := s.members[project]
	out := make([]ProjectMember, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out
}

// SetMember adds or updates a user's membership of a project. level must be
// read or write.
func (s *Store) SetMember(project, userID, level string) error {
	if project == "" || strings.ContainsAny(project, "/\\") {
		return fmt.Errorf("invalid project name")
	}
	if userID == "" {
		return fmt.Errorf("user id required")
	}
	if !ValidLevel(level) {
		return fmt.Errorf("invalid level %q", level)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if s.members == nil {
		s.members = map[string][]ProjectMember{}
	}
	list := s.members[project]
	for i := range list {
		if list[i].UserID == userID {
			list[i].Level = level
			s.members[project] = list
			return s.save()
		}
	}
	s.members[project] = append(list, ProjectMember{UserID: userID, Level: level})
	return s.save()
}

// RemoveMember drops a user's membership of a project. No-op if absent.
func (s *Store) RemoveMember(project, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	list := s.members[project]
	out := list[:0:0]
	for _, m := range list {
		if m.UserID != userID {
			out = append(out, m)
		}
	}
	if len(out) == len(list) {
		return nil // nothing removed
	}
	if len(out) == 0 {
		delete(s.members, project)
	} else {
		s.members[project] = out
	}
	return s.save()
}

// RemoveUserEverywhere strips a user from every project ACL. Called when a user
// is disabled/removed so stale memberships don't linger.
func (s *Store) RemoveUserEverywhere(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	changed := false
	for proj, list := range s.members {
		out := list[:0:0]
		for _, m := range list {
			if m.UserID != userID {
				out = append(out, m)
			} else {
				changed = true
			}
		}
		if len(out) == 0 {
			delete(s.members, proj)
		} else {
			s.members[proj] = out
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

// MemberScope returns the global member-scope mode, defaulting to "all"
// (legacy: owner/member see every project).
func (s *Store) MemberScope() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if s.memberScope == MemberScopeMembers {
		return MemberScopeMembers
	}
	return MemberScopeAll
}

// SetMemberScope sets the global member-scope mode. Unknown values normalize to
// "all".
func (s *Store) SetMemberScope(mode string) error {
	if mode != MemberScopeMembers {
		mode = MemberScopeAll
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	// Store "" for the default to keep projects.json minimal.
	if mode == MemberScopeAll {
		s.memberScope = ""
	} else {
		s.memberScope = mode
	}
	return s.save()
}
