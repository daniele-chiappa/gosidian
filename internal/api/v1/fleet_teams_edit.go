package v1

import (
	"errors"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
)

// officeEditRequest is a draft of the office's map (IMP-233, phase C): the
// changes of the team's block since the version etag, to check or save.
type officeEditRequest struct {
	ETag string     `json:"etag"`
	Ops  []fleet.Op `json:"ops"`
	Save bool       `json:"save"`
}

// officeEditResponse is the team as the draft would make it.
type officeEditResponse struct {
	View officeTeam `json:"view"`
	// Block is the team's block with the changes, Line where it starts in
	// the note.
	Block string `json:"block"`
	Line  int    `json:"line"`
	// ETag is the version the changes were made on; Changed: the note
	// changed since the draft's etag.
	ETag    string `json:"etag"`
	Changed bool   `json:"changed"`
	// Creates are the notes a save writes besides the team's, created
	// those it wrote.
	Creates []string `json:"creates"`
	// Problems keep the draft from being saved: the block's, and those of
	// the notes it names.
	Problems []fleet.Problem `json:"problems"`
	Saved    bool            `json:"saved"`
	Palette  *officePalette  `json:"palette,omitempty"`
}

// officePalette is what the map offers to change a team with.
type officePalette struct {
	// Profiles of the project, each with the name a role writes.
	Profiles []officeProfile `json:"profiles"`
	// Teams of the project that may become a room: a coordinator, no
	// rooms, no other team's room.
	Teams []officeAgent `json:"teams"`
	// Runs are the executors of the accepted plugins.
	Runs   []string      `json:"runs"`
	Skills []officeAgent `json:"skills"`
}

type officeProfile struct {
	officeAgent
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Fields      map[string]any `json:"fields"`
}

// officeMaxPalette caps each list of the palette.
const officeMaxPalette = 200

// handleFleetTeamEdit answers POST /api/v1/fleet/teams/<path>: the team as
// the draft's changes make it, written when save, for whoever may write
// the team's project.
func (r *Router) handleFleetTeamEdit(w http.ResponseWriter, req *http.Request) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	rel := strings.TrimPrefix(req.URL.Path, "/api/v1/fleet/teams/")
	clean, err := r.deps.Vault.Rel(rel)
	if err != nil || rel == "" || path.Ext(clean) != ".md" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid path: a team is a markdown note")
		return
	}
	p := user.principal()
	if !r.canSee(p, clean) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, p, projectOf(clean)) {
		return
	}
	var body officeEditRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Save {
		// One save of the team at a time: its etag is checked under the lock.
		unlock := r.deps.Vault.LockPath(clean)
		defer unlock()
	}
	note, err := r.deps.Vault.Load(clean)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	project := projectOf(clean)
	out := officeEditResponse{ETag: note.ETag(), Creates: []string{}, Problems: []fleet.Problem{}}
	out.Changed = body.ETag != "" && !etagMatches(body.ETag, note.ETag())
	if body.Save && (body.ETag == "" || out.Changed) {
		WriteErrorWithDetails(w, http.StatusPreconditionFailed, CodeConcurrencyEtag, "the team's note changed since the draft began: check the draft again, then save",
			map[string]any{"current_etag": quoteETag(note.ETag())})
		return
	}
	content, block, line, staged, err := fleet.EditNote(note.Content, clean, body.Ops, r.officeEditEnv(clean))
	if err != nil {
		details := map[string]any{}
		if oe := (*fleet.OpError)(nil); errors.As(err, &oe) {
			details["op"] = oe.Index
			err = errors.New(oe.Msg)
		}
		WriteErrorWithDetails(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error(), details)
		return
	}
	out.Block, out.Line = block, line
	// The draft's notes over the vault's: the team's new content, and the
	// notes the save writes.
	over := map[string]*vault.Note{clean: {Path: clean, Title: note.Title, Content: content, ModTime: note.ModTime, Size: int64(len(content))}}
	for _, n := range staged {
		over[n.Path] = &vault.Note{Path: n.Path, Title: strings.TrimSuffix(path.Base(n.Path), ".md"), Content: n.Content, Size: int64(len(n.Content))}
		out.Creates = append(out.Creates, n.Path)
	}
	load := func(p string) (*vault.Note, error) {
		if n, ok := over[p]; ok {
			return n, nil
		}
		return r.deps.Vault.Load(p)
	}
	eng := r.officeEngine()
	out.View = r.officeView(clean, over[clean], eng, load)
	r.officeRooms(&out.View, p, eng, load)
	out.View.ETag, out.View.Editable = note.ETag(), true
	out.Problems = officeSaveProblems(out.View, line)
	if !body.Save {
		out.Palette = r.officePalette(clean, project, out.View)
		writeJSON(w, http.StatusOK, out)
		return
	}
	if len(out.Problems) > 0 {
		WriteErrorWithDetails(w, http.StatusUnprocessableEntity, CodeValidationFormat,
			"the team has problems, the first: "+out.Problems[0].Msg, map[string]any{"problems": out.Problems})
		return
	}
	// The notes the team names first, then the team: a save that stops
	// halfway leaves a team as it was, and a draft that still applies.
	var created []string
	for _, n := range staged {
		wrote, err := r.createIfMissing(req, user, n)
		if err != nil {
			WriteError(w, http.StatusConflict, CodeConflict, err.Error())
			return
		}
		if wrote {
			created = append(created, n.Path)
		}
	}
	if err := r.writeAndIndex(clean, content); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	fresh, err := r.deps.Vault.Load(clean)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	r.auditNote(req, audit.ActionUpdate, user, clean, "", int64(len(content)))
	r.publishNoteEvent(events.TopicNote, clean, "update", fresh)
	out.Saved, out.ETag, out.Changed = true, fresh.ETag(), false
	out.Creates = append([]string{}, created...)
	out.View = r.officeView(clean, fresh, eng, r.deps.Vault.Load)
	r.officeRooms(&out.View, p, eng, r.deps.Vault.Load)
	out.View.ETag, out.View.Editable = fresh.ETag(), true
	out.Problems = officeSaveProblems(out.View, line)
	w.Header().Set("ETag", quoteETag(fresh.ETag()))
	writeJSON(w, http.StatusOK, out)
}

// officeEditEnv is what the changes of the team at clean read outside its
// block: the profiles, the open tasks of its roles, the notes of the vault.
func (r *Router) officeEditEnv(clean string) fleet.EditEnv {
	project := projectOf(clean)
	var open map[string][]string
	return fleet.EditEnv{
		Profiles: profilesOf(r.deps.Vault.Load),
		OpenTasks: func(role string) []string {
			if open == nil {
				open = map[string][]string{}
				for _, t := range r.teamTasks(project, clean) {
					if !fleet.Ended(t.task.Status) {
						open[t.task.Assignee] = append(open[t.task.Assignee], t.task.Path)
					}
				}
			}
			return open[role]
		},
		Exists: func(p string) bool {
			_, err := r.deps.Vault.Load(p)
			return err == nil
		},
		IsTeam: func(p string) bool {
			n, err := r.deps.Vault.Load(p)
			return err == nil && noteType(n) == fleet.TeamType
		},
	}
}

func noteType(n *vault.Note) string {
	typ, _ := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(n.Path, n.Content))["type"].(string)
	return strings.TrimSpace(typ)
}

// createIfMissing writes the note n of a saved draft: an agent's, a
// room's. A note there already is left as it is when n is only for a
// vault that lacks it, and stops the save when not.
func (r *Router) createIfMissing(req *http.Request, user *RequestUser, n fleet.NewNote) (bool, error) {
	unlock := r.deps.Vault.LockPath(n.Path)
	defer unlock()
	if _, err := r.deps.Vault.Load(n.Path); err == nil {
		if n.IfMissing {
			return false, nil
		}
		return false, errors.New(n.Path + " exists already: check the draft again")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := r.writeAndIndex(n.Path, n.Content); err != nil {
		return false, err
	}
	fresh, _ := r.deps.Vault.Load(n.Path)
	r.auditNote(req, audit.ActionCreate, user, n.Path, "", int64(len(n.Content)))
	r.publishNoteEvent(events.TopicNote, n.Path, "create", fresh)
	r.publishNoteEvent(events.TopicTree, n.Path, "create", fresh)
	return true, nil
}

// officeSaveProblems are what keeps the team of view from being saved: the
// problems of its block, a note it names that is missing or of another
// type, a room that cannot be one. Each says its role or room, and the
// line of the block when it has one.
func officeSaveProblems(view officeTeam, line int) []fleet.Problem {
	out := append([]fleet.Problem{}, view.Problems...)
	if view.Team == nil {
		return out
	}
	teamGuide := false
	for _, ro := range view.Team.Roles {
		or := view.Roles[ro.Name]
		if or == nil {
			continue
		}
		at := line
		if ro.Line() > 0 {
			at += ro.Line()
		}
		if or.Agent != nil && or.Agent.Missing {
			out = append(out, fleet.Problem{Line: at, Role: ro.Name, Msg: "role " + ro.Name + ": agent " + or.Agent.Path + " is no note of the project"})
		}
		for _, n := range or.Notes {
			switch {
			case n.Missing && n.Kind == "team_guide":
				if !teamGuide {
					teamGuide = true
					out = append(out, fleet.Problem{Line: line, Msg: "guide " + n.Path + " is no note of the project"})
				}
			case n.Missing:
				out = append(out, fleet.Problem{Line: at, Role: ro.Name, Msg: "role " + ro.Name + ": " + n.Kind + " " + n.Path + " is no note of the project"})
			case n.NotSkill:
				out = append(out, fleet.Problem{Line: at, Role: ro.Name, Msg: "role " + ro.Name + ": skill " + n.Path + " is no note type: skill"})
			}
		}
	}
	for _, room := range view.Rooms {
		var msg string
		switch {
		case room.View == nil:
			msg = "is no note of the project"
		case room.View.Team == nil || len(room.View.Problems) > 0:
			msg = "is no team that reads (its own problems say why)"
		case room.View.Team.Coordinator == "":
			msg = "has no coordinator to receive the objectives"
		case len(room.View.Team.Rooms) > 0:
			msg = "has rooms of its own: a room has none"
		}
		if msg != "" {
			out = append(out, fleet.Problem{Line: line, Room: room.Name, Msg: "room " + room.Name + ": " + room.Path + " " + msg})
		}
	}
	return out
}

// officePalette is what the map offers to change the team at clean with.
func (r *Router) officePalette(clean, project string, view officeTeam) *officePalette {
	out := &officePalette{Profiles: []officeProfile{}, Teams: []officeAgent{}, Runs: []string{}, Skills: []officeAgent{}}
	if r.deps.Plugins != nil {
		for _, n := range r.deps.Plugins.NodeSchemas() {
			if strings.HasSuffix(n.Type, ".run") && !slices.Contains(out.Runs, n.Type) {
				out.Runs = append(out.Runs, n.Type)
			}
		}
	}
	if view.Team != nil {
		for _, ro := range view.Team.Roles {
			if ro.Run != "" && !slices.Contains(out.Runs, ro.Run) {
				out.Runs = append(out.Runs, ro.Run)
			}
		}
	}
	for _, n := range r.officeNotesOfType(project, fleet.ProfileType) {
		pr := officeProfile{officeAgent: officeAgent{Path: n.Path, Title: n.DisplayTitle()}, Fields: map[string]any{}}
		pr.Name = fleet.Link(n.Path)
		if path.Dir(n.Path) == project+"/"+fleet.ProfileFolder {
			pr.Name = strings.TrimSuffix(path.Base(n.Path), ".md")
		}
		pr.Description, _ = parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(n.Content))["description"].(string)
		if blocks := fleet.FindProfiles(n.Content); len(blocks) == 1 {
			var fields map[string]any
			if yaml.Unmarshal([]byte(blocks[0].Src), &fields) == nil && fields != nil {
				pr.Fields = fields
			}
		}
		out.Profiles = append(out.Profiles, pr)
	}
	// The teams that may become rooms: read, with a coordinator, no rooms,
	// no other team's room.
	var teams []fleet.Team
	roomOf := map[string]bool{}
	for _, n := range r.officeNotesOfType(project, fleet.TeamType) {
		blocks := fleet.Find(n.Content)
		if len(blocks) != 1 {
			continue
		}
		t, problems := fleet.ParseWith(blocks[0].Src, n.Path, profilesOf(r.deps.Vault.Load))
		for _, ro := range t.Rooms {
			roomOf[ro.Path] = true
		}
		if len(problems) == 0 && n.Path != clean && t.Coordinator != "" && len(t.Rooms) == 0 {
			teams = append(teams, t)
		}
	}
	for _, t := range teams {
		if roomOf[t.Path] {
			continue
		}
		title := strings.TrimSuffix(path.Base(t.Path), ".md")
		if n, err := r.deps.Vault.Load(t.Path); err == nil {
			title = n.DisplayTitle()
		}
		out.Teams = append(out.Teams, officeAgent{Path: t.Path, Title: title})
	}
	for _, n := range r.officeNotesOfType(project, fleet.SkillType) {
		out.Skills = append(out.Skills, officeAgent{Path: n.Path, Title: n.DisplayTitle()})
	}
	return out
}

// officeNotesOfType are the notes of project of the type typ, by path,
// at most officeMaxPalette.
func (r *Router) officeNotesOfType(project, typ string) []*vault.Note {
	if r.deps.Index == nil {
		return nil
	}
	hits, _, err := r.deps.Index.Query(index.QueryOptions{Projects: []string{project},
		Where: []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{typ}}}, Sort: "path", Limit: officeMaxPalette})
	if err != nil {
		return nil
	}
	var out []*vault.Note
	for _, h := range hits {
		if n, err := r.deps.Vault.Load(h.Path); err == nil {
			out = append(out, n)
		}
	}
	return out
}
