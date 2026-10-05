// Package automation runs the rules a database note declares under
// `automations:` (IMP-127 iteration 3, phase 4): actions at a time, never
// on a write, so no rule can set another off.
//
//	automations:
//	  - name: Deadlines near
//	    due: due                 # a date field of the rows
//	    before: 3                # days ahead (0, the default: on the day)
//	    where:
//	      - status in [open, in-progress]
//	    handoff: myproject-dev   # a handoff to this agent, listing the rows
//	  - name: Monday backlog
//	    every: monday 09:00      # or "day 09:00"
//	    snapshot: myproject/hot.md
//
// A due rule fires once for each row whose date comes within `before` days
// (a row past its date too), and again only if the date changes; an every
// rule fires once per slot, the first slot after it was first seen. The
// actions write as the server's own identity, `automation`, inside the
// project of the database note, and the audit log says so.
package automation

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MaxRules caps the rules of a database note.
const MaxRules = 16

// MaxBefore caps how many days ahead a due rule looks.
const MaxBefore = 366

// Rule is one entry of a database note's automations.
type Rule struct {
	Name string `json:"name"`
	// Due is the date field of the rows a due rule watches; Every the slots
	// of an every rule. Exactly one is set.
	Due    string    `json:"due,omitempty"`
	Before int       `json:"before,omitempty"`
	Every  *Schedule `json:"every,omitempty"`
	// Where are conditions of a view on the rows, as written ("status in
	// [open, in-progress]"): a due rule fires for the rows that meet them.
	Where []string `json:"where,omitempty"`
	// Handoff is the agent a handoff goes to; Snapshot the note an every
	// rule freezes. Exactly one is set, and a due rule hands off.
	Handoff  string `json:"handoff,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
	// Message is the handoff's text: for a due rule it comes before the
	// rows, for an every rule it is the handoff.
	Message string `json:"message,omitempty"`
}

// Key names the rule in the engine's state: its database note and its
// name. A rule renamed is a new rule.
func (r Rule) Key(database string) string { return database + "#" + r.Name }

// Trigger says when the rule fires, for a person: "due - 3 days",
// "every monday 09:00".
func (r Rule) Trigger() string {
	if r.Every != nil {
		return "every " + r.Every.String()
	}
	if r.Before == 0 {
		return r.Due + " (on the day)"
	}
	return fmt.Sprintf("%s - %d days", r.Due, r.Before)
}

// Action says what the rule does, for a person.
func (r Rule) Action() string {
	if r.Snapshot != "" {
		return "snapshot of " + r.Snapshot
	}
	return "handoff to " + r.Handoff
}

type rawRule struct {
	Name     string `yaml:"name"`
	Due      string `yaml:"due"`
	Before   any    `yaml:"before"`
	Every    string `yaml:"every"`
	Where    []any  `yaml:"where"`
	Handoff  string `yaml:"handoff"`
	Snapshot string `yaml:"snapshot"`
	Message  string `yaml:"message"`
}

var ruleKeys = map[string]bool{"name": true, "due": true, "before": true, "every": true, "where": true, "handoff": true, "snapshot": true, "message": true}

// ErrNone is returned by Parse for a note that declares no automations.
var ErrNone = errors.New("no automations")

// Parse reads the automations of the database note at notePath from its
// frontmatter. The rules that are well formed come back with an error for
// each of the others, so one mistake leaves the rest running.
func Parse(notePath, frontmatter string) ([]Rule, []error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &doc); err != nil {
		return nil, []error{fmt.Errorf("frontmatter: %w", err)}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, []error{ErrNone}
	}
	m := doc.Content[0]
	var node *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "automations" {
			node = m.Content[i+1]
		}
	}
	if node == nil || node.Kind == yaml.ScalarNode && node.Value == "" {
		return nil, []error{ErrNone}
	}
	if node.Kind != yaml.SequenceNode {
		return nil, []error{errors.New("automations: want a list of rules")}
	}
	project, _, _ := strings.Cut(notePath, "/")
	var rules []Rule
	var errs []error
	seen := map[string]bool{}
	for i, n := range node.Content {
		if i >= MaxRules {
			errs = append(errs, fmt.Errorf("automations: at most %d rules, the others are ignored", MaxRules))
			break
		}
		r, err := parseRule(project, n)
		if err == nil && seen[r.Name] {
			err = fmt.Errorf("the name %q is taken by another rule", r.Name)
		}
		if err != nil {
			label := fmt.Sprintf("rule %d", i+1)
			if r.Name != "" {
				label = fmt.Sprintf("rule %q", r.Name)
			}
			errs = append(errs, fmt.Errorf("automations: %s: %w", label, err))
			continue
		}
		seen[r.Name] = true
		rules = append(rules, r)
	}
	return rules, errs
}

func parseRule(project string, n *yaml.Node) (Rule, error) {
	if n.Kind != yaml.MappingNode {
		return Rule{}, errors.New("want a map (name, due or every, handoff or snapshot)")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !ruleKeys[k] {
			return Rule{}, fmt.Errorf("unknown key %q (name, due, before, every, where, handoff, snapshot, message)", k)
		}
	}
	var raw rawRule
	if err := n.Decode(&raw); err != nil {
		return Rule{}, err
	}
	r := Rule{
		Name:     strings.TrimSpace(raw.Name),
		Due:      strings.TrimSpace(raw.Due),
		Handoff:  strings.TrimSpace(raw.Handoff),
		Snapshot: strings.Trim(strings.TrimSpace(raw.Snapshot), "/"),
		Message:  strings.TrimSpace(raw.Message),
	}
	if r.Name == "" {
		return r, errors.New("name is required")
	}
	for _, w := range raw.Where {
		s, ok := w.(string)
		if !ok {
			return r, fmt.Errorf("where: %v: write each condition as a view's, \"field op value\"", w)
		}
		r.Where = append(r.Where, s)
	}
	every := strings.TrimSpace(raw.Every)
	switch {
	case r.Due == "" && every == "":
		return r, errors.New("say when: due (a date field of the rows) or every (\"monday 09:00\", \"day 09:00\")")
	case r.Due != "" && every != "":
		return r, errors.New("due and every: one or the other")
	case every != "":
		s, err := ParseSchedule(every)
		if err != nil {
			return r, err
		}
		r.Every = &s
		if raw.Before != nil {
			return r, errors.New("before goes with due, not every")
		}
		if len(r.Where) > 0 {
			return r, errors.New("where goes with due: an every rule has no rows")
		}
	default:
		b, err := days(raw.Before)
		if err != nil {
			return r, err
		}
		r.Before = b
	}
	switch {
	case r.Handoff == "" && r.Snapshot == "":
		return r, errors.New("say what: handoff (an agent) or snapshot (a note)")
	case r.Handoff != "" && r.Snapshot != "":
		return r, errors.New("handoff and snapshot: one or the other")
	case r.Snapshot != "" && r.Due != "":
		return r, errors.New("a due rule hands off its rows; snapshot goes with every")
	case r.Snapshot != "":
		if !strings.HasPrefix(r.Snapshot, project+"/") {
			r.Snapshot = project + "/" + r.Snapshot
		}
		if path.Ext(r.Snapshot) == "" {
			r.Snapshot += ".md"
		}
		if path.Ext(r.Snapshot) != ".md" || strings.Contains(r.Snapshot, "..") {
			return r, fmt.Errorf("snapshot: %q is not a markdown note of the project", r.Snapshot)
		}
	}
	if r.Handoff != "" && !slugRe(r.Handoff) {
		return r, fmt.Errorf("handoff: %q is not an agent's slug (letters, digits, - and _)", r.Handoff)
	}
	return r, nil
}

// days reads before: a number of days, 3 or "3d".
func days(v any) (int, error) {
	var n int
	switch t := v.(type) {
	case nil:
		return 0, nil
	case int:
		n = t
	case string:
		s := strings.TrimSuffix(strings.TrimSpace(t), "d")
		i, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("before: %q is not a number of days", t)
		}
		n = i
	default:
		return 0, fmt.Errorf("before: %v is not a number of days", v)
	}
	if n < 0 || n > MaxBefore {
		return 0, fmt.Errorf("before: %d days, want 0 to %d", n, MaxBefore)
	}
	return n, nil
}

func slugRe(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
