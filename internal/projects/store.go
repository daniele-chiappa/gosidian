// Package projects persists per-project settings — visibility, the per-user
// access grants, skip git sync, hidden from MCP, opt-in features — in
// <state-dir>/projects.json. The store is concurrent-safe and reloads
// transparently when the underlying file's mtime changes, so edits from the
// CLI or another process become effective without a restart (mirrors the
// pattern of internal/auth.Store).
//
// Access model (v2.30, IMP-101 / ADR-026): a project's visibility says who
// may READ it — public (every signed-in account, guests included), internal
// (every non-guest account) or private (only accounts holding a grant). WRITE
// and ADMIN always come from a grant, never from visibility; the account's
// role is the ceiling (guests never write, the owner is always admin). A
// project without an entry takes the store's default visibility.
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

// Visibility values. See the package doc for what each one grants.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
	VisibilityPrivate  = "private"
)

// ValidVisibility reports whether s is an accepted visibility value.
func ValidVisibility(s string) bool {
	return s == VisibilityPublic || s == VisibilityInternal || s == VisibilityPrivate
}

// Flags are the configurable per-project knobs. JSON keys use snake_case
// for human-edited file ergonomics.
type Flags struct {
	SkipGitSync   bool `json:"skip_git_sync,omitempty"`
	HiddenFromMCP bool `json:"hidden_from_mcp,omitempty"`
	// Visibility is who may read the project: public | internal | private.
	// Empty means "not set": Store.Visibility resolves it from the legacy
	// Public flag, then from the store default.
	Visibility string `json:"visibility,omitempty"`
	// Public is the pre-v2.30 visibility flag (true = readable by guests).
	// Read by the one-time migration and by Store.Visibility as a fallback,
	// never written any more; the API still accepts it as an alias.
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

// Grant levels, in increasing order. Stored verbatim in projects.json; the
// authz layer maps them onto its ordered Level type.
//   - read:  may read the project even when its visibility would not allow it
//   - write: may also create, edit and delete notes and attachments
//   - admin: may also change the project's settings (visibility, flags,
//     rename, delete) and, from phase 2, manage its grants
const (
	LevelRead  = "read"
	LevelWrite = "write"
	LevelAdmin = "admin"
)

// ValidLevel reports whether s is an accepted grant level.
func ValidLevel(s string) bool { return s == LevelRead || s == LevelWrite || s == LevelAdmin }

// LevelRank orders the grant levels (unknown → 0) so callers can compare
// them without string switches.
func LevelRank(s string) int {
	switch s {
	case LevelRead:
		return 1
	case LevelWrite:
		return 2
	case LevelAdmin:
		return 3
	}
	return 0
}

// ProjectMember is a per-user grant on a project. The account's role stays
// the ceiling: a guest with a write grant is still read-only. Persisted in a
// separate map from Flags so Flags stays a comparable struct (Set relies on
// `f == Flags{}`). The JSON key is still "members" for file compatibility.
type ProjectMember struct {
	UserID string `json:"user_id"`
	Level  string `json:"level"` // read | write | admin
}

// MemberScopeMembers is the pre-v2.30 global switch value that gated private
// projects behind memberships. Only read by MigrateAccessModel.
const MemberScopeMembers = "members"

// accessModelVersion marks a projects.json already migrated to the
// visibility + grants model. Bump it only with a new migration.
const accessModelVersion = 2

// Store is a concurrent-safe per-project settings store backed by a JSON
// file. Like auth.Store, it re-reads the file when its mtime changes so
// out-of-band edits become effective without a restart.
type Store struct {
	path              string
	mu                sync.RWMutex
	data              map[string]Flags
	members           map[string][]ProjectMember // project -> grants
	teams             map[string]Team            // team id -> team (IMP-101 phase 2)
	memberScope       string                     // legacy switch, consumed by the migration
	defaultVisibility string                     // visibility of projects without an entry; "" = private
	personalOff       bool                       // personal projects for new accounts switched off (IMP-101 phase 3)
	accessModel       int                        // 0 = pre-v2.30 file, accessModelVersion = migrated
	mtime             time.Time
}

type storeFile struct {
	Projects          map[string]Flags           `json:"projects"`
	Members           map[string][]ProjectMember `json:"members,omitempty"`
	Teams             map[string]Team            `json:"teams,omitempty"`
	MemberScope       string                     `json:"member_scope,omitempty"`
	DefaultVisibility string                     `json:"default_visibility,omitempty"`
	PersonalOff       bool                       `json:"personal_projects_off,omitempty"`
	AccessModel       int                        `json:"access_model,omitempty"`
}

// Open loads the store from the given file path. A missing file is not an
// error — it returns an empty store, and the file is created lazily on the
// first write.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: map[string]Flags{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the on-disk path the store reads/writes.
func (s *Store) Path() string { return s.path }

// reset returns the in-memory snapshot to the empty state. Caller holds s.mu.
func (s *Store) reset() {
	s.data = map[string]Flags{}
	s.members = map[string][]ProjectMember{}
	s.teams = map[string]Team{}
	s.memberScope = ""
	s.defaultVisibility = ""
	s.personalOff = false
	s.accessModel = 0
	s.mtime = time.Time{}
}

// load replaces the in-memory snapshot with what's on disk. Caller must hold
// s.mu in write mode or be in an initialization context.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.reset()
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
	if sf.Teams == nil {
		sf.Teams = map[string]Team{}
	}
	s.data = sf.Projects
	s.members = sf.Members
	s.teams = sf.Teams
	s.memberScope = sf.MemberScope
	s.defaultVisibility = sf.DefaultVisibility
	s.personalOff = sf.PersonalOff
	s.accessModel = sf.AccessModel
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
			if !s.mtime.IsZero() || len(s.data) > 0 || len(s.members) > 0 || len(s.teams) > 0 || s.memberScope != "" || s.defaultVisibility != "" || s.accessModel != 0 {
				s.reset()
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
	data, err := json.MarshalIndent(storeFile{
		Projects:          s.data,
		Members:           s.members,
		Teams:             s.teams,
		MemberScope:       s.memberScope,
		DefaultVisibility: s.defaultVisibility,
		PersonalOff:       s.personalOff,
		AccessModel:       s.accessModel,
	}, "", "  ")
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

// Set persists the flags for a project. If every field is zero the entry is
// removed instead, keeping projects.json minimal.
func (s *Store) Set(name string, f Flags) error {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid project name")
	}
	if f.Visibility != "" && !ValidVisibility(f.Visibility) {
		return fmt.Errorf("invalid visibility %q", f.Visibility)
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
	changed := hadFlags || hadMembers
	delete(s.data, name)
	delete(s.members, name)
	for id, t := range s.teams {
		if _, ok := t.Grants[name]; ok {
			delete(t.Grants, name)
			s.teams[id] = t
			changed = true
		}
	}
	if !changed {
		return nil
	}
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
	changed := hadFlags || hadMembers
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
	for id, t := range s.teams {
		if lvl, ok := t.Grants[oldName]; ok {
			delete(t.Grants, oldName)
			t.Grants[newName] = lvl
			s.teams[id] = t
			changed = true
		}
	}
	if !changed {
		return nil
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

// Visibility resolves who may read the project: the explicit value, else
// the legacy Public flag, else the store default. Never empty.
func (s *Store) Visibility(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	return s.visibilityLocked(name)
}

// visibilityLocked is Visibility for callers already holding s.mu.
func (s *Store) visibilityLocked(name string) string {
	f := s.data[name]
	switch {
	case f.Visibility != "":
		return f.Visibility
	case f.Public:
		return VisibilityPublic
	}
	return s.defaultVisibilityLocked()
}

// IsPublic reports whether the project is readable by every signed-in
// account, guests included.
func (s *Store) IsPublic(name string) bool {
	return s.Visibility(name) == VisibilityPublic
}

// DefaultVisibility is the visibility applied to projects without an entry
// (folders that appeared on disk, projects created before this store knew
// them). Private unless the migration or the owner chose otherwise.
func (s *Store) DefaultVisibility() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	return s.defaultVisibilityLocked()
}

func (s *Store) defaultVisibilityLocked() string {
	if ValidVisibility(s.defaultVisibility) {
		return s.defaultVisibility
	}
	return VisibilityPrivate
}

// SetDefaultVisibility changes the default for projects without an entry.
func (s *Store) SetDefaultVisibility(v string) error {
	if !ValidVisibility(v) {
		return fmt.Errorf("invalid visibility %q", v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	// Private is the zero value: keep the file minimal.
	if v == VisibilityPrivate {
		s.defaultVisibility = ""
	} else {
		s.defaultVisibility = v
	}
	return s.save()
}

// PersonalProjectsEnabled reports whether a new account gets a private
// project named after it (default on).
func (s *Store) PersonalProjectsEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	return !s.personalOff
}

// SetPersonalProjects switches the provisioning of personal projects for new
// accounts on or off.
func (s *Store) SetPersonalProjects(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if s.personalOff == !on {
		return nil
	}
	s.personalOff = !on
	return s.save()
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

// MemberLevel returns the grant level a user holds on a project, and whether
// such a grant exists. Used by the authz layer.
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

// MembersOf returns a copy of the grants on a project, sorted by user id.
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

// MembersCount returns how many accounts hold a grant on the project.
func (s *Store) MembersCount(project string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	return len(s.members[project])
}

// SetMember adds or updates a user's grant on a project. level must be read,
// write or admin.
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
	s.setMemberLocked(project, userID, level)
	return s.save()
}

// setMemberLocked upserts a grant in memory. Caller holds s.mu and saves.
func (s *Store) setMemberLocked(project, userID, level string) {
	if s.members == nil {
		s.members = map[string][]ProjectMember{}
	}
	list := s.members[project]
	for i := range list {
		if list[i].UserID == userID {
			list[i].Level = level
			s.members[project] = list
			return
		}
	}
	s.members[project] = append(list, ProjectMember{UserID: userID, Level: level})
}

// RemoveMember drops a user's grant on a project. No-op if absent.
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

// RemoveUserEverywhere strips a user from every project's grants and from
// every team. Called when a user is disabled/removed so stale grants don't
// linger.
func (s *Store) RemoveUserEverywhere(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	changed := false
	for id, t := range s.teams {
		kept := t.Users[:0:0]
		for _, u := range t.Users {
			if u != userID {
				kept = append(kept, u)
			}
		}
		if len(kept) != len(t.Users) {
			t.Users = kept
			s.teams[id] = t
			changed = true
		}
	}
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

// MigrationReport says what MigrateAccessModel did, for the boot log.
type MigrationReport struct {
	// Applied is false when the file was already on the current model.
	Applied bool
	// LegacyMembersMode is true when the file carried member_scope=members.
	LegacyMembersMode bool
	// Projects is the number of entries given an explicit visibility.
	Projects int
	// GrantsSeeded is the number of write grants created so accounts that
	// could write everywhere keep that access on the existing projects.
	GrantsSeeded int
	// DefaultVisibility is the default chosen for projects created later.
	DefaultVisibility string
}

// MigrateAccessModel converts a pre-v2.30 file (Public flag + global
// member_scope + memberships) to the visibility + grants model, once. It is
// idempotent: a file already on the current model is left untouched.
//
// vaultProjects are the project folders on disk (they may have no entry
// yet); seedUsers are the enabled accounts that were neither owner nor guest
// — under the legacy default (member_scope=all) they could read and write
// every project, so each of them receives a write grant on every existing
// project, which preserves their access exactly. Existing memberships keep
// their level.
//
// Visibility: Public → public; otherwise private under member_scope=members
// (memberships already gated access) and internal under the legacy default
// (every member could read). A fresh installation (no file yet, no account
// besides the owner) gets private everywhere; the default for projects
// created afterwards is private there and internal on an upgraded one, so
// upgrading changes nothing for existing accounts except that writing a NEW
// project now takes a grant.
func (s *Store) MigrateAccessModel(vaultProjects []string, seedUsers []string) (MigrationReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfStale()
	if s.accessModel >= accessModelVersion {
		return MigrationReport{Applied: false, DefaultVisibility: s.defaultVisibilityLocked()}, nil
	}
	rep := MigrationReport{Applied: true, LegacyMembersMode: s.memberScope == MemberScopeMembers}
	// A file that never existed and no account besides the owner: nothing to
	// preserve, so the default-deny model applies from the start.
	fresh := s.mtime.IsZero() && len(seedUsers) == 0

	names := map[string]bool{}
	for _, n := range vaultProjects {
		if n != "" {
			names[n] = true
		}
	}
	for n := range s.data {
		names[n] = true
	}
	for n := range s.members {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, n := range sorted {
		f := s.data[n]
		if f.Visibility == "" {
			switch {
			case f.Public:
				f.Visibility = VisibilityPublic
			case rep.LegacyMembersMode, fresh:
				f.Visibility = VisibilityPrivate
			default:
				f.Visibility = VisibilityInternal
			}
		}
		f.Public = false
		s.data[n] = f
		rep.Projects++
	}
	if !rep.LegacyMembersMode {
		for _, u := range seedUsers {
			if u == "" {
				continue
			}
			for _, n := range sorted {
				if _, ok := s.memberLevelLocked(n, u); ok {
					continue
				}
				s.setMemberLocked(n, u, LevelWrite)
				rep.GrantsSeeded++
			}
		}
	}
	if rep.LegacyMembersMode || fresh {
		s.defaultVisibility = "" // private: memberships already gated access, or nothing to preserve
	} else {
		s.defaultVisibility = VisibilityInternal // upgraded installation: members keep reading new projects
	}
	rep.DefaultVisibility = s.defaultVisibilityLocked()
	s.memberScope = ""
	s.accessModel = accessModelVersion
	if err := s.save(); err != nil {
		return rep, err
	}
	return rep, nil
}

// memberLevelLocked is MemberLevel for callers already holding s.mu.
func (s *Store) memberLevelLocked(project, userID string) (string, bool) {
	for _, m := range s.members[project] {
		if m.UserID == userID {
			return m.Level, true
		}
	}
	return "", false
}
