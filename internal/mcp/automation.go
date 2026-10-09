package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/automation"
	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/views"
	"go.yaml.in/yaml/v3"
)

// The automations of database notes (IMP-127 iteration 3, phase 4) act
// through the server: AutomationHost reads and writes as the automation
// identity, a token of read and write scope on the project of the database
// note only, and audits as audit.SourceAutomation.

// AutomationAgent is the identity the automations write as: from_agent and
// created_by of their handoffs, and the author the audit log names.
const AutomationAgent = "automation"

// SetAutomations gives the server the engine memory_automations reads.
func (s *Server) SetAutomations(e *automation.Engine) { s.automations = e }

// AutomationHost is the server as the automations' host.
func (s *Server) AutomationHost() automation.Host { return automationHost{s} }

type automationHost struct{ s *Server }

func automationToken(project string) *auth.Token {
	return &auth.Token{Name: AutomationAgent, Projects: []string{project}, Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}
}

// Databases lists the database notes that declare automations.
func (h automationHost) Databases(project string) ([]automation.Database, error) {
	opts := index.QueryOptions{
		Where: []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{"database"}}},
		Sort:  "path",
		Limit: index.QueryMaxLimit,
	}
	if project != "" {
		opts.Projects = []string{project}
	}
	hits, _, err := h.s.index.Query(opts)
	if err != nil {
		return nil, err
	}
	var out []automation.Database
	for _, hit := range hits {
		note, err := h.s.vault.Load(hit.Path)
		if err != nil {
			continue
		}
		fm := parser.FrontmatterRawForPath(hit.Path, note.Content)
		rules, errs := automation.Parse(hit.Path, fm)
		if len(rules) == 0 && len(errs) == 1 && errors.Is(errs[0], automation.ErrNone) {
			continue
		}
		proj, _, _ := strings.Cut(hit.Path, "/")
		db := automation.Database{Path: hit.Path, Project: proj, Rules: rules, Errors: errs}
		sc, err := dbschema.Parse(hit.Path, fm)
		if err != nil {
			db.Rules = nil
			db.Errors = append(db.Errors, fmt.Errorf("the database's schema does not parse: %w", err))
		} else {
			db.Source = sc.Source
		}
		out = append(out, db)
	}
	return out, nil
}

// Rows returns the rows of db that meet r.Where and have r.Due set, read
// as the automation reads its project.
func (h automationHost) Rows(db automation.Database, r automation.Rule, now time.Time) ([]automation.Row, error) {
	note, err := h.s.vault.Load(db.Path)
	if err != nil {
		return nil, err
	}
	sc, err := dbschema.Parse(db.Path, parser.FrontmatterRawForPath(db.Path, note.Content))
	if err != nil {
		return nil, err
	}
	if f, ok := sc.Field(r.Due); !ok || f.Type != "date" {
		return nil, fmt.Errorf("due: %q is not a date field of the database (fields: %s)", r.Due, strings.Join(sc.FieldNames(), ", "))
	}
	tok := automationToken(db.Project)
	src, err := yaml.Marshal(map[string]any{"from": sc.Source, "where": r.Where})
	if err != nil {
		return nil, err
	}
	spec, err := views.Parse(string(src), views.Context{This: views.ThisFields(db.Path, nil), Today: now, Resolve: h.s.viewResolve(tok)})
	if err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}
	where := append(spec.Where, rowConds(sc)...)
	where = append(where, index.FieldCond{Field: r.Due, Op: index.OpExists, Values: []string{"true"}})
	// Every row, a page at a time, up to one past the engine's cap: one
	// query of QueryMaxLimit left the 501st row without a handoff, and
	// nothing said so (IMP-161, S3-8). The pages step by the index's
	// total, before the token's reach drops a hit (scopedQuery).
	opts := index.QueryOptions{Folders: spec.From, Where: where, Fields: []string{r.Due}, Sort: "path", Limit: index.QueryMaxLimit}
	var rows []automation.Row
	for len(rows) <= automation.MaxRows {
		hits, total, _, err := h.s.scopedQuery(tok, opts)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			if !sc.Covers(hit.Path) {
				continue
			}
			due := ""
			if vs := hit.Fields[r.Due]; len(vs) > 0 {
				due = vs[0]
			}
			rows = append(rows, automation.Row{Path: hit.Path, Title: hit.Title, Due: due})
		}
		opts.Offset += opts.Limit
		if opts.Offset >= total {
			break
		}
	}
	return rows, nil
}

// Handoff writes a handoff from the automation to r.Handoff in the
// database's project.
func (h automationHost) Handoff(db automation.Database, r automation.Rule, summary string, items []string, now time.Time) (string, error) {
	now = now.UTC()
	content := renderHandoffBody(AutomationAgent, r.Handoff, AutomationAgent, now, summary, items)
	base := fmt.Sprintf("%s/handoffs/%s-%s", db.Project, now.Format("20060102-150405"), handoffSlug(AutomationAgent, r.Handoff))
	for i := 0; i < 10; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		candidate += ".md"
		if ok, err := h.write(candidate, []byte(content)); err != nil {
			return "", err
		} else if ok {
			return candidate, nil
		}
	}
	return "", errors.New("no free handoff path")
}

// Snapshot freezes r.Snapshot as the automation reads it.
func (h automationHost) Snapshot(db automation.Database, r automation.Rule, now time.Time) (string, error) {
	note, err := h.s.vault.Load(r.Snapshot)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	content, _ := h.s.snapshotContent(automationToken(db.Project), r.Snapshot, note, now)
	dest := h.s.snapshotDest(r.Snapshot, now)
	ok, err := h.write(dest, content)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("snapshot %q already exists", dest)
	}
	return dest, nil
}

// write creates rel unless it exists (false), through the write pipeline
// as the automation: audited as such, without a token's limiter.
func (h automationHost) write(rel string, content []byte) (bool, error) {
	_, werr := h.s.writeNote(context.Background(), nil, noteWrite{
		rel: rel, mode: writeCreate, content: fixedContent(content), action: audit.ActionCreate, automation: true,
	})
	switch {
	case werr == nil:
		return true, nil
	case werr.kind == writeExists:
		return false, nil
	default:
		return false, werr
	}
}
