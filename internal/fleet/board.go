package fleet

// A board (IMP-216) is a note `type: board` with one ```board block of
// YAML: its columns in order, each bound to a state of the tasks and with
// its limit of cards at work (WIP), how hard the limits are for agents and
// for people, and who closes a new card. Its cards are tasks of the
// project with board: the board's note, a column and a rank; a card needs
// no team, and an agent in a session or a person works on it.

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

// BoardLang is the fence of a board's block, BoardType the type of its
// note.
const (
	BoardLang = "board"
	BoardType = "board"
)

// How hard the limit of a column is for who writes a card into it: hard
// refuses the card, warn writes it and says so.
const (
	WIPHard = "hard"
	WIPWarn = "warn"
)

// Who closes a card: its agent, or a person after it went to review.
const (
	DoneAgent  = "agent"
	DonePerson = "person"
)

// maxColumns is the most columns of a board.
const maxColumns = 20

// Board is a board as its note declares it.
type Board struct {
	// Path is the board's note.
	Path string `json:"path"`
	// Columns in the order the board shows them.
	Columns []Column `json:"columns"`
	WIP     Rigidity `json:"wip"`
	// Done is who closes a new card, DoneAgent or DonePerson.
	Done string `json:"done"`
}

// Column is one column of a board: its cards are in Status. WIP is the
// most cards it holds, 0 for no limit.
type Column struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	WIP    int    `json:"wip,omitempty"`
}

// Rigidity is how hard the limits of a board are, WIPHard or WIPWarn, for
// agents and for people.
type Rigidity struct {
	Agents string `json:"agents"`
	People string `json:"people"`
}

// FindBoard returns the board blocks of a note's body.
func FindBoard(body []byte) []Block { return findBlocks(body, BoardLang) }

// maxBoardText is the most of a board's text an agent receives with a card.
const maxBoardText = 8 << 10

// BoardText is what an agent reads about a board with each card it takes:
// the text of its note without the frontmatter and the board and view
// blocks, which say the columns and show the cards. Whoever writes the
// board says there, in their own words, what done means and how the work
// goes. A long one is cut.
func BoardText(content []byte) string {
	body := []byte(parser.BodyAfterFrontmatter(content))
	var spans []parser.FencedBlock
	for _, lang := range []string{BoardLang, "view"} {
		spans = append(spans, parser.FindFenced(body, lang)...)
	}
	slices.SortFunc(spans, func(a, b parser.FencedBlock) int { return a.Start - b.Start })
	var b strings.Builder
	at := 0
	for _, f := range spans {
		b.Write(body[at:f.Start])
		at = f.End
	}
	b.Write(body[at:])
	text := strings.TrimSpace(blankRuns.ReplaceAllString(b.String(), "\n\n"))
	if len(text) > maxBoardText {
		cut := strings.LastIndexAny(text[:maxBoardText], " \n")
		if cut < maxBoardText/2 {
			cut = maxBoardText
		}
		text = strings.ToValidUTF8(text[:cut], "") + "\n\n(cut: read the board's note)"
	}
	return text
}

// blankRuns are the blank lines a block taken out leaves.
var blankRuns = regexp.MustCompile(`\n{3,}`)

type rawColumn struct {
	Name   string   `yaml:"name"`
	Status string   `yaml:"status"`
	WIP    *float64 `yaml:"wip"` // a float, so that 1.5 is refused, not read as 1
}

type rawBoard struct {
	Columns yaml.Node `yaml:"columns"`
	WIP     struct {
		Agents string `yaml:"agents"`
		People string `yaml:"people"`
	} `yaml:"wip"`
	Done string `yaml:"done"`
}

// ParseBoard reads a block of the board note at notePath. A block that
// does not read still gives what it could, with its problems.
func ParseBoard(src, notePath string) (Board, []Problem) {
	b := Board{Path: notePath, WIP: Rigidity{Agents: WIPHard, People: WIPWarn}, Done: DoneAgent}
	var r rawBoard
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && err.Error() != "EOF" {
		return b, []Problem{yamlProblem(err, "a board: columns, wip, done")}
	}
	var problems []Problem
	bad := func(line int, format string, args ...any) {
		problems = append(problems, Problem{Line: line, Msg: fmt.Sprintf(format, args...)})
	}
	rigidity := func(who, v string, to *string) {
		switch v = strings.TrimSpace(v); v {
		case "":
		case WIPHard, WIPWarn:
			*to = v
		default:
			bad(0, "wip %s %q: hard or warn", who, v)
		}
	}
	rigidity("agents", r.WIP.Agents, &b.WIP.Agents)
	rigidity("people", r.WIP.People, &b.WIP.People)
	switch d := strings.TrimSpace(r.Done); d {
	case "":
	case DoneAgent, DonePerson:
		b.Done = d
	default:
		bad(0, "done %q: agent or person", d)
	}
	columns, cp := parseColumns(&r.Columns)
	problems = append(problems, cp...)
	b.Columns = columns
	if len(cp) == 0 {
		switch {
		case len(b.Columns) == 0:
			bad(0, "columns: a board has its columns, one for submitted and one for working at least")
		case !b.has(Submitted):
			bad(0, "columns: none for submitted, where an agent takes the next card")
		case !b.has(Working):
			bad(0, "columns: none for working, where the card an agent takes goes")
		case b.Done == DonePerson && !b.has(Review):
			bad(0, "done person: the cards a person closes wait in a column for review, and the board has none")
		}
	}
	return b, problems
}

// parseColumns reads the columns list in its order.
func parseColumns(n *yaml.Node) ([]Column, []Problem) {
	if n.Kind == 0 {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, []Problem{{Line: n.Line, Msg: "columns: a list of columns, each {name, status, wip}"}}
	}
	var columns []Column
	var problems []Problem
	if len(n.Content) > maxColumns {
		problems = append(problems, Problem{Line: n.Line, Msg: fmt.Sprintf("columns: at most %d", maxColumns)})
	}
	for _, v := range n.Content {
		bad := func(format string, args ...any) {
			problems = append(problems, Problem{Line: v.Line, Msg: fmt.Sprintf(format, args...)})
		}
		var rc rawColumn
		if err := decodeKnown(v, &rc); err != nil {
			p := yamlProblem(err, "a column: name, status, wip")
			p.Line = v.Line // decodeKnown counts from the column's own first line
			problems = append(problems, p)
			continue
		}
		c := Column{Name: strings.TrimSpace(rc.Name), Status: strings.TrimSpace(rc.Status)}
		switch {
		case c.Name == "":
			bad("a column has its name")
			continue
		case len(c.Name) > 64:
			bad("column %s: a name of at most 64 bytes", c.Name)
			continue
		case !Known(c.Status):
			bad("column %s: status %q is none of backlog, %s", c.Name, c.Status, strings.Join(States, ", "))
			continue
		}
		if rc.WIP != nil {
			if w := *rc.WIP; w < 1 || w != math.Trunc(w) || w > math.MaxInt32 {
				bad("column %s: wip %s, the most cards it holds, is a whole number from 1 (none for no limit)", c.Name, strconv.FormatFloat(w, 'f', -1, 64))
				continue
			}
			c.WIP = int(*rc.WIP)
		}
		if _, dup := (Board{Columns: columns}).Column(c.Name); dup {
			bad("column %s: written twice", c.Name)
			continue
		}
		columns = append(columns, c)
	}
	return columns, problems
}

// has says whether b has a column of status.
func (b Board) has(status string) bool {
	_, ok := b.First(status)
	return ok
}

// Column returns the column named name, in any case.
func (b Board) Column(name string) (Column, bool) {
	name = strings.TrimSpace(name)
	for _, c := range b.Columns {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Column{}, false
}

// First returns the first column of status.
func (b Board) First(status string) (Column, bool) {
	for _, c := range b.Columns {
		if c.Status == status {
			return c, true
		}
	}
	return Column{}, false
}

// Hard says whether the limits of b refuse a card: to a person when
// person, else to an agent.
func (b Board) Hard(person bool) bool {
	if person {
		return b.WIP.People == WIPHard
	}
	return b.WIP.Agents == WIPHard
}

// Target is where a card of b goes from where it is (cur). To column,
// whose state it takes, when one is named; else to status, staying in its
// column when that is one of status, or going to the first column of
// status. With neither, it stays, its column made to agree with its state
// (a note changed by hand). A closed state may have no column: the card
// then has none, and the board shows it among the closed. A column the
// board has not, a column and a state that disagree, and an open state
// without a column are refused.
func (b Board) Target(cur Task, column, status string) (Column, error) {
	if column != "" {
		c, ok := b.Column(column)
		if !ok {
			return Column{}, fmt.Errorf("the board %s has no column %s: %s", b.Path, column, b.names())
		}
		if status != "" && status != c.Status {
			return Column{}, fmt.Errorf("the column %s is %s, not %s", c.Name, c.Status, status)
		}
		return c, nil
	}
	if status == "" {
		status = cur.Status
	}
	if c, ok := b.Column(cur.Column); ok && c.Status == status {
		return c, nil
	}
	if c, ok := b.First(status); ok {
		return c, nil
	}
	if Ended(status) {
		return Column{Status: status}, nil
	}
	return Column{}, fmt.Errorf("the board %s has no column for %s: %s", b.Path, status, b.names())
}

// names are b's columns with their states, for an error.
func (b Board) names() string {
	var out []string
	for _, c := range b.Columns {
		out = append(out, c.Name+" ("+c.Status+")")
	}
	return strings.Join(out, ", ")
}

// ColumnOf is the column a card of b is in, from its state when its
// column disagrees: "" for a closed card without one, or a card whose
// state has no column.
func (b Board) ColumnOf(t Task) string {
	c, err := b.Target(t, "", "")
	if err != nil {
		return ""
	}
	return c.Name
}

// Count is the cards of b among cards in its column column, but the one at
// except.
func (b Board) Count(cards []Task, column, except string) int {
	n := 0
	for _, t := range cards {
		if t.Board == b.Path && t.Path != except && strings.EqualFold(b.ColumnOf(t), column) {
			n++
		}
	}
	return n
}

// Next is the card an agent takes from b: in b's columns of submitted, in
// their order, the first by rank that is ready (Waits, against the tasks
// among cards) and has no team, which a team's run would take.
func (b Board) Next(cards []Task, now time.Time) (Task, bool) {
	byPath := make(map[string]Task, len(cards))
	for _, t := range cards {
		byPath[t.Path] = t
	}
	for _, c := range b.Columns {
		if c.Status != Submitted {
			continue
		}
		var in []Task
		for _, t := range cards {
			if t.Board == b.Path && t.Team == "" && strings.EqualFold(b.ColumnOf(t), c.Name) {
				in = append(in, t)
			}
		}
		slices.SortFunc(in, Order)
		for _, t := range in {
			if t.Waits(byPath, now) == "" {
				return t, true
			}
		}
	}
	return Task{}, false
}

// CardMove is a person's move of a card, from the web UI: to Column, or
// to the column of Status; to place At in it (0 the top), or to its
// bottom when the card enters it. IfMatch is the etag of the note the
// person saw, Check a test of the card's content in the same write (a
// field the person saw has changed): either refuses the move.
type CardMove struct {
	Column  string
	Status  string
	At      *int
	IfMatch string
	Check   func(content []byte) error
}

// MoveError is a move of a card refused: by the board's rules, or Stale
// when the note changed since the etag the person saw.
type MoveError struct {
	Msg   string
	Stale bool
}

func (e *MoveError) Error() string { return e.Msg }

// Full says whether c, holding n cards, takes no more.
func (c Column) Full(n int) bool { return c.WIP > 0 && n >= c.WIP }

// Order sorts the cards of a column: by rank, the smaller first, then the
// cards without one (rank 0), each by path, which starts with the time it
// was created.
func Order(a, b Task) int {
	switch {
	case a.Rank > 0 && b.Rank > 0 && a.Rank != b.Rank:
		return cmp.Compare(a.Rank, b.Rank)
	case a.Rank > 0 && b.Rank <= 0:
		return -1
	case a.Rank <= 0 && b.Rank > 0:
		return 1
	}
	return strings.Compare(a.Path, b.Path)
}

// minGap is the least room between the ranks of two cards: a card put
// between two closer ones renumbers its column, so a rank keeps a few
// digits.
const minGap = 1.0 / 1024

// Place is the rank of a card put at position at (0 the top) of a column
// whose other cards, in order, have ranks: halfway between its neighbours,
// halfway to 0 at the top, one more than the last at the bottom. When the
// room ran out, or a card of the column has no rank, the column is
// renumbered 1, 2, 3… with the card among them: others are then the new
// ranks of the other cards, in their order, and nil when they keep theirs.
func Place(ranks []float64, at int) (rank float64, others []float64) {
	at = max(0, min(at, len(ranks)))
	renumber := false
	for _, r := range ranks {
		if r <= 0 {
			renumber = true
		}
	}
	if !renumber {
		switch {
		case len(ranks) == 0:
			return 1, nil
		case at == len(ranks):
			return ranks[at-1] + 1, nil
		}
		lo := 0.0
		if at > 0 {
			lo = ranks[at-1]
		}
		if hi := ranks[at]; (hi-lo)/2 >= minGap {
			return lo + (hi-lo)/2, nil
		}
	}
	others = make([]float64, len(ranks))
	for i := range ranks {
		others[i] = float64(i + 1)
		if i >= at {
			others[i]++
		}
	}
	return float64(at + 1), others
}
