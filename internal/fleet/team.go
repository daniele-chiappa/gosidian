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
package fleet

import (
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
)

// Team is a team as its note declares it.
type Team struct {
	// Path is the team's note.
	Path string `json:"path"`
	// Coordinator is the role that receives the objectives and splits
	// them; "" for a team whose tasks are written by hand.
	Coordinator string   `json:"coordinator,omitempty"`
	Split       string   `json:"split"`
	Budget      Budget   `json:"budget"`
	MaxAgents   int      `json:"max_agents,omitempty"`
	MaxRounds   int      `json:"max_rounds"`
	Alert       []string `json:"alert,omitempty"`
	Approvers   []string `json:"approvers,omitempty"`
	// Roles in the order the block writes them.
	Roles []Role `json:"roles"`
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
}

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
// the block's first line.
type Problem struct {
	Line int    `json:"line,omitempty"`
	Msg  string `json:"message"`
}

func (p Problem) String() string {
	if p.Line > 0 {
		return "line " + strconv.Itoa(p.Line) + ": " + p.Msg
	}
	return p.Msg
}

// Block is a ```team block of a note, at its line.
type Block struct {
	Line int
	Src  string
}

// Find returns the team blocks of a note's body.
func Find(body []byte) []Block {
	fenced := parser.FindFenced(body, Lang)
	out := make([]Block, 0, len(fenced))
	for _, b := range fenced {
		out = append(out, Block{Line: strings.Count(string(body[:b.Start]), "\n") + 1, Src: b.Spec})
	}
	return out
}

var (
	roleName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	runName  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}\.run$`)
	lineRe   = regexp.MustCompile(`line (\d+)`)
	fieldRe  = regexp.MustCompile(`line (\d+): field (\S+) not found in type \S+`)
)

type rawRole struct {
	Agent       string   `yaml:"agent"`
	Run         string   `yaml:"run"`
	Model       string   `yaml:"model"`
	Effort      string   `yaml:"effort"`
	MaxTurns    int      `yaml:"max_turns"`
	MaxSeconds  int      `yaml:"max_seconds"`
	MaxTokens   int      `yaml:"max_tokens"`
	Repo        string   `yaml:"repo"`
	Host        string   `yaml:"host"`
	Paths       []string `yaml:"paths"`
	Forbid      []string `yaml:"forbid"`
	Checks      string   `yaml:"checks"`
	Preview     string   `yaml:"preview"`
	Writes      []string `yaml:"writes"`
	DelegatesTo []string `yaml:"delegates_to"`
	Approve     string   `yaml:"approve"`
}

type rawTeam struct {
	Coordinator string `yaml:"coordinator"`
	Split       string `yaml:"split"`
	Budget      struct {
		DayUSD  float64 `yaml:"day_usd"`
		TaskUSD float64 `yaml:"task_usd"`
	} `yaml:"budget"`
	MaxAgents int       `yaml:"max_agents"`
	MaxRounds int       `yaml:"max_rounds"`
	Alert     []string  `yaml:"alert"`
	Approvers []string  `yaml:"approvers"`
	Roles     yaml.Node `yaml:"roles"`
}

const teamFields = "coordinator, split, budget, max_agents, max_rounds, alert, approvers, roles"

// Parse reads a block of the team note at notePath. A block that does not
// read still gives what it could, with its problems.
func Parse(src, notePath string) (Team, []Problem) {
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
	t.Alert = trimList(r.Alert)
	t.Approvers = trimList(r.Approvers)
	if len(t.Alert) > maxList || len(t.Approvers) > maxList {
		bad(0, "alert and approvers: at most %d each", maxList)
	}
	roles, rp := parseRoles(&r.Roles, project)
	problems = append(problems, rp...)
	t.Roles = roles
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
			bad(0, "role %s: approve %s asks a person, through the team's alert (the notify targets): it names none", ro.Name, ro.Approve)
			break
		}
	}
	for _, ro := range t.Roles {
		for _, d := range ro.DelegatesTo {
			to, ok := t.Role(d)
			switch {
			case !ok:
				bad(0, "role %s: delegates_to %s, which is no role of the team", ro.Name, d)
			case d == ro.Name:
				bad(0, "role %s: delegates_to itself", ro.Name)
			case len(to.DelegatesTo) > 0:
				bad(0, "role %s: delegates_to %s, which delegates too: a team delegates one level", ro.Name, d)
			}
		}
	}
	return t, problems
}

// parseRoles reads the roles mapping in its order.
func parseRoles(n *yaml.Node, project string) ([]Role, []Problem) {
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
			problems = append(problems, Problem{Line: k.Line, Msg: "role " + name + ": " + fmt.Sprintf(format, args...)})
		}
		if !roleName.MatchString(name) {
			problems = append(problems, Problem{Line: k.Line, Msg: fmt.Sprintf("role %q: lower-case letters, digits and -, starting with a letter, at most 32", name)})
			continue
		}
		if slices.ContainsFunc(roles, func(r Role) bool { return r.Name == name }) {
			bad("written twice")
			continue
		}
		var rr rawRole
		if err := decodeKnown(v, &rr); err != nil {
			p := yamlProblem(err, "a role: agent, run, model, effort, max_turns, max_seconds, max_tokens, repo, host, paths, forbid, checks, preview, writes, delegates_to, approve")
			if p.Line == 0 {
				p.Line = k.Line
			}
			p.Msg = "role " + name + ": " + p.Msg
			problems = append(problems, p)
			continue
		}
		ro := Role{Name: name, Agent: LinkPath(rr.Agent, project), Run: strings.TrimSpace(rr.Run),
			Model: strings.TrimSpace(rr.Model), Effort: strings.TrimSpace(rr.Effort),
			MaxTurns: rr.MaxTurns, MaxSeconds: rr.MaxSeconds, MaxTokens: rr.MaxTokens,
			Repo: strings.TrimSpace(rr.Repo), Host: strings.TrimSpace(rr.Host),
			Checks: strings.TrimSpace(rr.Checks), Preview: strings.Trim(strings.TrimSpace(rr.Preview), "/"), DelegatesTo: trimList(rr.DelegatesTo),
			Approve: strings.TrimSpace(rr.Approve)}
		if !runName.MatchString(ro.Run) {
			bad("run %q: the node that runs it, a plugin's <name>.run (claude.run, pi.run)", ro.Run)
		}
		if ro.MaxTurns < 0 || ro.MaxSeconds < 0 || ro.MaxTokens < 0 {
			bad("max_turns, max_seconds and max_tokens are 0 (the executor's default) or more")
		}
		if ro.Repo != "" && ro.Host != "" {
			bad("repo or host, not both")
		}
		if (ro.Checks != "" || ro.Preview != "") && ro.Repo == "" {
			bad("checks and preview work on a repo: the role has none")
		}
		if ro.Preview != "" && (ro.Checks == "" || strings.Contains(ro.Preview, "..") || strings.HasPrefix(ro.Preview, "/")) {
			bad("preview %q: the folder of the build its checks make, in the repository", ro.Preview)
		}
		var err error
		if ro.Paths, err = cleanRepoPaths(rr.Paths); err != nil {
			bad("paths: %v", err)
		}
		if ro.Forbid, err = cleanRepoPaths(rr.Forbid); err != nil {
			bad("forbid: %v", err)
		}
		if ro.Writes, err = cleanFolders(rr.Writes, project); err != nil {
			bad("writes: %v", err)
		}
		switch ro.Approve {
		case "":
			ro.Approve = ApproveNone
			if ro.Repo != "" {
				ro.Approve = ApproveMerge
			} else if ro.Host != "" {
				ro.Approve = ApproveOps
			}
		case ApproveMerge:
			if ro.Repo == "" {
				bad("approve merge: the role has no repo whose pull requests a person merges")
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
