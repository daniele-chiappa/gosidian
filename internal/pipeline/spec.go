// Package pipeline runs the pipelines of the vault (IMP-167, ADR-035): a
// note `type: pipeline` holds, in a ```pipeline block (YAML), a trigger and
// a list of nodes; a run executes the nodes on the job runner
// (internal/jobs), passing JSON values from one to the next.
package pipeline

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/automation"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/views"
)

// NoteType is the frontmatter type of a pipeline note.
const NoteType = "pipeline"

// Trigger kinds.
const (
	// TriggerManual: started by hand, from the web UI or a tool.
	TriggerManual = "manual"
	// TriggerExternal: started from outside, over HTTP or MCP; manual
	// starts work too.
	TriggerExternal = "external"
	// TriggerIngest: a document arrives as an attachment of the project.
	TriggerIngest = "ingest"
	// TriggerSchedule: at a time of the week or day, like the automations.
	TriggerSchedule = "schedule"
	// TriggerIN: files arrive in the project's IN folder (IMP-172), one
	// run per file or per batch.
	TriggerIN = "in"
	// TriggerCommit: a commit of the vault's git sync changed files of a
	// folder of the project (IMP-170): one run per commit.
	TriggerCommit = "commit"
	// TriggerPipeline: another run started it with pipeline.run
	// (IMP-186). Not a trigger a pipeline declares: any pipeline starts
	// this way, as any starts by hand.
	TriggerPipeline = "pipeline"
	// TriggerChat: a message of a chat, or the rating of an answer
	// (IMP-191), started from outside like external, with a run token,
	// or by hand; its input is checked (CheckChatInput), and its runs are
	// interactive.
	TriggerChat = "chat"
	// TriggerRun: a run of a pipeline of the project ended with one of
	// the trigger's statuses (IMP-206): one run per run that ended. A run
	// it starts starts none when it ends (runtrigger.go).
	TriggerRun = "run"
)

// runStatuses are the ends of a run a run trigger may take.
var runStatuses = []string{string(jobs.Done), string(jobs.Failed), string(jobs.Canceled)}

// PriorityInteractive is the priority of a pipeline whose runs someone
// waits for: their steps go before the others', and to the interactive
// workers ([pipelines] interactive_workers).
const PriorityInteractive = "interactive"

// Limits of a spec.
const (
	MaxNodes        = 64
	MaxRetries      = 10
	DefaultTimeout  = 5 * time.Minute
	DefaultWaitTime = 24 * time.Hour
)

// Trigger is what starts a pipeline.
type Trigger struct {
	On string `json:"on"`
	// Kinds are the file extensions an ingest or in trigger takes (pdf,
	// docx); for in, none is all.
	Kinds []string `json:"kinds,omitempty"`
	// Folder, for ingest, is the folder of the project the document
	// must arrive in (the folder of the note it is ingested with); for in,
	// a folder of the IN folder.
	Folder string `json:"folder,omitempty"`
	// Match, for in, is a glob on the file name (*_T?.pdf).
	Match string `json:"match,omitempty"`
	// Batch, for in, starts one run per batch instead of one per file.
	Batch bool `json:"batch,omitempty"`
	// Leave, for in (take: false), leaves the status of the files alone:
	// a run that reads them (counts, summaries, notices) does not take
	// them, nor mark them working, then done (IMP-174).
	Leave bool `json:"leave,omitempty"`
	// Skip, for commit, are pipelines of the project (named as
	// pipeline.run names them) whose writes do not start it: the files a
	// run of one of them wrote last are left out, as its own runs' are.
	// Only those pipelines, not the ones they start (IMP-201: an import
	// that cuts its own notes).
	Skip []string `json:"skip,omitempty"`
	// Every, for schedule: "monday 09:00", "day 18:30", "hour :05".
	Every    string               `json:"every,omitempty"`
	Schedule *automation.Schedule `json:"-"`
	// Status, for run, are the ends of a run it takes: done, failed,
	// canceled; failed when it names none.
	Status []string `json:"status,omitempty"`
	// Pipelines, for run, is a glob on the paths of the pipeline notes
	// whose runs it takes, relative to the project (pipelines/*); empty:
	// all of the project's.
	Pipelines string `json:"pipelines,omitempty"`
}

// Retry says how often a failed node is tried again.
type Retry struct {
	Attempts int           `json:"attempts,omitempty"` // tries in all, 1 = no retry
	Backoff  time.Duration `json:"backoff,omitempty"`  // before the second try, doubling after
}

// Node is one step of a pipeline.
type Node struct {
	ID   string         `json:"id"`
	Use  string         `json:"use"`
	With map[string]any `json:"with,omitempty"`
	// After are the nodes it waits for; unset, the node before it.
	After []string `json:"after"`
	// When and Unless are {{ }} conditions: false (or true) skips it.
	When   string `json:"when,omitempty"`
	Unless string `json:"unless,omitempty"`
	// Each runs the node once per element of a {{ }} list.
	Each    string        `json:"each,omitempty"`
	Retry   Retry         `json:"retry"`
	Timeout time.Duration `json:"timeout,omitempty"`
	// OnError is stop (the default: the run fails) or continue (the
	// nodes after it run with a null output).
	OnError string `json:"on_error,omitempty"`
}

// Spec is a pipeline as its note declares it.
type Spec struct {
	Trigger Trigger `json:"trigger"`
	Nodes   []Node  `json:"nodes"`
	// Answer names the node whose output is the run's answer: what a start
	// that waits gives back, and the callback (IMP-191).
	Answer string `json:"answer,omitempty"`
	// Priority is PriorityInteractive, or empty for a normal pipeline.
	Priority string `json:"priority,omitempty"`
}

// Interactive says whether the pipeline's runs are interactive: it says
// so, or it starts on chat.
func (s *Spec) Interactive() bool {
	return s.Priority == PriorityInteractive || s.Trigger.On == TriggerChat
}

// Node returns the node with id.
func (s *Spec) Node(id string) (*Node, bool) {
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			return &s.Nodes[i], true
		}
	}
	return nil, false
}

// FindBlock returns the ```pipeline block of a note's body: "" when there
// is none, an error when there are more.
func FindBlock(body []byte) (string, error) {
	blocks := views.FindFenced(body, "pipeline")
	switch len(blocks) {
	case 0:
		return "", nil
	case 1:
		return blocks[0].Spec, nil
	default:
		return "", fmt.Errorf("the note has %d ```pipeline blocks, a pipeline has one", len(blocks))
	}
}

var idRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// reserved roots of the expressions: a node may not be called so.
var reserved = []string{"trigger", "run", "item", "index"}

type rawNode struct {
	ID     string         `yaml:"id"`
	Use    string         `yaml:"use"`
	With   map[string]any `yaml:"with"`
	After  *[]string      `yaml:"after"`
	When   string         `yaml:"when"`
	Unless string         `yaml:"unless"`
	Each   string         `yaml:"each"`
	Retry  *struct {
		Attempts int    `yaml:"attempts"`
		Backoff  string `yaml:"backoff"`
	} `yaml:"retry"`
	Timeout string `yaml:"timeout"`
	OnError string `yaml:"on_error"`
}

type rawSpec struct {
	Trigger struct {
		On        string   `yaml:"on"`
		Kinds     []string `yaml:"kinds"`
		Folder    string   `yaml:"folder"`
		Every     string   `yaml:"every"`
		Match     string   `yaml:"match"`
		Batch     bool     `yaml:"batch"`
		Take      *bool    `yaml:"take"`
		Skip      []string `yaml:"skip"`
		Status    []string `yaml:"status"`
		Pipelines string   `yaml:"pipelines"`
	} `yaml:"trigger"`
	Nodes    yaml.Node `yaml:"nodes"`
	Answer   string    `yaml:"answer"`
	Priority string    `yaml:"priority"`
}

// Types tells which node types exist, for Parse to check `use`.
type Types interface {
	Known(use string) bool
}

// Parse reads and checks a ```pipeline block. types may be nil (no
// check of the node types).
func Parse(src string, types Types) (*Spec, error) {
	var r rawSpec
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		return nil, fmt.Errorf("the pipeline is not valid YAML: %w", err)
	}
	s := &Spec{Answer: strings.TrimSpace(r.Answer), Priority: strings.TrimSpace(r.Priority), Trigger: Trigger{
		On:     strings.TrimSpace(r.Trigger.On),
		Folder: strings.Trim(strings.TrimSpace(r.Trigger.Folder), "/"),
		Every:  strings.TrimSpace(r.Trigger.Every),
		Match:  strings.TrimSpace(r.Trigger.Match),
		Batch:  r.Trigger.Batch,
		Leave:  r.Trigger.Take != nil && !*r.Trigger.Take,
	}}
	for _, k := range r.Trigger.Kinds {
		k = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(k), "."))
		if k != "" {
			s.Trigger.Kinds = append(s.Trigger.Kinds, k)
		}
	}
	if s.Trigger.On == "" {
		s.Trigger.On = TriggerManual
	}
	switch s.Trigger.On {
	case TriggerManual, TriggerExternal, TriggerChat:
	case TriggerIngest:
		if len(s.Trigger.Kinds) == 0 {
			return nil, fmt.Errorf("trigger ingest: kinds lists the file extensions it takes (kinds: [pdf, docx])")
		}
	case TriggerSchedule:
		sc, err := automation.ParseSchedule(s.Trigger.Every)
		if err != nil {
			return nil, fmt.Errorf("trigger schedule: %w", err)
		}
		s.Trigger.Schedule = &sc
	case TriggerIN:
		s.Trigger.Folder = inRelative(s.Trigger.Folder)
		if s.Trigger.Match != "" {
			if _, err := path.Match(s.Trigger.Match, ""); err != nil {
				return nil, fmt.Errorf("trigger in: match %q is not a glob (*.pdf, fattura_??.xml)", s.Trigger.Match)
			}
		}
	case TriggerCommit:
		if strings.Contains(s.Trigger.Folder, "..") {
			return nil, fmt.Errorf("trigger commit: folder %q is a folder of the project", s.Trigger.Folder)
		}
		if s.Trigger.Match != "" {
			if _, err := path.Match(s.Trigger.Match, ""); err != nil {
				return nil, fmt.Errorf("trigger commit: match %q is not a glob (*.md)", s.Trigger.Match)
			}
		}
	case TriggerRun:
		for _, st := range r.Trigger.Status {
			st = strings.ToLower(strings.TrimSpace(st))
			if !slices.Contains(runStatuses, st) {
				return nil, fmt.Errorf("trigger run: status %q is how a run ends: %s", st, strings.Join(runStatuses, ", "))
			}
			if !slices.Contains(s.Trigger.Status, st) {
				s.Trigger.Status = append(s.Trigger.Status, st)
			}
		}
		if len(s.Trigger.Status) == 0 {
			s.Trigger.Status = []string{string(jobs.Failed)}
		}
		g := strings.TrimPrefix(strings.TrimSpace(r.Trigger.Pipelines), "/")
		if _, err := path.Match(g, ""); err != nil || strings.Contains(g, "..") || strings.Contains(g, "{{") {
			return nil, fmt.Errorf("trigger run: pipelines %q is a glob on the project's pipeline notes, relative to it (pipelines/*)", r.Trigger.Pipelines)
		}
		s.Trigger.Pipelines = g
	default:
		return nil, fmt.Errorf("trigger on %q: it is manual, external, chat, ingest, schedule, in, commit or run", s.Trigger.On)
	}
	if s.Trigger.On != TriggerRun && (len(r.Trigger.Status) > 0 || strings.TrimSpace(r.Trigger.Pipelines) != "") {
		return nil, fmt.Errorf("trigger %s: status and pipelines are for the run trigger (the runs whose end starts it)", s.Trigger.On)
	}
	if r.Trigger.Take != nil && s.Trigger.On != TriggerIN {
		return nil, fmt.Errorf("trigger %s: take is for the in trigger (take: false leaves the files' status alone)", s.Trigger.On)
	}
	for _, p := range r.Trigger.Skip {
		p = strings.TrimSpace(p)
		if s.Trigger.On != TriggerCommit {
			return nil, fmt.Errorf("trigger %s: skip is for the commit trigger (the pipelines whose writes do not start it)", s.Trigger.On)
		}
		if p == "" || strings.Contains(p, "{{") || strings.Contains(p, "..") {
			return nil, fmt.Errorf("trigger commit: skip %q is a pipeline of the project, as pipeline.run names it", p)
		}
		s.Trigger.Skip = append(s.Trigger.Skip, p)
	}
	switch s.Priority {
	case "", "normal":
		s.Priority = ""
	case PriorityInteractive:
	default:
		return nil, fmt.Errorf("priority %q: interactive, for a pipeline whose runs someone waits for, or normal", s.Priority)
	}

	raws, err := rawNodes(&r.Nodes)
	if err != nil {
		return nil, err
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("the pipeline has no nodes")
	}
	if len(raws) > MaxNodes {
		return nil, fmt.Errorf("the pipeline has %d nodes, at most %d", len(raws), MaxNodes)
	}
	seen := map[string]bool{}
	for i, rn := range raws {
		n, err := buildNode(rn, i, raws, types)
		if err != nil {
			return nil, err
		}
		if seen[n.ID] {
			return nil, fmt.Errorf("node %q: the id is used twice", n.ID)
		}
		seen[n.ID] = true
		s.Nodes = append(s.Nodes, n)
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	if s.Answer != "" {
		if _, ok := s.Node(s.Answer); !ok {
			return nil, fmt.Errorf("answer names %q, which is not a node", s.Answer)
		}
	}
	return s, nil
}

// rawNodes reads nodes as a list ([{id: a, use: …}]) or as a map in its
// written order ({a: {use: …}}).
func rawNodes(n *yaml.Node) ([]rawNode, error) {
	switch n.Kind {
	case 0:
		return nil, nil
	case yaml.SequenceNode:
		var out []rawNode
		if err := n.Decode(&out); err != nil {
			return nil, fmt.Errorf("nodes: %w", err)
		}
		return out, nil
	case yaml.MappingNode:
		var out []rawNode
		for i := 0; i+1 < len(n.Content); i += 2 {
			var rn rawNode
			if err := n.Content[i+1].Decode(&rn); err != nil {
				return nil, fmt.Errorf("node %q: %w", n.Content[i].Value, err)
			}
			if rn.ID != "" && rn.ID != n.Content[i].Value {
				return nil, fmt.Errorf("node %q: its id says %q", n.Content[i].Value, rn.ID)
			}
			rn.ID = n.Content[i].Value
			out = append(out, rn)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("nodes is a list of nodes, or a map from id to node")
	}
}

func buildNode(rn rawNode, i int, all []rawNode, types Types) (Node, error) {
	n := Node{ID: strings.TrimSpace(rn.ID), Use: strings.TrimSpace(rn.Use), With: rn.With,
		When: strings.TrimSpace(rn.When), Unless: strings.TrimSpace(rn.Unless), Each: strings.TrimSpace(rn.Each),
		OnError: strings.TrimSpace(rn.OnError)}
	label := n.ID
	if label == "" {
		label = fmt.Sprintf("#%d", i+1)
	}
	if !idRe.MatchString(n.ID) {
		return n, fmt.Errorf("node %s: the id is lowercase letters, digits and _, starting with a letter", label)
	}
	if slices.Contains(reserved, n.ID) {
		return n, fmt.Errorf("node %q: %q is a reserved name (%s)", n.ID, n.ID, strings.Join(reserved, ", "))
	}
	if n.Use == "" {
		return n, fmt.Errorf("node %q: use names the node type (note.write, llm.describe…)", n.ID)
	}
	if types != nil && !types.Known(n.Use) {
		if h, ok := types.(interface{ Why(use string) string }); ok {
			if why := h.Why(n.Use); why != "" {
				return n, fmt.Errorf("node %q: there is no node type %q: %s", n.ID, n.Use, why)
			}
		}
		return n, fmt.Errorf("node %q: there is no node type %q", n.ID, n.Use)
	}
	switch {
	case rn.After != nil:
		n.After = *rn.After
	case i > 0:
		n.After = []string{strings.TrimSpace(all[i-1].ID)}
	}
	if n.OnError == "" {
		n.OnError = "stop"
	}
	if n.OnError != "stop" && n.OnError != "continue" {
		return n, fmt.Errorf("node %q: on_error is stop or continue", n.ID)
	}
	n.Timeout = DefaultTimeout
	if rn.Timeout != "" {
		d, err := time.ParseDuration(rn.Timeout)
		if err != nil || d <= 0 {
			return n, fmt.Errorf("node %q: timeout %q is a duration (30s, 5m)", n.ID, rn.Timeout)
		}
		n.Timeout = d
	}
	n.Retry = Retry{Attempts: 1}
	if rn.Retry != nil {
		if rn.Retry.Attempts < 1 || rn.Retry.Attempts > MaxRetries {
			return n, fmt.Errorf("node %q: retry attempts is between 1 and %d", n.ID, MaxRetries)
		}
		n.Retry.Attempts = rn.Retry.Attempts
		n.Retry.Backoff = 10 * time.Second
		if rn.Retry.Backoff != "" {
			d, err := time.ParseDuration(rn.Retry.Backoff)
			if err != nil || d < 0 {
				return n, fmt.Errorf("node %q: retry backoff %q is a duration", n.ID, rn.Retry.Backoff)
			}
			n.Retry.Backoff = d
		}
	}
	for _, f := range []struct{ name, v string }{{"when", n.When}, {"unless", n.Unless}, {"each", n.Each}} {
		if f.v == "" {
			continue
		}
		m := exprRe.FindStringSubmatchIndex(f.v)
		if m == nil || m[0] != 0 || m[1] != len(f.v) {
			return n, fmt.Errorf("node %q: %s is one {{ }} reference", n.ID, f.name)
		}
	}
	return n, nil
}

// check verifies the graph: known dependencies, no cycle, and references
// only to nodes that run before.
func (s *Spec) check() error {
	ids := map[string]*Node{}
	for i := range s.Nodes {
		ids[s.Nodes[i].ID] = &s.Nodes[i]
	}
	for _, n := range s.Nodes {
		for _, d := range n.After {
			if _, ok := ids[d]; !ok {
				return fmt.Errorf("node %q: after names %q, which is not a node", n.ID, d)
			}
			if d == n.ID {
				return fmt.Errorf("node %q: it cannot come after itself", n.ID)
			}
		}
	}
	// Ancestors, and a cycle check, by depth-first search.
	anc := map[string]map[string]bool{}
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("node %q: the nodes after it lead back to it (a cycle)", id)
		case 2:
			return nil
		}
		state[id] = 1
		a := map[string]bool{}
		for _, d := range ids[id].After {
			if err := visit(d); err != nil {
				return err
			}
			a[d] = true
			for x := range anc[d] {
				a[x] = true
			}
		}
		anc[id] = a
		state[id] = 2
		return nil
	}
	for _, n := range s.Nodes {
		if err := visit(n.ID); err != nil {
			return err
		}
	}
	for _, n := range s.Nodes {
		refs, err := refsIn(n.With)
		if err != nil {
			return fmt.Errorf("node %q: %w", n.ID, err)
		}
		for _, e := range []string{n.When, n.Unless, n.Each} {
			if e == "" {
				continue
			}
			r, err := refsIn(e)
			if err != nil {
				return fmt.Errorf("node %q: %w", n.ID, err)
			}
			refs = append(refs, r...)
		}
		for _, r := range refs {
			switch r.root {
			case "trigger", "run":
				continue
			case "item", "index":
				if n.Each == "" {
					return fmt.Errorf("node %q: {{ %s }} only exists in a node with each", n.ID, r)
				}
				continue
			}
			if _, ok := ids[r.root]; !ok {
				return fmt.Errorf("node %q: {{ %s }}: there is no node %q", n.ID, r, r.root)
			}
			if !anc[n.ID][r.root] {
				return fmt.Errorf("node %q: {{ %s }}: %q does not run before it (add it to after)", n.ID, r, r.root)
			}
		}
	}
	return nil
}

// Matches says whether an ingest trigger takes a document: its extension
// is one of Kinds, and, with a Folder, it arrives in that folder of the
// project or below.
func (t Trigger) Matches(ext, folder string) bool {
	if t.On != TriggerIngest {
		return false
	}
	if !slices.Contains(t.Kinds, strings.ToLower(strings.TrimPrefix(ext, "."))) {
		return false
	}
	if t.Folder == "" {
		return true
	}
	folder = strings.Trim(folder, "/")
	return folder == t.Folder || strings.HasPrefix(folder, t.Folder+"/")
}

// FromNote is a pipeline note as a Pipeline: Err says why it cannot run
// (no block, a block that does not parse).
func FromNote(path, project, title string, content []byte, types Types) Pipeline {
	p := Pipeline{Path: path, Project: project, Title: title}
	src, err := FindBlock(content)
	switch {
	case err != nil:
		p.Err = err
	case src == "":
		p.Err = fmt.Errorf("the note has no ```pipeline block")
	default:
		p.Spec, p.Err = Parse(src, types)
	}
	return p
}
