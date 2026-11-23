package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

// The changes of a team's block the office's map asks (IMP-233, phase C):
// Edit applies them to the block's YAML tree and writes again only the
// entries they change, so the comments, the blank lines and the fields the
// map does not know stay as the note writes them.
const (
	// OpSet sets a field of a role, or of the team without a role; a null
	// value takes it away.
	OpSet = "set"
	// OpAddRole adds a role, with a profile and an executor: its agent's
	// note is written with the block when it is missing.
	OpAddRole = "add_role"
	// OpRenameRole and OpRemoveRole change a role without open tasks; the
	// delegations of the block follow.
	OpRenameRole = "rename_role"
	OpRemoveRole = "remove_role"
	// OpAddRoom makes a team of the project a room; OpCreateRoom writes a
	// new team, as memory_create_team does, and makes it one; OpRemoveRoom
	// takes a room away, its note stays.
	OpAddRoom    = "add_room"
	OpCreateRoom = "create_room"
	OpRemoveRoom = "remove_room"
)

// maxOps caps the changes of one draft.
const maxOps = 200

// Op is one change of a team's block.
type Op struct {
	Op string `json:"op"`
	// Role is the role a change is about; for set, "" sets a field of the
	// team.
	Role  string          `json:"role,omitempty"`
	Field string          `json:"field,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	// To is a role's new name.
	To string `json:"to,omitempty"`
	// Profile and Run of a role added; Run of a room created too.
	Profile string `json:"profile,omitempty"`
	Run     string `json:"run,omitempty"`
	// Room is a room's name, Path its team's note (add_room).
	Room string `json:"room,omitempty"`
	Path string `json:"path,omitempty"`
	// Coordinator and Roles of a room created: lead and lead, dev when
	// none.
	Coordinator string   `json:"coordinator,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

// EditEnv is what Edit reads outside the block.
type EditEnv struct {
	// Profiles reads the profiles a role added names.
	Profiles Profiles
	// OpenTasks are the open tasks of a role of the team: a role with some
	// is neither renamed nor taken away.
	OpenTasks func(role string) []string
	// Exists says whether a note is in the vault; IsTeam whether it is a
	// note type: team.
	Exists func(path string) bool
	IsTeam func(path string) bool
}

// NewNote is a note a saved draft writes besides the team's: an agent's,
// a room's and its pipelines. IfMissing: only when the vault lacks it.
type NewNote struct {
	Path      string `json:"path"`
	Content   []byte `json:"-"`
	IfMissing bool   `json:"if_missing,omitempty"`
}

// OpError is a change that cannot be made, at its index in the draft.
type OpError struct {
	Index int
	Msg   string
}

func (e *OpError) Error() string { return fmt.Sprintf("change %d: %s", e.Index+1, e.Msg) }

// The kinds of the fields a change sets.
const (
	kindString = iota
	kindInt
	kindNumber
	kindBool
	kindList
)

var roleFieldKinds = map[string]int{
	"profile": kindString, "agent": kindString, "run": kindString, "model": kindString, "effort": kindString,
	"max_turns": kindInt, "max_seconds": kindInt, "max_tokens": kindInt, "access": kindString,
	"repo": kindString, "host": kindString, "paths": kindList, "forbid": kindList, "checks": kindString,
	"preview": kindString, "writes": kindList, "delegates_to": kindList, "approve": kindString,
	"review": kindBool, "guide": kindString, "skills": kindList, "tools": kindList,
}

var teamFieldKinds = map[string]int{
	"coordinator": kindString, "split": kindString, "max_agents": kindInt, "max_rounds": kindInt,
	"reopen_on": kindString, "alert": kindList, "approvers": kindList, "guide": kindString,
	"budget.day_usd": kindNumber, "budget.task_usd": kindNumber,
}

// NewTeamNotes are the notes of a new team named name in project, its
// roles run by run, coordinator among them or "": the team, its tasks'
// database and view, its pipelines, its agents' notes and the starter
// profiles (memory_create_team, a room the map creates).
func NewTeamNotes(project, name, run string, roles []string, coordinator string) []NewNote {
	teamPath := project + "/" + name + ".md"
	notes := []NewNote{
		{Path: teamPath, Content: TeamNote(project, name, run, roles, coordinator)},
		{Path: project + "/" + Database, Content: DatabaseNote(project), IfMissing: true},
		{Path: project + "/" + name + "-tasks.md", Content: ViewNote(project, teamPath)},
		{Path: WorkPath(project, name), Content: WorkPipeline(teamPath)},
		{Path: AlertPath(project, name), Content: AlertPipeline(teamPath)},
	}
	if coordinator != "" {
		notes = append(notes, NewNote{Path: CoordinatePath(project, name), Content: CoordinatePipeline(teamPath)})
	}
	for _, r := range roles {
		notes = append(notes, NewNote{Path: AgentPath(project, r), Content: AgentNote(project, r, r == coordinator), IfMissing: true})
	}
	for _, p := range StarterProfiles(project, run) {
		notes = append(notes, NewNote{Path: p.Path, Content: p.Content, IfMissing: true})
	}
	return notes
}

// TeamNameRe is the name of a team's note, which memory_create_team and a
// room the map creates take.
var TeamNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// EditNote applies ops to the one team block of content, the note at
// notePath: the note with the new block, the block, its first line in the
// note, and the notes a save writes besides it.
func EditNote(content []byte, notePath string, ops []Op, env EditEnv) ([]byte, string, int, []NewNote, error) {
	fenced := parser.FindFenced(content, Lang)
	if len(fenced) != 1 {
		return nil, "", 0, nil, fmt.Errorf("the map changes a note with one ```team block, this one has %d: change it in the editor", len(fenced))
	}
	b := fenced[0]
	specStart := b.Start + bytes.IndexByte(content[b.Start:], '\n') + 1
	src, staged, err := Edit(b.Spec, notePath, ops, env)
	if err != nil {
		return nil, "", 0, nil, err
	}
	out := make([]byte, 0, len(content)+len(src))
	out = append(out, content[:specStart]...)
	out = append(out, src...)
	out = append(out, content[specStart+len(b.Spec):]...)
	return out, src, strings.Count(string(content[:b.Start]), "\n") + 1, staged, nil
}

// Edit applies ops to src, the team block of the note at notePath, and
// returns the new block and the notes a save writes besides the team's.
func Edit(src, notePath string, ops []Op, env EditEnv) (string, []NewNote, error) {
	if len(ops) > maxOps {
		return "", nil, fmt.Errorf("a draft holds at most %d changes: save it, then go on", maxOps)
	}
	crlf := strings.Contains(src, "\r\n")
	if crlf {
		src = strings.ReplaceAll(src, "\r\n", "\n")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return "", nil, errors.New("the block's YAML does not read: change it in the editor")
	}
	var orig *yaml.Node
	if len(doc.Content) == 1 {
		orig = doc.Content[0]
	}
	if orig != nil && orig.Kind != yaml.MappingNode {
		return "", nil, errors.New("the block is no mapping of a team's fields: change it in the editor")
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	origOf := map[*yaml.Node]*yaml.Node{}
	if orig != nil {
		root = cloneTree(orig, origOf)
	}
	e := &editor{root: root, notePath: notePath, project: projectOf(notePath), env: env,
		added: map[*yaml.Node]string{}, rooms: map[string][]NewNote{}}
	for i, op := range ops {
		if err := e.apply(op); err != nil {
			return "", nil, &OpError{Index: i, Msg: err.Error()}
		}
	}
	out := render(src, &doc, root, origOf)
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, e.staged(), nil
}

func projectOf(p string) string {
	project, _, _ := strings.Cut(p, "/")
	return project
}

// editor applies the changes of a draft to the block's tree.
type editor struct {
	root     *yaml.Node
	notePath string
	project  string
	env      EditEnv
	// added are the keys of the roles the draft adds, with the default
	// agent link they were given ("" for none): their agents' notes are
	// written with the block.
	added map[*yaml.Node]string
	// rooms are the notes of the rooms the draft creates, by their path.
	rooms map[string][]NewNote
}

func (e *editor) apply(op Op) error {
	switch op.Op {
	case OpSet:
		if op.Role == "" {
			return e.setTeam(op.Field, op.Value)
		}
		return e.setRole(op.Role, op.Field, op.Value)
	case OpAddRole:
		return e.addRole(op)
	case OpRenameRole:
		return e.renameRole(op.Role, op.To)
	case OpRemoveRole:
		return e.removeRole(op.Role)
	case OpAddRoom:
		return e.addRoom(op.Room, op.Path)
	case OpCreateRoom:
		return e.createRoom(op)
	case OpRemoveRoom:
		return e.removeRoom(op.Room)
	}
	return fmt.Errorf("%q is no change of a team: %s, %s, %s, %s, %s, %s or %s", op.Op,
		OpSet, OpAddRole, OpRenameRole, OpRemoveRole, OpAddRoom, OpCreateRoom, OpRemoveRoom)
}

// get returns the value of key in the mapping m and the index of its key,
// nil and -1 when m has none.
func get(m *yaml.Node, key string) (*yaml.Node, int) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// textNode is a text as a value: a link in double quotes, as the notes
// write them, anything else quoted only when YAML needs it.
func textNode(s string) *yaml.Node {
	n := scalar(s)
	if strings.HasPrefix(s, "[[") {
		n.Style = yaml.DoubleQuotedStyle
		return n
	}
	if err := n.Encode(s); err != nil {
		return scalar(s)
	}
	return n
}

// put sets key of m to v, keeping the comment beside the value it
// replaces; a new key goes before the key named before, or at the end.
func put(m *yaml.Node, key string, v *yaml.Node, before string) {
	if old, i := get(m, key); old != nil {
		if v.LineComment == "" {
			v.LineComment = old.LineComment
		}
		m.Content[i+1] = v
		return
	}
	if _, i := get(m, before); before != "" && i >= 0 {
		m.Content = slices.Insert(m.Content, i, scalar(key), v)
		return
	}
	m.Content = append(m.Content, scalar(key), v)
}

func del(m *yaml.Node, key string) {
	if _, i := get(m, key); i >= 0 {
		m.Content = slices.Delete(m.Content, i, i+2)
	}
}

func (e *editor) roles(create bool) (*yaml.Node, error) {
	m, _ := get(e.root, "roles")
	if m == nil && create {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		put(e.root, "roles", m, "")
	}
	if m != nil && m.Kind != yaml.MappingNode {
		return nil, errors.New("the block's roles are no mapping: change it in the editor")
	}
	return m, nil
}

func (e *editor) role(name string) (*yaml.Node, error) {
	roles, err := e.roles(false)
	if err != nil {
		return nil, err
	}
	r, _ := get(roles, name)
	if r == nil {
		return nil, fmt.Errorf("the team has no role %s", name)
	}
	if r.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("role %s is no mapping of fields: change it in the editor", name)
	}
	return r, nil
}

func (e *editor) roomsNode(create bool) (*yaml.Node, error) {
	m, _ := get(e.root, "rooms")
	if m == nil && create {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		put(e.root, "rooms", m, "roles")
	}
	if m != nil && m.Kind != yaml.MappingNode {
		return nil, errors.New("the block's rooms are no mapping: change it in the editor")
	}
	return m, nil
}

// taken says why name cannot be a new role's or room's.
func (e *editor) taken(name string) error {
	if !roleName.MatchString(name) {
		return fmt.Errorf("%q: lower-case letters, digits and -, starting with a letter, at most 32", name)
	}
	if roles, _ := e.roles(false); roles != nil {
		if r, _ := get(roles, name); r != nil {
			return fmt.Errorf("the team has a role %s already", name)
		}
	}
	if rooms, _ := e.roomsNode(false); rooms != nil {
		if r, _ := get(rooms, name); r != nil {
			return fmt.Errorf("the team has a room %s already", name)
		}
	}
	return nil
}

// value is the YAML of a field's JSON value; nil for null, "", and an
// empty list but tools', which says none.
func value(field string, kind int, raw json.RawMessage) (*yaml.Node, error) {
	var v any
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%s: the value does not read", field)
	}
	if v == nil {
		return nil, nil
	}
	n := &yaml.Node{}
	switch kind {
	case kindString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: a text", field)
		}
		if s = strings.TrimSpace(s); s == "" {
			return nil, nil
		}
		n = textNode(s)
	case kindInt, kindNumber:
		f, ok := v.(float64)
		if !ok || f < 0 || math.IsInf(f, 0) || (kind == kindInt && f != math.Trunc(f)) {
			return nil, fmt.Errorf("%s: a number, 0 or more", field)
		}
		if kind == kindInt {
			if f > math.MaxInt32 {
				return nil, fmt.Errorf("%s: a number, 0 or more", field)
			}
			if err := n.Encode(int(f)); err != nil {
				return nil, err
			}
		} else if err := n.Encode(f); err != nil {
			return nil, err
		}
	case kindBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: true or false", field)
		}
		if err := n.Encode(b); err != nil {
			return nil, err
		}
	case kindList:
		xs, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: a list of texts", field)
		}
		var list []string
		for _, x := range xs {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("%s: a list of texts", field)
			}
			if s = strings.TrimSpace(s); s != "" {
				list = append(list, s)
			}
		}
		if len(list) > maxList {
			return nil, fmt.Errorf("%s: at most %d", field, maxList)
		}
		if len(list) == 0 && field != "tools" {
			return nil, nil
		}
		n = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, s := range list {
			n.Content = append(n.Content, textNode(s))
		}
	}
	return n, nil
}

func (e *editor) setRole(name, field string, raw json.RawMessage) error {
	kind, ok := roleFieldKinds[field]
	if !ok {
		return fmt.Errorf("%q is no field of a role: %s", field, roleFields)
	}
	r, err := e.role(name)
	if err != nil {
		return err
	}
	v, err := value(field, kind, raw)
	if err != nil {
		return err
	}
	if v == nil {
		del(r, field)
		return nil
	}
	put(r, field, v, "")
	return nil
}

func (e *editor) setTeam(field string, raw json.RawMessage) error {
	kind, ok := teamFieldKinds[field]
	if !ok {
		return fmt.Errorf("%q is no field of a team the map changes: coordinator, split, max_agents, max_rounds, reopen_on, alert, approvers, guide, budget.day_usd, budget.task_usd", field)
	}
	v, err := value(field, kind, raw)
	if err != nil {
		return err
	}
	if field == "coordinator" && v != nil {
		if _, err := e.role(v.Value); err != nil {
			return fmt.Errorf("coordinator: %v", err)
		}
	}
	if sub, ok := strings.CutPrefix(field, "budget."); ok {
		b, _ := get(e.root, "budget")
		if b == nil {
			if v == nil {
				return nil
			}
			b = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
			put(e.root, "budget", b, "roles")
		}
		if b.Kind != yaml.MappingNode {
			return errors.New("the block's budget is no mapping: change it in the editor")
		}
		if v == nil {
			del(b, sub)
			if len(b.Content) == 0 {
				del(e.root, "budget")
			}
			return nil
		}
		put(b, sub, v, "")
		return nil
	}
	if v == nil {
		del(e.root, field)
		return nil
	}
	put(e.root, field, v, "roles")
	return nil
}

// profileFieldsOf are the fields the profile p gives, none when it does
// not read.
func (e *editor) profileFieldsOf(p string) (*yaml.Node, error) {
	path, err := ProfilePath(p, e.project)
	if err != nil {
		return nil, err
	}
	if e.env.Profiles == nil {
		return nil, fmt.Errorf("profile %s: the profiles are read by the server", path)
	}
	src, err := e.env.Profiles(path)
	if err != nil {
		return nil, fmt.Errorf("profile %s: %v", path, err)
	}
	fields, problem := readProfile(src)
	if problem != nil {
		return nil, fmt.Errorf("profile %s: %s", path, problem.Msg)
	}
	if fields == nil {
		fields = &yaml.Node{Kind: yaml.MappingNode}
	}
	return fields, nil
}

func (e *editor) addRole(op Op) error {
	name := strings.TrimSpace(op.Role)
	if err := e.taken(name); err != nil {
		return fmt.Errorf("role %v", err)
	}
	roles, err := e.roles(true)
	if err != nil {
		return err
	}
	if len(roles.Content)/2 >= maxRoles {
		return fmt.Errorf("a team has at most %d roles", maxRoles)
	}
	r := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var given *yaml.Node
	if p := strings.TrimSpace(op.Profile); p != "" {
		if given, err = e.profileFieldsOf(p); err != nil {
			return err
		}
		put(r, "profile", textNode(p), "")
	}
	agent := ""
	if given == nil || !hasKey(given, "agent") {
		agent = Link(AgentPath(e.project, name))
		put(r, "agent", textNode(agent), "")
	}
	run := strings.TrimSpace(op.Run)
	if run == "" && (given == nil || !hasKey(given, "run")) {
		// The executor of the team's first role, when the profile says none.
		if len(roles.Content) >= 2 {
			if v, _ := get(roles.Content[1], "run"); v != nil && v.Kind == yaml.ScalarNode {
				run = v.Value
			}
		}
	}
	if run != "" {
		put(r, "run", textNode(run), "")
	}
	key := scalar(name)
	roles.Content = append(roles.Content, key, r)
	e.added[key] = agent
	return nil
}

// openTasks says why a role with open tasks keeps its name.
func (e *editor) openTasks(name, what string) error {
	if e.env.OpenTasks == nil {
		return nil
	}
	for k := range e.added {
		if k.Value == name {
			return nil // added by this draft: no task names it yet
		}
	}
	tasks := e.env.OpenTasks(name)
	if len(tasks) == 0 {
		return nil
	}
	shown := tasks
	if len(shown) > 5 {
		shown = shown[:5]
	}
	more := ""
	if len(tasks) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(tasks)-len(shown))
	}
	return fmt.Errorf("role %s has %d open tasks, %s%s: finish them or give them to another role before you %s it", name, len(tasks), strings.Join(shown, ", "), more, what)
}

// eachDelegation calls fn with each role's delegates_to list.
func (e *editor) eachDelegation(fn func(list *yaml.Node, role *yaml.Node)) {
	roles, _ := e.roles(false)
	if roles == nil {
		return
	}
	for i := 0; i+1 < len(roles.Content); i += 2 {
		r := roles.Content[i+1]
		if d, _ := get(r, "delegates_to"); d != nil && d.Kind == yaml.SequenceNode {
			fn(d, r)
		}
	}
}

// forget takes name out of every role's delegations.
func (e *editor) forget(name string) {
	e.eachDelegation(func(d, r *yaml.Node) {
		d.Content = slices.DeleteFunc(d.Content, func(n *yaml.Node) bool { return strings.TrimSpace(n.Value) == name })
		if len(d.Content) == 0 {
			del(r, "delegates_to")
		}
	})
}

func (e *editor) renameRole(name, to string) error {
	to = strings.TrimSpace(to)
	r, err := e.role(name)
	if err != nil {
		return err
	}
	if to == name {
		return nil
	}
	if err := e.taken(to); err != nil {
		return fmt.Errorf("role %v", err)
	}
	if err := e.openTasks(name, "rename"); err != nil {
		return err
	}
	roles, _ := e.roles(false)
	_, i := get(roles, name)
	key := roles.Content[i]
	key.Value = to
	if agent, ok := e.added[key]; ok && agent != "" {
		// A role this draft added keeps the agent's note of its name.
		if a, _ := get(r, "agent"); a != nil && a.Value == agent {
			a.Value = Link(AgentPath(e.project, to))
			e.added[key] = a.Value
		}
	}
	e.eachDelegation(func(d, _ *yaml.Node) {
		for _, n := range d.Content {
			if strings.TrimSpace(n.Value) == name {
				n.Value = to
			}
		}
	})
	if c, _ := get(e.root, "coordinator"); c != nil && strings.TrimSpace(c.Value) == name {
		c.Value = to
	}
	return nil
}

func (e *editor) removeRole(name string) error {
	if _, err := e.role(name); err != nil {
		return err
	}
	if c, _ := get(e.root, "coordinator"); c != nil && strings.TrimSpace(c.Value) == name {
		return fmt.Errorf("role %s is the coordinator: give the team another one first", name)
	}
	roles, _ := e.roles(false)
	if len(roles.Content) <= 2 {
		return errors.New("a team has at least one role")
	}
	if err := e.openTasks(name, "take away"); err != nil {
		return err
	}
	_, i := get(roles, name)
	delete(e.added, roles.Content[i])
	roles.Content = slices.Delete(roles.Content, i, i+2)
	e.forget(name)
	return nil
}

// roomPath checks the note of a room: a team of the project, not this
// one, nor another room's.
func (e *editor) roomPath(p string) (string, error) {
	path, err := projectNote(p, e.project)
	switch {
	case err != nil:
		return "", err
	case path == "":
		return "", errors.New("the link to its team's note")
	case path == e.notePath:
		return "", errors.New("the team itself")
	}
	if rooms, _ := e.roomsNode(false); rooms != nil {
		for i := 0; i+1 < len(rooms.Content); i += 2 {
			if LinkPath(rooms.Content[i+1].Value, e.project) == path {
				return "", fmt.Errorf("%s is the room %s already", path, rooms.Content[i].Value)
			}
		}
	}
	return path, nil
}

func (e *editor) linkRoom(name, path string) error {
	rooms, err := e.roomsNode(true)
	if err != nil {
		return err
	}
	if len(rooms.Content)/2 >= maxRooms {
		return fmt.Errorf("a team has at most %d rooms", maxRooms)
	}
	rooms.Content = append(rooms.Content, scalar(name), textNode(Link(path)))
	return nil
}

func (e *editor) addRoom(name, p string) error {
	name = strings.TrimSpace(name)
	if err := e.taken(name); err != nil {
		return fmt.Errorf("room %v", err)
	}
	path, err := e.roomPath(p)
	if err != nil {
		return fmt.Errorf("room %s: %v", name, err)
	}
	if _, created := e.rooms[path]; !created {
		if e.env.Exists != nil && !e.env.Exists(path) {
			return fmt.Errorf("room %s: %s is no note of the project", name, path)
		}
		if e.env.IsTeam != nil && !e.env.IsTeam(path) {
			return fmt.Errorf("room %s: %s is no note type: team", name, path)
		}
	}
	return e.linkRoom(name, path)
}

func (e *editor) createRoom(op Op) error {
	name := strings.TrimSpace(op.Room)
	if err := e.taken(name); err != nil {
		return fmt.Errorf("room %v", err)
	}
	if !TeamNameRe.MatchString(name) {
		return fmt.Errorf("room %q: lower-case letters, digits and -, starting with a letter", name)
	}
	path := e.project + "/" + name + ".md"
	if _, err := e.roomPath(path); err != nil {
		return fmt.Errorf("room %s: %v", name, err)
	}
	if _, again := e.rooms[path]; again || (e.env.Exists != nil && e.env.Exists(path)) {
		return fmt.Errorf("room %s: %s exists already: make it a room as it is", name, path)
	}
	coordinator := strings.TrimSpace(op.Coordinator)
	if coordinator == "" {
		coordinator = "lead"
	}
	roles := trimList(op.Roles)
	if len(roles) == 0 {
		roles = []string{coordinator, "dev"}
	}
	if !slices.Contains(roles, coordinator) {
		roles = append([]string{coordinator}, roles...)
	}
	for _, r := range roles {
		if !roleName.MatchString(r) {
			return fmt.Errorf("room %s: role %q: lower-case letters, digits and -, starting with a letter, at most 32", name, r)
		}
	}
	if len(roles) > maxRoles {
		return fmt.Errorf("room %s: at most %d roles", name, maxRoles)
	}
	run := strings.TrimSpace(op.Run)
	if run == "" {
		if rs, _ := e.roles(false); rs != nil && len(rs.Content) >= 2 {
			if v, _ := get(rs.Content[1], "run"); v != nil && v.Kind == yaml.ScalarNode {
				run = v.Value
			}
		}
	}
	if !runName.MatchString(run) {
		return fmt.Errorf("room %s: run %q: the node that runs its roles, a plugin's <name>.run", name, run)
	}
	notes := NewTeamNotes(e.project, name, run, roles, coordinator)
	for _, n := range notes {
		if !n.IfMissing && e.env.Exists != nil && e.env.Exists(n.Path) {
			return fmt.Errorf("room %s: %s exists already", name, n.Path)
		}
	}
	e.rooms[path] = notes
	return e.linkRoom(name, path)
}

func (e *editor) removeRoom(name string) error {
	rooms, err := e.roomsNode(false)
	if err != nil {
		return err
	}
	v, i := get(rooms, name)
	if v == nil {
		return fmt.Errorf("the team has no room %s", name)
	}
	delete(e.rooms, LinkPath(v.Value, e.project))
	rooms.Content = slices.Delete(rooms.Content, i, i+2)
	if len(rooms.Content) == 0 {
		del(e.root, "rooms")
	}
	e.forget(name)
	return nil
}

// staged are the notes a save writes besides the team's: the agents of
// the roles added, the rooms created, each once.
func (e *editor) staged() []NewNote {
	var out []NewNote
	seen := map[string]bool{}
	add := func(n NewNote) {
		if seen[n.Path] || (n.IfMissing && e.env.Exists != nil && e.env.Exists(n.Path)) {
			return
		}
		seen[n.Path] = true
		out = append(out, n)
	}
	for _, p := range mapsKeys(e.rooms) {
		for _, n := range e.rooms[p] {
			add(n)
		}
	}
	roles, _ := e.roles(false)
	for i := 0; roles != nil && i+1 < len(roles.Content); i += 2 {
		k := roles.Content[i]
		agent, ok := e.added[k]
		if !ok || agent == "" {
			continue
		}
		if a, _ := get(roles.Content[i+1], "agent"); a == nil || a.Value != agent {
			continue // the draft gave it another agent
		}
		add(NewNote{Path: AgentPath(e.project, k.Value), Content: AgentNote(e.project, k.Value, false), IfMissing: true})
	}
	return out
}

func mapsKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// cloneTree copies the tree n, each copy's original in origOf: an entry
// keeps its lines through the changes, a role renamed among them.
func cloneTree(n *yaml.Node, origOf map[*yaml.Node]*yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	if n.Content != nil {
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, x := range n.Content {
			c.Content[i] = cloneTree(x, origOf)
		}
	}
	origOf[&c] = n
	return &c
}

// render writes root, the edited mapping of the block src, whose document
// was doc: the lines of src for the entries that did not change, the
// entries that did written again. A text that would not read back as the
// document with root, its comments too, is the document written whole.
func render(src string, doc, root *yaml.Node, origOf map[*yaml.Node]*yaml.Node) string {
	edited := *doc
	if edited.Kind == 0 {
		edited = yaml.Node{Kind: yaml.DocumentNode}
	}
	edited.Content = []*yaml.Node{root}
	if len(doc.Content) == 1 && doc.Content[0].Style&yaml.FlowStyle == 0 {
		lines := strings.SplitAfter(src, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if out, ok := renderMapping(lines, doc.Content[0], root, origOf, 0, len(lines), "", true); ok && readsAs(out, &edited) {
			return out
		}
	}
	if len(root.Content) == 0 {
		return ""
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(&edited)
	_ = enc.Close()
	return b.String()
}

func isBlank(l string) bool { return strings.TrimSpace(l) == "" }

func isComment(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "#") }

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

// spans are where each entry of the mapping m is among lines[from:to]:
// from the blank lines and comments above its key, at its indentation or
// less, to the next entry's; tail is where the lines after the last
// entry's begin, the blank lines and comments of no entry.
func spans(lines []string, m *yaml.Node, from, to int) ([][2]int, int, bool) {
	n := len(m.Content) / 2
	starts := make([]int, n)
	for i := 0; i < n; i++ {
		k := m.Content[2*i]
		l := k.Line - 1
		if l < from || l >= to {
			return nil, 0, false
		}
		s := l
		for s-1 >= from && (isBlank(lines[s-1]) || (isComment(lines[s-1]) && indentOf(lines[s-1]) <= k.Column-1)) {
			if i > 0 && s-1 <= m.Content[2*i-2].Line-1 {
				break
			}
			s--
		}
		starts[i] = s
	}
	tail := to
	if n > 0 {
		last := m.Content[2*n-2]
		for tail-1 > last.Line-1 && (isBlank(lines[tail-1]) || (isComment(lines[tail-1]) && indentOf(lines[tail-1]) <= last.Column-1)) {
			tail--
		}
	}
	out := make([][2]int, n)
	for i := 0; i < n; i++ {
		end := tail
		if i+1 < n {
			end = starts[i+1]
		}
		if end < starts[i] {
			return nil, 0, false
		}
		out[i] = [2]int{starts[i], end}
	}
	return out, tail, true
}

// renderMapping writes the entries of cur, the edited mapping orig is at
// lines[from:to], each key at indent: the roles and rooms of the block,
// when top, one level deeper.
func renderMapping(lines []string, orig, cur *yaml.Node, origOf map[*yaml.Node]*yaml.Node, from, to int, indent string, top bool) (string, bool) {
	sp, tail, ok := spans(lines, orig, from, to)
	if !ok {
		return "", false
	}
	at := map[*yaml.Node]int{}
	for i := 0; i+1 < len(orig.Content); i += 2 {
		at[orig.Content[i]] = i / 2
	}
	var b strings.Builder
	if len(sp) > 0 {
		b.WriteString(strings.Join(lines[from:sp[0][0]], ""))
	} else {
		b.WriteString(strings.Join(lines[from:tail], ""))
	}
	for i := 0; i+1 < len(cur.Content); i += 2 {
		k, v := cur.Content[i], cur.Content[i+1]
		j, was := at[origOf[k]]
		if !was {
			text, err := encodeEntry(k, v, indent, false)
			if err != nil {
				return "", false
			}
			b.WriteString(text)
			continue
		}
		ok0, ov := orig.Content[2*j], orig.Content[2*j+1]
		s := sp[j]
		if sameEntry(ok0, ov, k, v) {
			b.WriteString(strings.Join(lines[s[0]:s[1]], ""))
			continue
		}
		keyLine := ok0.Line - 1
		// The blank lines and comments above the key, as they are.
		b.WriteString(strings.Join(lines[s[0]:keyLine], ""))
		if top && (k.Value == "roles" || k.Value == "rooms") && ov.Kind == yaml.MappingNode && v.Kind == yaml.MappingNode &&
			ov.Style&yaml.FlowStyle == 0 && v.Style&yaml.FlowStyle == 0 && len(ov.Content) > 0 && ov.Content[0].Line > ok0.Line &&
			ok0.LineComment == k.LineComment && ov.LineComment == v.LineComment && ok0.HeadComment == k.HeadComment {
			b.WriteString(lines[keyLine])
			inner := strings.Repeat(" ", ov.Content[0].Column-1)
			text, ok := renderMapping(lines, ov, v, origOf, keyLine+1, s[1], inner, false)
			if !ok {
				return "", false
			}
			b.WriteString(text)
			continue
		}
		text, err := encodeEntry(k, v, indent, true)
		if err != nil {
			return "", false
		}
		b.WriteString(text)
	}
	b.WriteString(strings.Join(lines[tail:to], ""))
	return b.String(), true
}

// sameEntry says whether an entry reads the same, comments too.
func sameEntry(ok, ov, k, v *yaml.Node) bool {
	a, err1 := encodeEntry(ok, ov, "", false)
	b, err2 := encodeEntry(k, v, "", false)
	return err1 == nil && err2 == nil && a == b
}

// encodeEntry writes key: value at indent; kept says the comments above
// the key stay as the lines wrote them, so the key's are not written
// again.
func encodeEntry(k, v *yaml.Node, indent string, kept bool) (string, error) {
	kk := *k
	vv := *v
	if kept {
		kk.HeadComment = ""
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{&kk, &vv}}); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	if indent == "" {
		return b.String(), nil
	}
	var o strings.Builder
	for _, l := range strings.SplitAfter(b.String(), "\n") {
		if strings.TrimSpace(l) != "" {
			o.WriteString(indent)
		}
		o.WriteString(l)
	}
	return o.String(), nil
}

// readsAs says whether text reads back as the document want: the same
// values and the same comments, each as many times.
func readsAs(text string, want *yaml.Node) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return false
	}
	var a, b any
	if len(doc.Content) == 1 && doc.Content[0].Decode(&a) != nil {
		return false
	}
	if len(want.Content) == 1 && len(want.Content[0].Content) > 0 && want.Content[0].Decode(&b) != nil {
		return false
	}
	if !reflect.DeepEqual(a, b) {
		return false
	}
	return slices.Equal(comments(&doc), comments(want))
}

// comments are the lines of the comments of a tree, sorted.
func comments(n *yaml.Node) []string {
	var out []string
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		for _, c := range []string{n.HeadComment, n.LineComment, n.FootComment} {
			for _, l := range strings.Split(c, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					out = append(out, l)
				}
			}
		}
		for _, x := range n.Content {
			walk(x)
		}
	}
	walk(n)
	sort.Strings(out)
	return out
}

// RoleFields are the fields each role of the block src writes itself, by
// role, in their order: a field a role has and does not write is its
// profile's.
func RoleFields(src string) map[string][]string {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(src), &doc) != nil || len(doc.Content) != 1 {
		return nil
	}
	roles, _ := get(doc.Content[0], "roles")
	if roles == nil || roles.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string][]string{}
	for i := 0; i+1 < len(roles.Content); i += 2 {
		fields := []string{}
		if r := roles.Content[i+1]; r.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(r.Content); j += 2 {
				fields = append(fields, r.Content[j].Value)
			}
		}
		out[strings.TrimSpace(roles.Content[i].Value)] = fields
	}
	return out
}
