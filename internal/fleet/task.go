package fleet

import (
	"bytes"
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/parser"
)

// The states of a task: A2A's, and review, the work done that waits for a
// person (A2A says input-required for it).
const (
	Submitted     = "submitted"
	Working       = "working"
	InputRequired = "input-required"
	Review        = "review"
	Completed     = "completed"
	Failed        = "failed"
	Canceled      = "canceled"
	// Backlog is a board's card not ready yet (IMP-216): no team's task
	// is in it, and nothing takes it before it is submitted.
	Backlog = "backlog"
)

// States are the states of a team's task, in the order a board shows them.
var States = []string{Submitted, Working, InputRequired, Review, Completed, Failed, Canceled}

// Known says whether s is a state of a task or of a card.
func Known(s string) bool { return s == Backlog || slices.Contains(States, s) }

// Ended says whether a task in state s is closed.
func Ended(s string) bool { return s == Completed || s == Failed || s == Canceled }

// Folder is the folder of a project's tasks, and Database the database
// note whose rows they are.
const (
	Folder   = "tasks"
	Database = "tasks.md"
)

// The frontmatter keys the server keeps on a task.
const (
	KeyStatus     = "status"
	KeyClaimedBy  = "claimed_by"
	KeyLeaseUntil = "lease_until"
	KeyAttempts   = "attempts"
	KeyCostUSD    = "cost_usd"
	KeyTokens     = "tokens"
	KeyUpdated    = "updated"
	// KeyRounds counts the coordinator's runs on an objective after its
	// sub-tasks closed; KeyChildrenState is the state of the sub-tasks
	// the last of them saw.
	KeyRounds        = "rounds"
	KeyChildrenState = "children_state"
	// KeyBranch is the branch of the repository an attempt sent back left
	// its work on: the next attempt continues it.
	KeyBranch = "branch"
	// A card's (IMP-216): its column, its rank in it, and when it entered
	// it.
	KeyColumn      = "column"
	KeyRank        = "rank"
	KeyColumnSince = "column_since"
	// And, for the board's flow (phase 2), when it first entered a column
	// of working and when it closed; its iteration, a note type: iteration.
	KeyStarted   = "started"
	KeyFinished  = "finished"
	KeyIteration = "iteration"
)

// Task is a task as its note's frontmatter says it.
type Task struct {
	Path      string   `json:"path"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Team      string   `json:"team,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Parent    string   `json:"parent,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Forbid    []string `json:"forbid,omitempty"`
	BudgetUSD float64  `json:"budget_usd,omitempty"`
	CostUSD   float64  `json:"cost_usd,omitempty"`
	Tokens    int64    `json:"tokens,omitempty"`
	// ClaimedBy is the run that holds it ("run-<id>"), or the token of an
	// outside agent; LeaseUntil when an outside agent's hold ends.
	ClaimedBy  string    `json:"claimed_by,omitempty"`
	LeaseUntil time.Time `json:"lease_until,omitzero"`
	Attempts   int       `json:"attempts,omitempty"`
	// Rounds and ChildrenState are an objective's (KeyRounds).
	Rounds        int    `json:"rounds,omitempty"`
	ChildrenState string `json:"children_state,omitempty"`
	// Branch is the branch the next attempt continues (KeyBranch).
	Branch string `json:"branch,omitempty"`
	// A card's (IMP-216): its board's note, its column, its rank in it (0
	// for none), when it entered it, who closes it (DoneAgent or
	// DonePerson, "" for its board's default), and the notes it is for.
	Board       string   `json:"board,omitempty"`
	Column      string   `json:"column,omitempty"`
	Rank        float64  `json:"rank,omitempty"`
	ColumnSince string   `json:"column_since,omitempty"`
	DoneBy      string   `json:"done_by,omitempty"`
	For         []string `json:"for,omitempty"`
	Created     string   `json:"created,omitempty"`
	Started     string   `json:"started,omitempty"`
	Finished    string   `json:"finished,omitempty"`
	Iteration   string   `json:"iteration,omitempty"`
}

// IsTask says whether a note's raw frontmatter is a task's.
func IsTask(raw string) bool {
	return strings.TrimSpace(fmt.Sprint(parser.ParseFrontmatterFields(raw)["type"])) == TaskType
}

// ReadTask reads the task of the note at p from its content.
func ReadTask(p string, content []byte) Task {
	project, _, _ := strings.Cut(p, "/")
	fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(p, content))
	str := func(k string) string {
		s, _ := fm[k].(string)
		return strings.TrimSpace(s)
	}
	list := func(k string) []string {
		switch v := fm[k].(type) {
		case []string:
			return trimList(v)
		case string:
			return trimList([]string{v})
		}
		return nil
	}
	t := Task{Path: p, Title: str("title"), Status: str(KeyStatus), Team: LinkPath(str("team"), project),
		Assignee: str("assignee"), Parent: LinkPath(str("parent"), project), Paths: list("paths"), Forbid: list("forbid"),
		ClaimedBy: str(KeyClaimedBy), ChildrenState: str(KeyChildrenState), Branch: str(KeyBranch), Created: str("created"),
		Board: LinkPath(str("board"), project), Column: str(KeyColumn), ColumnSince: str(KeyColumnSince), DoneBy: str("done_by"),
		Started: str(KeyStarted), Finished: str(KeyFinished), Iteration: LinkPath(str(KeyIteration), project)}
	for _, d := range list("depends_on") {
		t.DependsOn = append(t.DependsOn, LinkPath(d, project))
	}
	for _, f := range list("for") {
		t.For = append(t.For, LinkPath(f, project))
	}
	t.Rank = number(str(KeyRank))
	t.BudgetUSD = number(str("budget_usd"))
	t.CostUSD = number(str(KeyCostUSD))
	t.Tokens = int64(number(str(KeyTokens)))
	t.Attempts = int(number(str(KeyAttempts)))
	t.Rounds = int(number(str(KeyRounds)))
	if s := str(KeyLeaseUntil); s != "" {
		if at, err := time.Parse(time.RFC3339, s); err == nil {
			t.LeaseUntil = at
		}
	}
	if t.Title == "" {
		t.Title = strings.TrimSuffix(path.Base(p), ".md")
	}
	return t
}

// number reads a non-negative finite number, 0 for anything else.
func number(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// NewTask is a task to write.
type NewTask struct {
	Title     string
	Body      string
	Team      string // the team's note; "" for a board's card
	Assignee  string
	Parent    string
	DependsOn []string
	Paths     []string
	Forbid    []string
	BudgetUSD float64
	// Status is Submitted, or InputRequired for a task that waits for a
	// person before it starts; a card's is its column's.
	Status string
	// A card's (IMP-216): its board's note, its column, its rank, who
	// closes it, the notes it is for and its iteration's note.
	Board     string
	Column    string
	Rank      float64
	DoneBy    string
	For       []string
	Iteration string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// TaskPath is the path of a new task of project, named after its time and
// title: tasks/AAAAMMGG-HHMMSS-<slug>.md.
func TaskPath(project, title string, at time.Time) string {
	slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	if slug == "" {
		slug = "task"
	}
	return project + "/" + Folder + "/" + at.Format("20060102-150405") + "-" + slug + ".md"
}

// Render is the note of a new task, created at.
func (n NewTask) Render(project string, at time.Time) []byte {
	var b bytes.Buffer
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(n.Title))
	fmt.Fprintf(&b, "type: %s\n", TaskType)
	status := n.Status
	if status == "" {
		status = Submitted
	}
	fmt.Fprintf(&b, "status: %s\n", status)
	if n.Team != "" {
		fmt.Fprintf(&b, "team: %s\n", yamlString(Link(n.Team)))
		fmt.Fprintf(&b, "assignee: %s\n", n.Assignee)
	}
	if n.Board != "" {
		fmt.Fprintf(&b, "board: %s\n", yamlString(Link(n.Board)))
		fmt.Fprintf(&b, "%s: %s\n", KeyColumn, yamlString(n.Column))
		fmt.Fprintf(&b, "%s: %s\n", KeyColumnSince, at.Format(time.RFC3339))
		if n.Rank > 0 {
			fmt.Fprintf(&b, "%s: %s\n", KeyRank, strconv.FormatFloat(n.Rank, 'f', -1, 64))
		}
	}
	if n.DoneBy != "" {
		fmt.Fprintf(&b, "done_by: %s\n", n.DoneBy)
	}
	if n.Iteration != "" {
		fmt.Fprintf(&b, "%s: %s\n", KeyIteration, yamlString(Link(n.Iteration)))
	}
	if n.Parent != "" {
		fmt.Fprintf(&b, "parent: %s\n", yamlString(Link(n.Parent)))
	}
	writeList := func(k string, xs []string, link bool) {
		if len(xs) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", k)
		for _, x := range xs {
			if link {
				x = Link(x)
			}
			fmt.Fprintf(&b, "  - %s\n", yamlString(x))
		}
	}
	writeList("depends_on", n.DependsOn, true)
	writeList("for", n.For, true)
	writeList("paths", n.Paths, false)
	writeList("forbid", n.Forbid, false)
	if n.BudgetUSD > 0 {
		fmt.Fprintf(&b, "budget_usd: %s\n", strconv.FormatFloat(n.BudgetUSD, 'f', -1, 64))
	}
	fmt.Fprintf(&b, "created: %s\n", at.Format(time.RFC3339))
	fmt.Fprintf(&b, "tags: [%s, type:task]\n", project)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(n.Title))
	if body := strings.TrimSpace(n.Body); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.Bytes()
}

// yamlString quotes s when YAML would not read it back as the same text.
func yamlString(s string) string {
	if s == "" || strings.ContainsAny(s, ":#[]{},&*!|>'\"%@`") || strings.TrimSpace(s) != s {
		return strconv.Quote(s)
	}
	return s
}

// Set writes the keys of set on a task's content and removes those of
// unset, line by line: the rest of the note stays as written.
func Set(content []byte, set map[string]any, unset ...string) ([]byte, error) {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	fields := make([]frontmatter.Field, 0, len(keys))
	for _, k := range keys {
		fields = append(fields, frontmatter.Field{Key: k, Value: set[k]})
	}
	return frontmatter.SetKeys(content, fields, unset)
}

// AppendSection adds a section ## heading with text at the end of a
// task's note. The text's own headings go two levels down (# to ###, at
// most ######), but in its code blocks: an agent's report with headings
// stays inside its section, where a reader that stops at the next ## finds
// all of it (the fitness pilot, 2026-10-25: a coordinator read its
// reviewer's reports as empty).
func AppendSection(content []byte, heading, text string) []byte {
	out := bytes.TrimRight(content, "\n")
	out = append(out, "\n\n## "...)
	out = append(out, heading...)
	out = append(out, "\n\n"...)
	out = append(out, demoteHeadings(strings.TrimSpace(text))...)
	out = append(out, '\n')
	return out
}

// demoteHeadings puts the headings of text two levels down, but in its
// code blocks.
func demoteHeadings(text string) string {
	lines := strings.Split(text, "\n")
	var f parser.Fences
	for i, line := range lines {
		if f.Code(line) {
			continue
		}
		if n := headingLevel(line); n > 0 {
			lines[i] = strings.Repeat("#", min(n+2, 6)) + line[n:]
		}
	}
	return strings.Join(lines, "\n")
}

// headingLevel is the level of an ATX heading line, as the parser reads it
// (one to six # and a space), or 0.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' && n < 6 {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// LastSection is the text of the last section ## heading of a task's note,
// up to the next heading of level 1 or 2, but in code blocks; "" when there
// is none. A task sent again closes again: its last report is the one.
func LastSection(content []byte, heading string) string {
	lines := strings.Split(parser.BodyAfterFrontmatter(content), "\n")
	var f parser.Fences
	start, end := -1, len(lines)
	for i, line := range lines {
		if f.Code(line) {
			continue
		}
		n := headingLevel(line)
		if n == 0 || n > 2 {
			continue
		}
		if n == 2 && strings.TrimSpace(line[n:]) == heading {
			start, end = i, len(lines)
		} else if start >= 0 && end == len(lines) {
			end = i
		}
	}
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
}

// Moves are the states a task may go to from each: the server's moves and
// the people's (canceled, submitted again). A card of a board leaves the
// backlog when it is ready, and goes back to it before it starts.
var moves = map[string][]string{
	Backlog:       {Submitted, Canceled},
	Submitted:     {Working, InputRequired, Canceled, Backlog},
	Working:       {Submitted, InputRequired, Review, Completed, Failed, Canceled},
	InputRequired: {Submitted, Working, Canceled, Failed},
	Review:        {Working, Submitted, Completed, Failed, Canceled},
	Completed:     {},
	Failed:        {Submitted},
	Canceled:      {Submitted},
}

// CanMove says whether a task may go from state from to state to.
func CanMove(from, to string) bool {
	return slices.Contains(moves[from], to)
}

// Waits says why a task in submitted does not start: a dependency not
// completed, or its hold not ended; "" when it is ready. deps are the
// tasks it depends on, by path; one missing waits too.
func (t Task) Waits(deps map[string]Task, now time.Time) string {
	if t.Status != Submitted {
		return "status " + t.Status
	}
	if !t.LeaseUntil.IsZero() && now.Before(t.LeaseUntil) {
		return "held by " + t.ClaimedBy + " until " + t.LeaseUntil.Format(time.RFC3339)
	}
	for _, d := range t.DependsOn {
		dt, ok := deps[d]
		switch {
		case !ok:
			return "waits for " + d + ", which is no task"
		case dt.Status != Completed:
			return "waits for " + d + " (" + dt.Status + ")"
		}
	}
	return ""
}

// Moves are the states a holder moves its task to while it works on it;
// Ends those it closes it with.
var (
	Moves = []string{Working, InputRequired, Review, Submitted}
	Ends  = []string{Completed, Failed, Canceled}
)

// Update moves t, held by holder, to status (one of Moves), adding message
// as a section dated at (in the notes' zone): back to submitted, the
// holder lets it go.
func Update(t Task, content []byte, holder, status, message string, at time.Time) ([]byte, error) {
	if err := held(t, holder); err != nil {
		return nil, err
	}
	if !slices.Contains(Moves, status) {
		return nil, fmt.Errorf("status %q: %s (closing a task completes it)", status, strings.Join(Moves, ", "))
	}
	if t.Status != status && !CanMove(t.Status, status) {
		return nil, fmt.Errorf("the task is %s: it does not go to %s", t.Status, status)
	}
	var unset []string
	if status == Submitted {
		unset = []string{KeyClaimedBy, KeyLeaseUntil}
	}
	out, err := Set(content, map[string]any{KeyStatus: status, KeyUpdated: at.Format(time.RFC3339)}, unset...)
	if err != nil {
		return nil, err
	}
	if m := strings.TrimSpace(message); m != "" {
		out = AppendSection(out, at.Format("2006-01-02 15:04")+" — "+status, m)
	}
	return out, nil
}

// Complete closes t, held by holder, as status (one of Ends) with its
// report: the section Report, which the tasks after it and the coordinator
// read. No attempt follows: the branch to continue goes.
func Complete(t Task, content []byte, holder, status, report string, at time.Time) ([]byte, error) {
	if err := held(t, holder); err != nil {
		return nil, err
	}
	if !slices.Contains(Ends, status) {
		return nil, fmt.Errorf("status %q: %s", status, strings.Join(Ends, ", "))
	}
	if !CanMove(t.Status, status) {
		return nil, fmt.Errorf("the task is %s: it does not close as %s", t.Status, status)
	}
	out, err := Set(content, map[string]any{KeyStatus: status, KeyUpdated: at.Format(time.RFC3339)}, KeyClaimedBy, KeyLeaseUntil, KeyBranch)
	if err != nil {
		return nil, err
	}
	r := strings.TrimSpace(report)
	if r == "" {
		r = "(no report)"
	}
	r += fmt.Sprintf("\n\n%s by %s at %s, attempt %d, %s USD, %d tokens.", status, holder, at.Format("2006-01-02 15:04"), t.Attempts,
		strconv.FormatFloat(t.CostUSD, 'f', -1, 64), t.Tokens)
	return AppendSection(out, "Report", r), nil
}

// LeaseOver says whether an outside agent held t past its lease at now.
func (t Task) LeaseOver(now time.Time) bool {
	return t.ClaimedBy != "" && !t.LeaseUntil.IsZero() && now.After(t.LeaseUntil) && !Ended(t.Status)
}

// AfterLease is the state of t given back after its lease: submitted for
// another attempt, failed at the max-th.
func AfterLease(t Task, max int) string {
	if t.Attempts >= max {
		return Failed
	}
	return Submitted
}

// GiveBack gives back t, which its holder kept past its lease, at at (in
// the notes' zone): to AfterLease's state, without its holder, with a dated
// section that says so.
func GiveBack(t Task, content []byte, max int, at time.Time) ([]byte, error) {
	out, err := Set(content, map[string]any{KeyStatus: AfterLease(t, max), KeyUpdated: at.Format(time.RFC3339)}, KeyClaimedBy, KeyLeaseUntil)
	if err != nil {
		return nil, err
	}
	return AppendSection(out, at.Format("2006-01-02 15:04")+" — lease ended",
		fmt.Sprintf("%s held the task until %s without a heartbeat (attempt %d of %d).", t.ClaimedBy,
			t.LeaseUntil.In(at.Location()).Format(time.RFC3339), t.Attempts, max)), nil
}

// ToReview sends t, held by holder, to review with its report, for a
// person to close: the card of a board that a person closes (DonePerson).
// The holder lets it go.
func ToReview(t Task, content []byte, holder, report string, at time.Time) ([]byte, error) {
	if err := held(t, holder); err != nil {
		return nil, err
	}
	if !CanMove(t.Status, Review) {
		return nil, fmt.Errorf("the task is %s: it does not go to review", t.Status)
	}
	out, err := Set(content, map[string]any{KeyStatus: Review, KeyUpdated: at.Format(time.RFC3339)}, KeyClaimedBy, KeyLeaseUntil)
	if err != nil {
		return nil, err
	}
	r := strings.TrimSpace(report)
	if r == "" {
		r = "(no report)"
	}
	r += fmt.Sprintf("\n\nDone by %s at %s; a person closes it.", holder, at.Format("2006-01-02 15:04"))
	return AppendSection(out, "Report", r), nil
}

// held checks that holder holds t.
func held(t Task, holder string) error {
	if t.ClaimedBy != holder {
		by := t.ClaimedBy
		if by == "" {
			by = "nobody"
		}
		return fmt.Errorf("the task %s is held by %s, not by %s", t.Path, by, holder)
	}
	return nil
}
