// Package fleet reads the teams of agents and their tasks (IMP-195).
//
// A team is a note `type: team` with one ```team block of YAML: its roles,
// each with the note of its agent, the node that runs it (claude.run,
// pi.run), its model and limits, the repo or host it works on, the paths it
// may touch, the folders of the vault it may write and the approval its
// work needs; the coordinator, the roles it may pass work to, the budget of
// a day and of a task, and the agents at work at once.
//
// A task is a note of the project's folder tasks/, a row of the database
// tasks.md: what to do, the team and the role it is for, the objective it
// came from, the tasks it waits for, and what the server keeps on it (who
// holds it, until when, its attempts and cost). The server takes and
// releases tasks; this package only reads and writes the notes.
//
// A board (board.go, IMP-216) shows a project's tasks as cards in columns,
// with limits of the cards at work; a card needs no team.
package fleet

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

// Lang is the fence of a team's block; TeamType and TaskType the types of
// their notes.
const (
	Lang     = "team"
	TeamType = "team"
	TaskType = "task"
	// SkillType is the type of the notes a role's skills name.
	SkillType = "skill"
	// ProfileType is the type of a profile's note, ProfileLang the fence
	// of its block, ProfileFolder where a profile named by its name is
	// (IMP-233).
	ProfileType   = "profile"
	ProfileLang   = "profile"
	ProfileFolder = "profiles"
)

// The approvals a role's work may need.
const (
	// ApproveMerge: a person approves before its pull request is merged.
	ApproveMerge = "merge"
	// ApproveOps: a person approves before a command runs on a host.
	ApproveOps = "ops"
	// ApproveNone: its work closes the task without a person.
	ApproveNone = "none"
)

// The access levels of a role's agent (agent-run/1's access): read,
// edit (also writes files), exec (also runs commands).
const (
	AccessRead = "read"
	AccessEdit = "edit"
	AccessExec = "exec"
)

// The ways a coordinator splits the work.
const (
	// SplitContext gives each task a whole piece of context and phase.
	SplitContext = "context"
	// SplitCraft passes the work along a chain of crafts.
	SplitCraft = "craft"
)

// Limits of a team's block.
const (
	maxRoles      = 20
	maxList       = 50
	maxRoundsCap  = 50
	defaultRounds = 5
	maxSkills     = 20
	maxRooms      = 10
)

// Team is a team as its note declares it.
type Team struct {
	// Path is the team's note.
	Path string `json:"path"`
	// Coordinator is the role that receives the objectives and splits
	// them; "" for a team whose tasks are written by hand.
	Coordinator string `json:"coordinator,omitempty"`
	Split       string `json:"split"`
	Budget      Budget `json:"budget"`
	MaxAgents   int    `json:"max_agents,omitempty"`
	MaxRounds   int    `json:"max_rounds"`
	// ReopenOn is which problems of a review reopen the work: ReopenSerious
	// (the default: the minor ones go to the objective's summary) or
	// ReopenAny.
	ReopenOn  string   `json:"reopen_on"`
	Alert     []string `json:"alert,omitempty"`
	Approvers []string `json:"approvers,omitempty"`
	// Guide is a note of rules every role of the team follows, added to
	// its agent's system prompt (IMP-233); "" for none.
	Guide string `json:"guide,omitempty"`
	// Rooms are the teams of the project this one gives objectives to,
	// by the name its roles delegate to (IMP-233): each has its own
	// coordinator, budget and limits, and spends of this team's budget
	// too. A room has no rooms.
	Rooms []Room `json:"rooms,omitempty"`
	// Roles in the order the block writes them.
	Roles []Role `json:"roles"`
}

// Room is a team another gives objectives to: its name in the other's
// delegates_to, and its note.
type Room struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// line is its key's in the block.
	line int
}

// Room returns the room named name.
func (t Team) Room(name string) (Room, bool) {
	for _, r := range t.Rooms {
		if r.Name == name {
			return r, true
		}
	}
	return Room{}, false
}

// RoomOf returns the room whose note is path.
func (t Team) RoomOf(path string) (Room, bool) {
	for _, r := range t.Rooms {
		if r.Path == path {
			return r, true
		}
	}
	return Room{}, false
}

// Budget is what a team may spend, in dollars as the executors count
// them: in a day (local time) and on one task when the task says none.
// Zero is no limit.
type Budget struct {
	DayUSD  float64 `json:"day_usd,omitempty"`
	TaskUSD float64 `json:"task_usd,omitempty"`
}

// Role is one role of a team.
type Role struct {
	Name string `json:"name"`
	// Agent is the note of its agent, whose body is its system prompt; ""
	// for none.
	Agent string `json:"agent,omitempty"`
	// Run is the node that runs it, a plugin's <name>.run.
	Run        string `json:"run"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	MaxTurns   int    `json:"max_turns,omitempty"`
	MaxSeconds int    `json:"max_seconds,omitempty"`
	// MaxTokens is the tokens of a step, cache reads too, within the
	// executor's preset; 0 for its default.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Access is its agent's level, AccessRead, AccessEdit or AccessExec:
	// with exec it runs commands, its checks among them, in its
	// executor's sandbox. "" for the executor's default, which is edit for
	// a role with a repo; read with a repo, the agent reads its files and
	// changes none (Commits).
	Access string `json:"access,omitempty"`
	// Repo is the repository it works on, a name of the executor's preset;
	// Host the host its commands run on.
	Repo string `json:"repo,omitempty"`
	Host string `json:"host,omitempty"`
	// Paths and Forbid are the paths of the repository it may and may not
	// touch: a folder ends with /, anything else is a file or a glob.
	Paths  []string `json:"paths,omitempty"`
	Forbid []string `json:"forbid,omitempty"`
	// Checks is the script that tests its work (exec.run), in the
	// repository's folder.
	Checks string `json:"checks,omitempty"`
	// Preview is the folder of the build Checks makes, published and
	// looked at before a person approves (preview.publish).
	Preview string `json:"preview,omitempty"`
	// Writes are the folders of the vault its MCP token may write, each
	// ending with /.
	Writes []string `json:"writes,omitempty"`
	// DelegatesTo are the roles it may give tasks to.
	DelegatesTo []string `json:"delegates_to,omitempty"`
	// Approve is ApproveMerge, ApproveOps or ApproveNone; Parse fills it
	// from Repo and Host when the block says none.
	Approve string `json:"approve"`
	// Review: the role judges the work of others. Its report ends with a
	// verdict and its problems, each serious or minor, which the gate
	// counts (ReviewOf).
	Review bool `json:"review,omitempty"`
	// line is its key's in the block.
	line int
	// Guide is a note of rules of the role, added to its agent's system
	// prompt after the team's; Skills are notes type: skill its agent
	// receives whole, after the guides (IMP-233). Both are notes of the
	// team's project.
	Guide  string   `json:"guide,omitempty"`
	Skills []string `json:"skills,omitempty"`
	// Profile is the note of the profile whose fields the role takes where
	// it writes none (IMP-233); "" for none.
	Profile string `json:"profile,omitempty"`
	// Tools, when the block names them, are the only plugins' tools the
	// MCP tokens of its agent see: a plugin's name, for all its tools, or
	// one tool's (vec-pg_search); [] for none. nil: every plugin exposed
	// in the project, as without the field (IMP-233).
	Tools []string `json:"tools"`
}

// PromptNote is a note added to the system prompt of a role's agent.
type PromptNote struct {
	Path string
	// Skill: a note type: skill; else a guide.
	Skill bool
}

// PromptNotes are the notes the agent of role r receives after its agent
// note, in this order: the team's guide, the role's, the role's skills. A
// note named twice comes once, where it comes first.
func (t Team) PromptNotes(r Role) []PromptNote {
	var out []PromptNote
	add := func(p string, skill bool) {
		if p != "" && !slices.ContainsFunc(out, func(n PromptNote) bool { return n.Path == p }) {
			out = append(out, PromptNote{Path: p, Skill: skill})
		}
	}
	add(t.Guide, false)
	add(r.Guide, false)
	for _, s := range r.Skills {
		add(s, true)
	}
	return out
}

// Line is the line of the role's key in its block, 0 when unknown.
func (r Role) Line() int { return r.line }

// Commits says whether the role's work is a commit on its repository, the
// branch of a pull request: a role without a repo, or that reads it
// (access read), changes no file of one.
func (r Role) Commits() bool { return r.Repo != "" && r.Access != AccessRead }

// Which problems of a review reopen the work (Team.ReopenOn).
const (
	ReopenSerious = "serious"
	ReopenAny     = "any"
)

// Verdict is what a reviewer's report says, as ReviewOf reads it.
type Verdict struct {
	// Verdict is "passed" or "to correct"; "" when the report has none.
	Verdict        string
	Serious, Minor int
}

var (
	verdictLine = regexp.MustCompile(`(?mi)^[\s>*_#-]*VERDICT[*_]*\s*:[*_\s]*(passed|to correct)\b`)
	problemLine = regexp.MustCompile(`(?mi)^\s*(?:[-*+]|\d+[.)])\s*[*_]*\[(serious|minor)\]`)
)

// ReviewOf reads the verdict line and the problems of a reviewer's
// report: "VERDICT: passed" or "VERDICT: to correct" (the last one
// counts), and the list items that start with [serious] or [minor].
func ReviewOf(report string) Verdict {
	var r Verdict
	if m := verdictLine.FindAllStringSubmatch(report, -1); len(m) > 0 {
		r.Verdict = strings.ToLower(m[len(m)-1][1])
	}
	for _, m := range problemLine.FindAllStringSubmatch(report, -1) {
		if strings.EqualFold(m[1], "serious") {
			r.Serious++
		} else {
			r.Minor++
		}
	}
	return r
}

// ReviewInstructions end the prompt of a role that reviews.
const ReviewInstructions = "You review: you judge, you do not correct. End your report with these lines, in English as written, whatever the language of the rest:\n" +
	"- one list item for each problem you leave open, starting with `[serious]` or `[minor]`: `- [serious] where, what is wrong, what it should be`. Serious: wrong, unsafe, against what the sources or the task say, or failing how the work is accepted. Minor: wording, a preference, a remark that asks no change, something to verify that you could not;\n" +
	"- then one line, `VERDICT: passed` when you leave no serious problem open, else `VERDICT: to correct`.\n" +
	"The pipeline counts these lines: a report without the verdict line goes back to you."

// Role returns the role named name.
func (t Team) Role(name string) (Role, bool) {
	for _, r := range t.Roles {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}

// Name is the team's name in the gate's resources: its note's path
// without .md.
func (t Team) Name() string { return strings.TrimSuffix(t.Path, ".md") }

// Project is the project of the team's note.
func (t Team) Project() string {
	p, _, _ := strings.Cut(t.Path, "/")
	return p
}

// Problem is what keeps a block from reading as a team; Line counts from
// the block's first line. Role or Room is the role or the room it is
// about, "" for the team's (IMP-233: the office's map marks them).
type Problem struct {
	Line int    `json:"line,omitempty"`
	Msg  string `json:"message"`
	Role string `json:"role,omitempty"`
	Room string `json:"room,omitempty"`
}

func (p Problem) String() string {
	if p.Line > 0 {
		return "line " + strconv.Itoa(p.Line) + ": " + p.Msg
	}
	return p.Msg
}

// Block is a ```team or ```board block of a note, at its line.
type Block struct {
	Line int
	Src  string
}

// Find returns the team blocks of a note's body.
func Find(body []byte) []Block { return findBlocks(body, Lang) }

// FindProfiles returns the profile blocks of a note's body.
func FindProfiles(body []byte) []Block { return findBlocks(body, ProfileLang) }

// findBlocks returns the blocks of language lang of a note's body.
func findBlocks(body []byte, lang string) []Block {
	fenced := parser.FindFenced(body, lang)
	out := make([]Block, 0, len(fenced))
	for _, b := range fenced {
		out = append(out, Block{Line: strings.Count(string(body[:b.Start]), "\n") + 1, Src: b.Spec})
	}
	return out
}

var (
	roleName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	runName  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}\.run$`)
	// toolName is a plugin's name (vec-pg) or one of its tools'
	// (vec-pg_search): the plugin's, _, the tool's.
	toolName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}(_[A-Za-z0-9_-]{1,64})?$`)
	lineRe   = regexp.MustCompile(`line (\d+)`)
	fieldRe  = regexp.MustCompile(`line (\d+): field (\S+) not found in type \S+`)
)

type rawRole struct {
	Agent       string    `yaml:"agent"`
	Run         string    `yaml:"run"`
	Model       string    `yaml:"model"`
	Effort      string    `yaml:"effort"`
	MaxTurns    int       `yaml:"max_turns"`
	MaxSeconds  int       `yaml:"max_seconds"`
	MaxTokens   int       `yaml:"max_tokens"`
	Access      string    `yaml:"access"`
	Repo        string    `yaml:"repo"`
	Host        string    `yaml:"host"`
	Paths       []string  `yaml:"paths"`
	Forbid      []string  `yaml:"forbid"`
	Checks      string    `yaml:"checks"`
	Preview     string    `yaml:"preview"`
	Writes      []string  `yaml:"writes"`
	DelegatesTo []string  `yaml:"delegates_to"`
	Approve     string    `yaml:"approve"`
	Review      bool      `yaml:"review"`
	Guide       string    `yaml:"guide"`
	Skills      []string  `yaml:"skills"`
	Tools       *[]string `yaml:"tools"`
	Profile     string    `yaml:"profile"`
}

// rawProfile is the block of a profile: the fields of a role that are not
// of a team's repository, host or folders.
type rawProfile struct {
	Agent      string    `yaml:"agent"`
	Run        string    `yaml:"run"`
	Model      string    `yaml:"model"`
	Effort     string    `yaml:"effort"`
	MaxTurns   int       `yaml:"max_turns"`
	MaxSeconds int       `yaml:"max_seconds"`
	MaxTokens  int       `yaml:"max_tokens"`
	Access     string    `yaml:"access"`
	Review     bool      `yaml:"review"`
	Guide      string    `yaml:"guide"`
	Skills     []string  `yaml:"skills"`
	Tools      *[]string `yaml:"tools"`
}

const profileFields = "agent, run, model, effort, max_turns, max_seconds, max_tokens, access, review, guide, skills, tools"

type rawTeam struct {
	Coordinator string `yaml:"coordinator"`
	Split       string `yaml:"split"`
	Budget      struct {
		DayUSD  float64 `yaml:"day_usd"`
		TaskUSD float64 `yaml:"task_usd"`
	} `yaml:"budget"`
	MaxAgents int       `yaml:"max_agents"`
	MaxRounds int       `yaml:"max_rounds"`
	ReopenOn  string    `yaml:"reopen_on"`
	Alert     []string  `yaml:"alert"`
	Approvers []string  `yaml:"approvers"`
	Guide     string    `yaml:"guide"`
	Rooms     yaml.Node `yaml:"rooms"`
	Roles     yaml.Node `yaml:"roles"`
}

const teamFields = "coordinator, split, budget, max_agents, max_rounds, reopen_on, alert, approvers, guide, rooms, roles"

// roleFields are the fields of a role, as a problem lists them.
const roleFields = "profile, agent, run, model, effort, max_turns, max_seconds, max_tokens, access, repo, host, paths, forbid, checks, preview, writes, delegates_to, approve, review, guide, skills, tools"

// Profiles reads the block of the profile note at path, a note of the
// team's project (ProfilesFrom).
type Profiles func(path string) (string, error)

// Parse reads a block of the team note at notePath, without the profiles:
// a role that names one has a problem. The server reads its teams with
// ParseWith.
func Parse(src, notePath string) (Team, []Problem) { return ParseWith(src, notePath, nil) }

// ParseWith reads a block of the team note at notePath, each role with
// the fields of its profile it does not write (IMP-233), read by profiles.
// A block that does not read still gives what it could, with its problems.
func ParseWith(src, notePath string, profiles Profiles) (Team, []Problem) {
	t := Team{Path: notePath, Split: SplitContext, MaxRounds: defaultRounds}
	var r rawTeam
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && err.Error() != "EOF" {
		return t, []Problem{yamlProblem(err, "a team: "+teamFields)}
	}
	var problems []Problem
	bad := func(line int, format string, args ...any) {
		problems = append(problems, Problem{Line: line, Msg: fmt.Sprintf(format, args...)})
	}
	badRole := func(ro Role, format string, args ...any) {
		problems = append(problems, Problem{Line: ro.line, Msg: fmt.Sprintf(format, args...), Role: ro.Name})
	}
	project, _, _ := strings.Cut(notePath, "/")
	t.Coordinator = strings.TrimSpace(r.Coordinator)
	if s := strings.TrimSpace(r.Split); s != "" {
		t.Split = s
	}
	if t.Split != SplitContext && t.Split != SplitCraft {
		bad(0, "split %q: context or craft", t.Split)
	}
	t.Budget = Budget{DayUSD: r.Budget.DayUSD, TaskUSD: r.Budget.TaskUSD}
	if t.Budget.DayUSD < 0 || t.Budget.TaskUSD < 0 {
		bad(0, "budget: day_usd and task_usd are 0 (no limit) or more")
	}
	t.MaxAgents = r.MaxAgents
	if t.MaxAgents < 0 {
		bad(0, "max_agents: 0 (no limit of the team) or more")
	}
	if r.MaxRounds != 0 {
		t.MaxRounds = r.MaxRounds
	}
	if t.MaxRounds < 1 || t.MaxRounds > maxRoundsCap {
		bad(0, "max_rounds: 1 to %d", maxRoundsCap)
	}
	switch t.ReopenOn = strings.TrimSpace(r.ReopenOn); t.ReopenOn {
	case "":
		t.ReopenOn = ReopenSerious
	case ReopenSerious, ReopenAny:
	default:
		bad(0, "reopen_on: %s (only the serious problems of a review reopen the work, the default) or %s", ReopenSerious, ReopenAny)
	}
	t.Alert = trimList(r.Alert)
	t.Approvers = trimList(r.Approvers)
	if len(t.Alert) > maxList || len(t.Approvers) > maxList {
		bad(0, "alert and approvers: at most %d each", maxList)
	}
	var err error
	if t.Guide, err = projectNote(r.Guide, project); err != nil {
		bad(0, "guide: %v", err)
	}
	roles, rp := parseRoles(&r.Roles, project, profiles)
	problems = append(problems, rp...)
	t.Roles = roles
	rooms, roomProblems := parseRooms(&r.Rooms, project, notePath)
	problems = append(problems, roomProblems...)
	t.Rooms = rooms
	for _, ro := range t.Rooms {
		if _, ok := t.Role(ro.Name); ok {
			problems = append(problems, Problem{Line: ro.line, Msg: fmt.Sprintf("room %s: a role has its name", ro.Name), Room: ro.Name})
		}
	}
	if len(t.Roles) == 0 && len(rp) == 0 {
		bad(0, "roles: a team has at least one role")
	}
	if t.Coordinator != "" {
		if _, ok := t.Role(t.Coordinator); !ok {
			bad(0, "coordinator %s is no role of the team", t.Coordinator)
		}
	}
	for _, ro := range t.Roles {
		if ro.Approve != ApproveNone && len(t.Alert) == 0 {
			badRole(ro, "role %s: approve %s asks a person, through the team's alert (the notify targets): it names none", ro.Name, ro.Approve)
			break
		}
	}
	for _, ro := range t.Roles {
		for _, d := range ro.DelegatesTo {
			if _, room := t.Room(d); room {
				continue
			}
			to, ok := t.Role(d)
			switch {
			case !ok:
				badRole(ro, "role %s: delegates_to %s, which is no role nor room of the team", ro.Name, d)
			case d == ro.Name:
				badRole(ro, "role %s: delegates_to itself", ro.Name)
			case len(to.DelegatesTo) > 0:
				badRole(ro, "role %s: delegates_to %s, which delegates too: a team delegates one level", ro.Name, d)
			}
		}
	}
	return t, problems
}

// parseRooms reads the rooms mapping, name to the room's note, in its
// order.
func parseRooms(n *yaml.Node, project, notePath string) ([]Room, []Problem) {
	if n.Kind == 0 {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, []Problem{{Line: n.Line, Msg: "rooms: a mapping of room names to their teams' notes"}}
	}
	var rooms []Room
	var problems []Problem
	if len(n.Content)/2 > maxRooms {
		problems = append(problems, Problem{Line: n.Line, Msg: fmt.Sprintf("rooms: at most %d", maxRooms)})
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		name := strings.TrimSpace(k.Value)
		bad := func(format string, args ...any) {
			problems = append(problems, Problem{Line: k.Line, Msg: "room " + name + ": " + fmt.Sprintf(format, args...), Room: name})
		}
		switch {
		case !roleName.MatchString(name):
			problems = append(problems, Problem{Line: k.Line, Msg: fmt.Sprintf("room %q: lower-case letters, digits and -, starting with a letter, at most 32", name), Room: name})
			continue
		case slices.ContainsFunc(rooms, func(r Room) bool { return r.Name == name }):
			bad("written twice")
			continue
		case v.Kind != yaml.ScalarNode:
			bad("the link to its team's note")
			continue
		}
		p, err := projectNote(v.Value, project)
		switch {
		case err != nil:
			bad("%v", err)
			continue
		case p == "":
			bad("the link to its team's note")
			continue
		case p == notePath:
			bad("the team itself")
			continue
		case slices.ContainsFunc(rooms, func(r Room) bool { return r.Path == p }):
			bad("%s is another room too", p)
			continue
		}
		rooms = append(rooms, Room{Name: name, Path: p, line: k.Line})
	}
	return rooms, problems
}

// parseRoles reads the roles mapping in its order.
func parseRoles(n *yaml.Node, project string, profiles Profiles) ([]Role, []Problem) {
	if n.Kind == 0 {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, []Problem{{Line: n.Line, Msg: "roles: a mapping of role names to roles"}}
	}
	var roles []Role
	var problems []Problem
	if len(n.Content)/2 > maxRoles {
		problems = append(problems, Problem{Line: n.Line, Msg: fmt.Sprintf("roles: at most %d", maxRoles)})
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		name := strings.TrimSpace(k.Value)
		bad := func(format string, args ...any) {
			problems = append(problems, Problem{Line: k.Line, Msg: "role " + name + ": " + fmt.Sprintf(format, args...), Role: name})
		}
		if !roleName.MatchString(name) {
			problems = append(problems, Problem{Line: k.Line, Msg: fmt.Sprintf("role %q: lower-case letters, digits and -, starting with a letter, at most 32", name), Role: name})
			continue
		}
		if slices.ContainsFunc(roles, func(r Role) bool { return r.Name == name }) {
			bad("written twice")
			continue
		}
		// The profile's fields first, then the role as it reads with them:
		// its checks hold for what the role ends up with. A profile that
		// does not read leaves the role with its own fields.
		profile, err := withProfile(v, project, profiles)
		if err != nil {
			bad("%v", err)
		}
		var rr rawRole
		if err := decodeKnown(v, &rr); err != nil {
			p := yamlProblem(err, "a role: "+roleFields)
			if p.Line == 0 {
				p.Line = k.Line
			}
			p.Msg = "role " + name + ": " + p.Msg
			p.Role = name
			problems = append(problems, p)
			continue
		}
		ro := Role{Name: name, Profile: profile, Agent: LinkPath(rr.Agent, project), Run: strings.TrimSpace(rr.Run),
			Model: strings.TrimSpace(rr.Model), Effort: strings.TrimSpace(rr.Effort),
			MaxTurns: rr.MaxTurns, MaxSeconds: rr.MaxSeconds, MaxTokens: rr.MaxTokens, Access: strings.TrimSpace(rr.Access),
			Repo: strings.TrimSpace(rr.Repo), Host: strings.TrimSpace(rr.Host),
			Checks: strings.TrimSpace(rr.Checks), Preview: strings.Trim(strings.TrimSpace(rr.Preview), "/"), DelegatesTo: trimList(rr.DelegatesTo),
			Approve: strings.TrimSpace(rr.Approve), Review: rr.Review, line: k.Line}
		if !runName.MatchString(ro.Run) {
			bad("run %q: the node that runs it, a plugin's <name>.run (claude.run, pi.run)", ro.Run)
		}
		if ro.MaxTurns < 0 || ro.MaxSeconds < 0 || ro.MaxTokens < 0 {
			bad("max_turns, max_seconds and max_tokens are 0 (the executor's default) or more")
		}
		if ro.Repo != "" && ro.Host != "" {
			bad("repo or host, not both")
		}
		switch ro.Access {
		case "", AccessEdit, AccessExec:
		case AccessRead:
			// A role that reads its repository (a reviewer of an app's
			// files) changes none of them: no paths to keep it within.
			if ro.Repo != "" && (len(rr.Paths) > 0 || len(rr.Forbid) > 0) {
				bad("access read: the role reads its repo and changes nothing in it, paths and forbid have nothing to limit")
			}
		default:
			bad("access %q: read, edit or exec", ro.Access)
		}
		if (ro.Checks != "" || ro.Preview != "") && ro.Repo == "" {
			bad("checks and preview work on a repo: the role has none")
		} else if (ro.Checks != "" || ro.Preview != "") && !ro.Commits() {
			bad("checks and preview test the role's changes: with access read it makes none")
		}
		if ro.Preview != "" && (ro.Checks == "" || strings.Contains(ro.Preview, "..") || strings.HasPrefix(ro.Preview, "/")) {
			bad("preview %q: the folder of the build its checks make, in the repository", ro.Preview)
		}
		if ro.Paths, err = cleanRepoPaths(rr.Paths); err != nil {
			bad("paths: %v", err)
		}
		if ro.Forbid, err = cleanRepoPaths(rr.Forbid); err != nil {
			bad("forbid: %v", err)
		}
		if ro.Writes, err = cleanFolders(rr.Writes, project); err != nil {
			bad("writes: %v", err)
		}
		if ro.Guide, err = projectNote(rr.Guide, project); err != nil {
			bad("guide: %v", err)
		}
		if ro.Skills, err = projectNotes(rr.Skills, project); err != nil {
			bad("skills: %v", err)
		}
		if rr.Tools != nil {
			if ro.Tools, err = cleanTools(*rr.Tools); err != nil {
				bad("tools: %v", err)
			}
		}
		switch ro.Approve {
		case "":
			ro.Approve = ApproveNone
			if ro.Commits() {
				ro.Approve = ApproveMerge
			} else if ro.Host != "" {
				ro.Approve = ApproveOps
			}
		case ApproveMerge:
			if ro.Repo == "" {
				bad("approve merge: the role has no repo whose pull requests a person merges")
			} else if !ro.Commits() {
				bad("approve merge: with access read the role opens no pull request")
			}
		case ApproveOps, ApproveNone:
		default:
			bad("approve %q: merge, ops or none", ro.Approve)
		}
		if len(ro.DelegatesTo) > maxRoles {
			bad("delegates_to: at most %d", maxRoles)
		}
		roles = append(roles, ro)
	}
	return roles, problems
}

// decodeKnown decodes n into v, refusing unknown fields.
func decodeKnown(n *yaml.Node, v any) error {
	out, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && err.Error() != "EOF" {
		return err
	}
	return nil
}

// yamlProblem words a YAML error of a block: an unknown field with the
// fields it has.
func yamlProblem(err error, fields string) Problem {
	msg := err.Error()
	if m := fieldRe.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		return Problem{Line: line, Msg: fmt.Sprintf("%s is no field of %s", m[2], fields)}
	}
	p := Problem{Msg: "the YAML does not read: " + strings.TrimPrefix(msg, "yaml: ")}
	if m := lineRe.FindStringSubmatch(msg); m != nil {
		p.Line, _ = strconv.Atoi(m[1])
	}
	return p
}

// cleanRepoPaths checks the paths of a repository a role may touch:
// relative, without . or .. segments.
func cleanRepoPaths(ps []string) ([]string, error) {
	var out []string
	for _, p := range trimList(ps) {
		if strings.HasPrefix(p, "/") || slices.ContainsFunc(strings.Split(strings.TrimSuffix(p, "/"), "/"), func(s string) bool {
			return s == "" || s == "." || s == ".."
		}) {
			return nil, fmt.Errorf("%q is no path of the repository: relative, without . and ..", p)
		}
		out = append(out, p)
	}
	if len(out) > maxList {
		return nil, fmt.Errorf("at most %d", maxList)
	}
	return out, nil
}

// ProfilePath is the note of the profile a role names: a bare name is in
// the project's profiles/ folder (analyst: <project>/profiles/analyst.md),
// a link or a path a note of the project. "" stays "".
func ProfilePath(v, project string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if strings.HasPrefix(v, "[[") || strings.Contains(v, "/") {
		return projectNote(v, project)
	}
	name := strings.TrimSuffix(v, ".md")
	if !roleName.MatchString(name) {
		return "", fmt.Errorf("profile %q: a name of %s/ (lower-case letters, digits and -) or a link to a note of the project", v, ProfileFolder)
	}
	return project + "/" + ProfileFolder + "/" + name + ".md", nil
}

// withProfile adds to the role v the fields of its profile it does not
// write, and says the profile's note; "" for a role without one.
func withProfile(v *yaml.Node, project string, profiles Profiles) (string, error) {
	if v.Kind != yaml.MappingNode {
		return "", nil
	}
	var name *yaml.Node
	for i := 0; i+1 < len(v.Content); i += 2 {
		if v.Content[i].Value == "profile" {
			name = v.Content[i+1]
		}
	}
	if name == nil {
		return "", nil
	}
	p, err := ProfilePath(name.Value, project)
	if err != nil || p == "" {
		return "", err
	}
	if profiles == nil {
		return p, fmt.Errorf("profile %s: the profiles are read by the server", p)
	}
	src, err := profiles(p)
	if err != nil {
		return p, fmt.Errorf("profile %s: %v", p, err)
	}
	fields, problem := readProfile(src)
	if problem != nil {
		return p, fmt.Errorf("profile %s: %s", p, problem.Msg)
	}
	for i := 0; fields != nil && i+1 < len(fields.Content); i += 2 {
		k := fields.Content[i].Value
		if !hasKey(v, k) {
			v.Content = append(v.Content, fields.Content[i], fields.Content[i+1])
		}
	}
	return p, nil
}

// hasKey says whether the mapping m has the key k.
func hasKey(m *yaml.Node, k string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return true
		}
	}
	return false
}

// readProfile reads a profile's block: its mapping of fields, nil when
// empty, or the problem that keeps it from reading.
func readProfile(src string) (*yaml.Node, *Problem) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		p := yamlProblem(err, "a profile: "+profileFields)
		return nil, &p
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, &Problem{Line: m.Line, Msg: "a profile is a mapping of a role's fields: " + profileFields}
	}
	var rp rawProfile
	if err := decodeKnown(m, &rp); err != nil {
		p := yamlProblem(err, "a profile: "+profileFields)
		return nil, &p
	}
	return m, nil
}

// CheckProfile reads the block of a profile note, for the lint: its fields
// as a role would read them alone.
func CheckProfile(src, notePath string) []Problem {
	m, problem := readProfile(src)
	if problem != nil {
		return []Problem{*problem}
	}
	role := &yaml.Node{Kind: yaml.MappingNode}
	if m != nil {
		role.Content = append(role.Content, m.Content...)
	}
	if !hasKey(role, "run") {
		// A profile may leave the executor to its roles.
		role.Content = append(role.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "run"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "x.run"})
	}
	project, _, _ := strings.Cut(notePath, "/")
	roles := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "profile"}, role}}
	_, problems := parseRoles(roles, project, nil)
	for i := range problems {
		problems[i].Line = 0
		problems[i].Msg = strings.TrimPrefix(problems[i].Msg, "role profile: ")
	}
	return problems
}

// ProfilesFrom reads the profiles a team's roles name with load, which
// gives a note's content: a note type: profile with one ```profile block.
func ProfilesFrom(load func(path string) ([]byte, error)) Profiles {
	return func(p string) (string, error) {
		content, err := load(p)
		if err != nil {
			return "", errors.New("no note of the project")
		}
		typ, _ := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(content))["type"].(string)
		if strings.TrimSpace(typ) != ProfileType {
			return "", errors.New("no note type: profile")
		}
		blocks := FindProfiles(content)
		if len(blocks) != 1 {
			return "", fmt.Errorf("a profile holds one ```profile block, this one %d", len(blocks))
		}
		return blocks[0].Src, nil
	}
}

// cleanTools checks the plugins and tools a role names, each once; none is
// a list, empty, never nil.
func cleanTools(ts []string) ([]string, error) {
	out := []string{}
	for _, t := range trimList(ts) {
		if !toolName.MatchString(t) {
			return nil, fmt.Errorf("%q is no plugin (vec-pg) nor plugin's tool (vec-pg_search)", t)
		}
		if slices.Contains(out, t) {
			return nil, fmt.Errorf("%s is written twice", t)
		}
		out = append(out, t)
	}
	if len(out) > maxList {
		return nil, fmt.Errorf("at most %d", maxList)
	}
	return out, nil
}

// projectNote is the vault path of a note a block links to (a guide, a
// skill), a note of the team's project: a link without the project is
// inside it, as an agent's (AgentPrompt). "" stays "".
func projectNote(v, project string) (string, error) {
	p := LinkPath(v, project)
	if p == "" {
		return "", nil
	}
	if !strings.HasPrefix(p, project+"/") {
		p = project + "/" + p
	}
	if c := path.Clean(p); c != p || !strings.HasPrefix(c, project+"/") {
		return "", fmt.Errorf("%q is no note of the project %s", strings.TrimSpace(v), project)
	}
	if path.Ext(p) != ".md" {
		return "", fmt.Errorf("%s is no note: a note ends with .md", p)
	}
	return p, nil
}

// projectNotes checks a list of notes of the project, each once.
func projectNotes(vs []string, project string) ([]string, error) {
	var out []string
	for _, v := range trimList(vs) {
		p, err := projectNote(v, project)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, p) {
			return nil, fmt.Errorf("%s is written twice", p)
		}
		out = append(out, p)
	}
	if len(out) > maxSkills {
		return nil, fmt.Errorf("at most %d", maxSkills)
	}
	return out, nil
}

// cleanFolders checks the folders of the vault a role writes: inside the
// project, each ending with /.
func cleanFolders(fs []string, project string) ([]string, error) {
	var out []string
	for _, f := range trimList(fs) {
		f = strings.Trim(f, "/")
		if c := path.Clean(f); c != f || f == "" || strings.HasPrefix(f, "..") {
			return nil, fmt.Errorf("%q is no folder of the vault", f)
		}
		if f != project && !strings.HasPrefix(f, project+"/") {
			return nil, fmt.Errorf("%s is outside the project %s", f, project)
		}
		out = append(out, f+"/")
	}
	if len(out) > maxList {
		return nil, fmt.Errorf("at most %d", maxList)
	}
	return out, nil
}

func trimList(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// LinkPath turns a link to a note, as a frontmatter or a block writes it
// ("[[fitness/squadra]]", "[[squadra|the team]]", "fitness/squadra.md"),
// into its vault path: a target without a folder is in project. "" stays
// "".
func LinkPath(v, project string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(strings.TrimPrefix(v, "[["), "]]")
	if i := strings.IndexAny(v, "|#"); i >= 0 {
		v = v[:i]
	}
	v = strings.Trim(strings.TrimSpace(v), "/")
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "/") && project != "" {
		v = project + "/" + v
	}
	if path.Ext(v) == "" {
		v += ".md"
	}
	return v
}

// Link is the wikilink the notes write for a vault path.
func Link(p string) string {
	if p == "" {
		return ""
	}
	return "[[" + strings.TrimSuffix(p, ".md") + "]]"
}
