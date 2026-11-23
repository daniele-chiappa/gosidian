package v1

import (
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
)

// The state of a role on the office's map (IMP-233, docs/plugins.md, the
// bridge office/1): the first that holds of its open tasks.
const (
	// roleWaiting: a task waits for a person (input-required, review).
	roleWaiting = "waiting"
	// roleHeld: a task submitted or at work waits, for a budget, a limit,
	// the tasks it depends on.
	roleHeld = "held"
	// roleWorking: a run holds a task.
	roleWorking = "working"
	// roleQueued: a task is ready and no run holds it yet.
	roleQueued = "queued"
	roleIdle   = "idle"
)

// officeMaxTasks caps the open tasks listed for a role.
const officeMaxTasks = 20

type officeTask struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Waits  string `json:"waits"`
}

type officeAgent struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Missing bool   `json:"missing"`
}

// officeNote is a guide or a skill of a role (IMP-233), as the map opens
// it.
type officeNote struct {
	officeAgent
	// Kind is profile (the role's), team_guide (the team's guide), guide
	// (the role's) or skill.
	Kind string `json:"kind"`
	// NotSkill: a skill whose note is no note type: skill.
	NotSkill bool `json:"not_skill,omitempty"`
}

type officeRole struct {
	State string       `json:"state"`
	Agent *officeAgent `json:"agent,omitempty"`
	// Notes are the guides and skills its agent receives, in their order.
	Notes []officeNote `json:"notes"`
	// Unexposed are the tools the role names whose plugin is not exposed
	// in the project: its agent cannot call them (IMP-233).
	Unexposed []string `json:"unexposed_tools,omitempty"`
	// Fields are those the block writes for the role, in their order: the
	// others it has are its profile's (IMP-233, phase C).
	Fields     []string     `json:"fields"`
	Tasks      []officeTask `json:"tasks"`
	SpentToday *jobs.Spend  `json:"spent_today,omitempty"`
	modified   map[string]time.Time
}

type officeTeam struct {
	Path       string                 `json:"path"`
	Title      string                 `json:"title"`
	Line       int                    `json:"line"`
	Team       *fleet.Team            `json:"team"`
	Problems   []fleet.Problem        `json:"problems"`
	Pipelines  bool                   `json:"pipelines"`
	SpentToday *jobs.Spend            `json:"spent_today,omitempty"`
	Roles      map[string]*officeRole `json:"roles"`
	// Rooms are the team's rooms, each with its view: nil for a room
	// whose note is missing or the reader may not see (IMP-233).
	Rooms []officeRoom `json:"rooms,omitempty"`
	Now   time.Time    `json:"now"`
	// ETag is the team's note's, Editable whether the reader may write its
	// project, the map's changes (IMP-233, phase C): only on the team
	// asked, not on its rooms.
	ETag     string `json:"etag,omitempty"`
	Editable bool   `json:"editable,omitempty"`
}

// noteLoader reads a note of the vault, or of a draft of the map over it.
type noteLoader func(p string) (*vault.Note, error)

// officeRoom is a room of the team, as the map draws it.
type officeRoom struct {
	Name string      `json:"name"`
	Path string      `json:"path"`
	View *officeTeam `json:"view"`
}

// handleFleetTeam answers GET /api/v1/fleet/teams/<path>: the team of the
// note at path as the server reads it, with the state and today's spend of
// each role, for whoever may read the note (IMP-233). The office's map
// draws it; it changes nothing.
func (r *Router) handleFleetTeam(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPost {
		r.handleFleetTeamEdit(w, req)
		return
	}
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	rel := strings.TrimPrefix(req.URL.Path, "/api/v1/fleet/teams/")
	clean, err := r.deps.Vault.Rel(rel)
	if err != nil || rel == "" || path.Ext(clean) != ".md" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: a team is a markdown note")
		return
	}
	p := principalFromContext(req)
	if !r.canSee(p, clean) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	note, err := r.deps.Vault.Load(clean)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	eng := r.officeEngine()
	out := r.officeView(clean, note, eng, r.deps.Vault.Load)
	r.officeRooms(&out, p, eng, r.deps.Vault.Load)
	out.ETag = note.ETag()
	out.Editable = p.CanWrite() && r.canWriteProject(p, projectOf(clean))
	writeJSON(w, http.StatusOK, out)
}

// officeEngine is the pipeline engine while the pipelines are on: a nil
// *pipeline.Engine in the interface would not read as nil.
func (r *Router) officeEngine() officeEngine {
	if r.deps.Pipelines != nil {
		return r.deps.Pipelines
	}
	return nil
}

// officeRooms adds to out its team's rooms (IMP-233), each as the map
// draws a team, for whoever may read the room's note too.
func (r *Router) officeRooms(out *officeTeam, p authz.Principal, eng officeEngine, load noteLoader) {
	if out.Team == nil {
		return
	}
	for _, room := range out.Team.Rooms {
		v := officeRoom{Name: room.Name, Path: room.Path}
		if rn, err := load(room.Path); err == nil && r.canSee(p, room.Path) {
			view := r.officeView(room.Path, rn, eng, load)
			v.View = &view
		}
		out.Rooms = append(out.Rooms, v)
	}
}

// officeView is the team of the note at clean, note's content, as the map
// draws it, its other notes read by load.
func (r *Router) officeView(clean string, note *vault.Note, eng officeEngine, load noteLoader) officeTeam {
	out := officeTeam{Path: clean, Title: note.DisplayTitle(), Problems: []fleet.Problem{},
		Pipelines: eng != nil, Roles: map[string]*officeRole{}, Now: time.Now()}
	if eng != nil {
		out.Now = eng.Now()
	}
	blocks := fleet.Find(note.Content)
	if t := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(clean, note.Content))["type"]; strings.TrimSpace(fmt.Sprint(t)) != fleet.TeamType {
		out.Problems = append(out.Problems, fleet.Problem{Msg: "the note is not type: team"})
	}
	switch len(blocks) {
	case 0:
		out.Problems = append(out.Problems, fleet.Problem{Msg: "the note has no ```team block"})
		return out
	case 1:
	default:
		out.Problems = append(out.Problems, fleet.Problem{Msg: "a note type: team holds one ```team block, this one " + strconv.Itoa(len(blocks))})
	}
	b := blocks[0]
	out.Line = b.Line
	team, problems := fleet.ParseWith(b.Src, clean, profilesOf(load))
	for _, pr := range problems {
		if pr.Line > 0 {
			pr.Line += b.Line
		}
		out.Problems = append(out.Problems, pr)
	}
	if len(team.Roles) == 0 {
		// A block that does not read at all gives no roles to draw.
		return out
	}
	out.Team = &team
	r.officeState(&out, team, eng, load)
	own := fleet.RoleFields(b.Src)
	for name, or := range out.Roles {
		if or.Fields = own[name]; or.Fields == nil {
			or.Fields = []string{}
		}
	}
	return out
}

// profilesOf reads the profiles of a team's roles with load.
func profilesOf(load noteLoader) fleet.Profiles {
	return fleet.ProfilesFrom(func(p string) ([]byte, error) {
		n, err := load(p)
		if err != nil {
			return nil, err
		}
		return n.Content, nil
	})
}

// officeEngine is what the map reads of the pipeline engine: why tasks
// wait, what the team spent today, by task, and the clock.
type officeEngine interface {
	TaskWaits(project string) map[string]string
	TeamSpend(project, team string) (jobs.Spend, error)
	TeamSpendByTask(project, team string) (map[string]jobs.Spend, error)
	Now() time.Time
}

// officeNoteOf is the note at p for the map, with its frontmatter's type:
// missing when it is no note of project.
func officeNoteOf(p, project string, load noteLoader) (officeAgent, string) {
	a := officeAgent{Path: p, Title: strings.TrimSuffix(path.Base(p), ".md")}
	n, err := load(p)
	if err != nil || projectOf(p) != project {
		a.Missing = true
		return a, ""
	}
	a.Title = n.DisplayTitle()
	typ, _ := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(n.Content))["type"].(string)
	return a, strings.TrimSpace(typ)
}

// officeState fills the roles of out: each one's agent, open tasks, state
// and spend, from the tasks of the team's project and eng, nil while the
// pipelines are off.
func (r *Router) officeState(out *officeTeam, team fleet.Team, eng officeEngine, load noteLoader) {
	project := team.Project()
	for _, ro := range team.Roles {
		or := &officeRole{State: roleIdle, Notes: []officeNote{}, Tasks: []officeTask{}, modified: map[string]time.Time{}}
		if ro.Agent != "" {
			a, _ := officeNoteOf(ro.Agent, project, load)
			or.Agent = &a
		}
		if ro.Profile != "" {
			a, _ := officeNoteOf(ro.Profile, project, load)
			or.Notes = append(or.Notes, officeNote{officeAgent: a, Kind: "profile"})
		}
		for _, pn := range team.PromptNotes(ro) {
			a, typ := officeNoteOf(pn.Path, project, load)
			n := officeNote{officeAgent: a, Kind: "guide"}
			switch {
			case pn.Skill:
				n.Kind, n.NotSkill = "skill", !a.Missing && typ != fleet.SkillType
			case pn.Path == team.Guide:
				n.Kind = "team_guide"
			}
			or.Notes = append(or.Notes, n)
		}
		if r.deps.Plugins != nil {
			for _, tl := range ro.Tools {
				plugin, _, _ := strings.Cut(tl, "_")
				if !slices.Contains(r.deps.Plugins.ExposedIn(plugin), project) {
					or.Unexposed = append(or.Unexposed, tl)
				}
			}
		}
		out.Roles[ro.Name] = or
	}
	var waits map[string]string
	var byTask map[string]jobs.Spend
	if eng != nil {
		waits = eng.TaskWaits(project)
		if sp, err := eng.TeamSpend(project, team.Path); err == nil {
			out.SpentToday = &sp
		}
		byTask, _ = eng.TeamSpendByTask(project, team.Path)
	}
	for _, t := range r.teamTasks(project, team.Path) {
		or := out.Roles[t.task.Assignee]
		if or == nil {
			continue
		}
		if sp, ok := byTask[t.task.Path]; ok {
			if or.SpentToday == nil {
				or.SpentToday = &jobs.Spend{}
			}
			or.SpentToday.CostUSD += sp.CostUSD
			or.SpentToday.Tokens += sp.Tokens
			or.SpentToday.Steps += sp.Steps
		}
		if fleet.Ended(t.task.Status) {
			continue
		}
		ot := officeTask{Path: t.task.Path, Title: t.task.Title, Status: t.task.Status}
		if t.task.Status == fleet.Submitted || t.task.Status == fleet.Working {
			ot.Waits = waits[t.task.Path]
		}
		or.Tasks = append(or.Tasks, ot)
		or.modified[t.task.Path] = t.modified
		or.State = worseState(or.State, taskState(ot))
	}
	for _, or := range out.Roles {
		slices.SortStableFunc(or.Tasks, func(a, b officeTask) int {
			if c := or.modified[b.Path].Compare(or.modified[a.Path]); c != 0 {
				return c
			}
			return strings.Compare(b.Path, a.Path)
		})
		if len(or.Tasks) > officeMaxTasks {
			or.Tasks = or.Tasks[:officeMaxTasks]
		}
		if out.Pipelines && or.SpentToday == nil {
			or.SpentToday = &jobs.Spend{}
		}
	}
}

// taskState is the state an open task gives its role.
func taskState(t officeTask) string {
	switch {
	case t.Status == fleet.InputRequired || t.Status == fleet.Review:
		return roleWaiting
	case (t.Status == fleet.Submitted || t.Status == fleet.Working) && t.Waits != "":
		return roleHeld
	case t.Status == fleet.Working:
		return roleWorking
	case t.Status == fleet.Submitted:
		return roleQueued
	}
	// A board's card in its backlog is not ready: nothing for its role yet.
	return roleIdle
}

// stateRank orders the states, the one a role shows first.
var stateRank = map[string]int{roleWaiting: 4, roleHeld: 3, roleWorking: 2, roleQueued: 1, roleIdle: 0}

func worseState(a, b string) string {
	if stateRank[b] > stateRank[a] {
		return b
	}
	return a
}

type teamTask struct {
	task     fleet.Task
	modified time.Time
}

// teamTasks are the tasks of team in project, with when their note changed.
func (r *Router) teamTasks(project, team string) []teamTask {
	if r.deps.Index == nil {
		return nil
	}
	where := []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{fleet.TaskType}}}
	var out []teamTask
	for offset := 0; ; offset += index.QueryMaxLimit {
		hits, total, err := r.deps.Index.Query(index.QueryOptions{Projects: []string{project}, Where: where,
			Sort: "path", Limit: index.QueryMaxLimit, Offset: offset})
		if err != nil {
			return out
		}
		for _, h := range hits {
			n, err := r.deps.Vault.Load(h.Path)
			if err != nil {
				continue
			}
			t := fleet.ReadTask(h.Path, n.Content)
			if t.Team == team {
				out = append(out, teamTask{task: t, modified: n.ModTime})
			}
		}
		if len(hits) == 0 || offset+len(hits) >= total {
			return out
		}
	}
}
