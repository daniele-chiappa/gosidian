package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/server/events"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// The write conformance suite (IMP-086): every tool that changes the vault
// is one case below, and every case is run twice. With the write budget
// free, the call must leave its audit entries and publish its events; with
// the budget already spent, it must be refused by the limiter and leave the
// vault and the audit log as they were. TestTools_EveryToolClassified makes
// a new tool fail until it is listed as read-only or given a case here: that
// is what keeps the next tool from skipping a step again (BUG-035, BUG-036,
// BUG-038, BUG-041, BUG-044).

// auditWant is an expected audit entry; a path ending in "*" is a prefix.
type auditWant struct {
	action audit.Action
	path   string
}

type conformanceCase struct {
	tool string
	name string // sub-case label when a tool has several modes
	// server builds the server; nil means newTestServer.
	server func(t *testing.T) *Server
	// ctx is the caller; nil means the implicit admin of a server without
	// a token store.
	ctx func() context.Context
	// setup prepares the vault and returns what call needs (a path).
	setup func(t *testing.T, s *Server, ctx context.Context) string
	call  func(ctx context.Context, s *Server, arg string) (*mcplib.CallToolResult, error)
	// audit and events may name the setup's result as "$".
	audit  []auditWant
	events []seenEvent
}

func (c conformanceCase) label() string {
	if c.name != "" {
		return c.tool + "/" + c.name
	}
	return c.tool
}

// pathMatches reports whether got is want, or starts with want minus its
// trailing "*".
func pathMatches(want, got string) bool {
	if p, ok := strings.CutSuffix(want, "*"); ok {
		return strings.HasPrefix(got, p)
	}
	return want == got
}

func seed(t *testing.T, s *Server, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		writeVaultFile(t, s.vault.Root, rel, body)
	}
}

func writeConformanceCases() []conformanceCase {
	png, _ := base64.StdEncoding.DecodeString(onePxPNG)
	none := func(*testing.T, *Server, context.Context) string { return "" }
	note := func(files map[string]string) func(*testing.T, *Server, context.Context) string {
		return func(t *testing.T, s *Server, _ context.Context) string { seed(t, s, files); return "" }
	}
	handoff := func(claim bool) func(*testing.T, *Server, context.Context) string {
		return func(t *testing.T, s *Server, ctx context.Context) string {
			path := createTestHandoff(t, s, ctx)
			if claim {
				if res, _ := s.handleClaimHandoff(ctx, call(map[string]any{"path": path})); res.IsError {
					t.Fatalf("claim: %s", expectError(t, res))
				}
			}
			return path
		}
	}
	args := func(h func(*Server) func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error), a map[string]any) func(context.Context, *Server, string) (*mcplib.CallToolResult, error) {
		return func(ctx context.Context, s *Server, _ string) (*mcplib.CallToolResult, error) {
			return h(s)(ctx, call(a))
		}
	}
	return []conformanceCase{
		{tool: "memory_create", setup: none,
			call:   args(func(s *Server) toolFn { return s.handleCreate }, map[string]any{"path": "p/n.md", "content": "x\n"}),
			audit:  []auditWant{{audit.ActionCreate, "p/n.md"}},
			events: []seenEvent{{events.TopicNote, "create", "p/n.md"}, {events.TopicTree, "create", "p/n.md"}}},
		{tool: "memory_update", setup: note(map[string]string{"p/a.md": "x\n"}),
			call:   args(func(s *Server) toolFn { return s.handleUpdate }, map[string]any{"path": "p/a.md", "content": "y\n"}),
			audit:  []auditWant{{audit.ActionUpdate, "p/a.md"}},
			events: []seenEvent{{events.TopicNote, "update", "p/a.md"}}},
		{tool: "memory_edit", setup: note(map[string]string{"p/a.md": "hello\n"}),
			call:   args(func(s *Server) toolFn { return s.handleEdit }, map[string]any{"path": "p/a.md", "old_string": "hello", "new_string": "bye"}),
			audit:  []auditWant{{audit.ActionUpdate, "p/a.md"}},
			events: []seenEvent{{events.TopicNote, "update", "p/a.md"}}},
		{tool: "memory_append", setup: note(map[string]string{"p/a.md": "x\n"}),
			call:   args(func(s *Server) toolFn { return s.handleAppend }, map[string]any{"path": "p/a.md", "content": "more"}),
			audit:  []auditWant{{audit.ActionAppend, "p/a.md"}},
			events: []seenEvent{{events.TopicNote, "update", "p/a.md"}}},
		{tool: "memory_append", name: "creates", setup: none,
			call:   args(func(s *Server) toolFn { return s.handleAppend }, map[string]any{"path": "p/new.md", "content": "first"}),
			audit:  []auditWant{{audit.ActionAppend, "p/new.md"}},
			events: []seenEvent{{events.TopicNote, "create", "p/new.md"}, {events.TopicTree, "create", "p/new.md"}}},
		{tool: "memory_delete", setup: note(map[string]string{"p/a.md": "x\n"}),
			call:   args(func(s *Server) toolFn { return s.handleDelete }, map[string]any{"path": "p/a.md"}),
			audit:  []auditWant{{audit.ActionDelete, "p/a.md"}},
			events: []seenEvent{{events.TopicNote, "delete", "p/a.md"}, {events.TopicTree, "delete", "p/a.md"}}},
		{tool: "memory_rename_note", setup: note(map[string]string{"p/a.md": "x\n"}),
			call:   args(func(s *Server) toolFn { return s.handleRenameNote }, map[string]any{"from": "p/a.md", "to": "p/b.md"}),
			audit:  []auditWant{{audit.ActionRename, "p/a.md"}},
			events: []seenEvent{{events.TopicNote, "delete", "p/a.md"}, {events.TopicNote, "create", "p/b.md"}, {events.TopicTree, "create", "p/b.md"}}},
		{tool: "memory_move_note", setup: note(map[string]string{"p/a.md": "x\n", "q/README.md": "q\n"}),
			call:   args(func(s *Server) toolFn { return s.handleMoveNote }, map[string]any{"path": "p/a.md", "project": "q"}),
			audit:  []auditWant{{audit.ActionRename, "p/a.md"}},
			events: []seenEvent{{events.TopicNote, "delete", "p/a.md"}, {events.TopicNote, "create", "q/a.md"}}},
		{tool: "memory_compact", setup: note(map[string]string{"p/log.md": "# L\n\n## 2026-01-01 a\n\nx\n\n## 2026-01-02 b\n\ny\n"}),
			call:   args(func(s *Server) toolFn { return s.handleCompact }, map[string]any{"path": "p/log.md", "keep_last_n": 1, "archive_summary": "old"}),
			audit:  []auditWant{{audit.ActionUpdate, "p/log.md"}},
			events: []seenEvent{{events.TopicNote, "update", "p/log.md"}}},
		{tool: "memory_refresh_hot", setup: note(map[string]string{
			"p/hot.md":              "# H\n\n" + recentMarkerOpen + "\nstale\n" + recentMarkerClose + "\n",
			"p/memory/decisions.md": "# D\n\n## ADR-001 one\n\nx\n",
		}),
			call:   args(func(s *Server) toolFn { return s.handleRefreshHot }, map[string]any{"project": "p"}),
			audit:  []auditWant{{audit.ActionUpdate, "p/hot.md"}},
			events: []seenEvent{{events.TopicNote, "update", "p/hot.md"}}},
		{tool: "memory_ask", setup: none,
			call:   args(func(s *Server) toolFn { return s.handleAsk }, map[string]any{"project": "p", "question": "why?"}),
			audit:  []auditWant{{audit.ActionAppend, "p/docs/open-questions.md"}},
			events: []seenEvent{{events.TopicNote, "create", "p/docs/open-questions.md"}, {events.TopicTree, "create", "p/docs/open-questions.md"}}},
		{tool: "memory_snapshot", setup: note(map[string]string{"p/hot.md": "---\ntitle: Hot\ntags: [p]\n---\n\n# Hot\n"}),
			call:   args(func(s *Server) toolFn { return s.handleSnapshot }, map[string]any{"path": "p/hot.md"}),
			audit:  []auditWant{{audit.ActionCreate, "p/hot.snapshots/*"}},
			events: []seenEvent{{events.TopicNote, "create", "p/hot.snapshots/*"}, {events.TopicTree, "create", "p/hot.snapshots/*"}}},
		{tool: "memory_create_handoff", setup: none,
			call: args(func(s *Server) toolFn { return s.handleCreateHandoff }, map[string]any{
				"project": "p", "from_agent": "a", "to_agent": "b", "summary": "do it", "pending_items": []any{"one"},
			}),
			audit:  []auditWant{{audit.ActionCreate, "p/handoffs/*"}},
			events: []seenEvent{{events.TopicNote, "create", "p/handoffs/*"}, {events.TopicTree, "create", "p/handoffs/*"}}},
		{tool: "memory_claim_handoff", setup: handoff(false),
			call: func(ctx context.Context, s *Server, path string) (*mcplib.CallToolResult, error) {
				return s.handleClaimHandoff(ctx, call(map[string]any{"path": path}))
			},
			audit:  []auditWant{{audit.ActionUpdate, "$"}},
			events: []seenEvent{{events.TopicNote, "update", "$"}}},
		{tool: "memory_complete_handoff", setup: handoff(true),
			call: func(ctx context.Context, s *Server, path string) (*mcplib.CallToolResult, error) {
				return s.handleCompleteHandoff(ctx, call(map[string]any{"path": path, "outcome": "done", "note": "ok"}))
			},
			audit:  []auditWant{{audit.ActionUpdate, "$"}},
			events: []seenEvent{{events.TopicNote, "update", "$"}}},
		{tool: "memory_self_improve",
			server: func(t *testing.T) *Server {
				s, _, _ := newTestServer(t)
				s.SetSelfImprove(true, "insights")
				return s
			},
			ctx:   func() context.Context { return optInCtx(true) },
			setup: none,
			call: args(func(s *Server) toolFn { return s.handleSelfImprove }, map[string]any{
				"category": "friction", "title": "slow", "friction": "it was slow", "confidence": "low",
			}),
			audit:  []auditWant{{audit.ActionCreate, "insights/*"}},
			events: []seenEvent{{events.TopicNote, "create", "insights/*"}, {events.TopicTree, "create", "insights/*"}}},
		{tool: "memory_create_table_note",
			setup: func(t *testing.T, s *Server, _ context.Context) string { s.vault.SetTableNotes(true); return "" },
			call: args(func(s *Server) toolFn { return s.handleCreateTableNote }, map[string]any{
				"project": "p", "data": csvB64(sampleCSV), "filename": "log.csv", "caption": "x",
			}),
			audit:  []auditWant{{audit.ActionUploadAttachment, "p/attachments/*"}, {audit.ActionCreate, "p/log.md"}},
			events: []seenEvent{{events.TopicNote, "create", "p/log.md"}, {events.TopicTree, "create", "p/log.md"}}},
		{tool: "memory_create_media_note",
			setup: func(t *testing.T, s *Server, _ context.Context) string { s.vault.SetMediaNotes(true); return "" },
			call: args(func(s *Server) toolFn { return s.handleCreateMediaNote }, map[string]any{
				"project": "p", "data": b64(string(png)), "filename": "shot.png", "caption": "x",
			}),
			audit:  []auditWant{{audit.ActionUploadAttachment, "p/attachments/*"}, {audit.ActionCreate, "p/shot.md"}},
			events: []seenEvent{{events.TopicNote, "create", "p/shot.md"}, {events.TopicTree, "create", "p/shot.md"}}},
		{tool: "memory_ingest", name: "note", setup: none,
			call: args(func(s *Server) toolFn { return s.handleIngest }, map[string]any{
				"project": "p", "data": b64("# Doc\n\ntext\n"), "filename": "doc.md", "note_path": "p/doc.md",
			}),
			audit:  []auditWant{{audit.ActionCreate, "p/doc.md"}},
			events: []seenEvent{{events.TopicNote, "create", "p/doc.md"}, {events.TopicTree, "create", "p/doc.md"}}},
		{tool: "memory_ingest", name: "attachment", setup: none,
			call: args(func(s *Server) toolFn { return s.handleIngest }, map[string]any{
				"project": "p", "data": b64(string(png)), "filename": "shot.png", "as": "attachment",
			}),
			audit: []auditWant{{audit.ActionUploadAttachment, "p/attachments/*"}}},
		{tool: "memory_ingest", name: "package",
			setup: func(t *testing.T, s *Server, _ context.Context) string { stagePackage(t, s, "pkg", guide); return "" },
			call: args(func(s *Server) toolFn { return s.handleIngest }, map[string]any{
				"project": "p", "as": "package", "bridge_filename": "pkg", "dest": "p/imp",
			}),
			audit:  []auditWant{{audit.ActionCreate, "p/imp/INDEX.md"}, {audit.ActionIngestPackage, "p/imp"}},
			events: []seenEvent{{events.TopicTree, "create", "p/imp"}}},
		{tool: "memory_ingest", name: "ticket", setup: none,
			call: args(func(s *Server) toolFn { return s.handleIngest }, map[string]any{"project": "p", "transfer": "http"})},
		{tool: "memory_upload_attachment", setup: none,
			call:  args(func(s *Server) toolFn { return s.handleUploadAttachment }, map[string]any{"project": "p", "data": b64(string(png)), "filename": "photo.png"}),
			audit: []auditWant{{audit.ActionUploadAttachment, "p/attachments/*"}}},
		{tool: "memory_upload_resource", setup: none,
			call:  args(func(s *Server) toolFn { return s.handleUploadResource }, map[string]any{"project": "p", "data": b64(string(png)), "filename": "photo.png"}),
			audit: []auditWant{{audit.ActionUploadAttachment, "p/attachments/*"}}},
		{tool: "memory_delete_attachment", setup: note(map[string]string{"p/attachments/x.png": string(png)}),
			call:  args(func(s *Server) toolFn { return s.handleDeleteAttachment }, map[string]any{"path": "p/attachments/x.png"}),
			audit: []auditWant{{audit.ActionDeleteAttachment, "p/attachments/x.png"}}},
		{tool: "memory_gc_attachments",
			setup: func(t *testing.T, s *Server, _ context.Context) string {
				seed(t, s, map[string]string{"p/attachments/old.png": string(png), "p/n.md": "# n\n"})
				old := time.Now().Add(-48 * time.Hour)
				if err := os.Chtimes(filepath.Join(s.vault.Root, "p", "attachments", "old.png"), old, old); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			call:  args(func(s *Server) toolFn { return s.handleGCAttachments }, map[string]any{"project": "p", "dry_run": false}),
			audit: []auditWant{{audit.ActionDeleteAttachment, "p/attachments/old.png"}}},
		{tool: "memory_project_scaffold",
			setup: func(t *testing.T, s *Server, _ context.Context) string {
				seedTemplatesForTest(t, s.vault.Root)
				return ""
			},
			call:   args(func(s *Server) toolFn { return s.handleProjectScaffold }, map[string]any{"project": "proj"}),
			audit:  []auditWant{{audit.ActionCreate, "proj/hot.md"}},
			events: []seenEvent{{events.TopicNote, "create", "proj/hot.md"}, {events.TopicTree, "create", "proj/hot.md"}}},
		{tool: "memory_promote_agent", setup: none,
			call: args(func(s *Server) toolFn { return s.handlePromoteAgent }, map[string]any{
				"project": "rc", "slug": "rc-database", "profile": "claude",
				"content": "---\nname: rc-database\ndescription: DB ops\ntools: Read\n---\n\nYou are the rc database engineer.\n",
			}),
			audit:  []auditWant{{audit.ActionCreate, "rc/agents/rc-database.md"}},
			events: []seenEvent{{events.TopicNote, "create", "rc/agents/rc-database.md"}, {events.TopicTree, "create", "rc/agents/rc-database.md"}}},
		{tool: "memory_create_project", setup: none,
			call:   args(func(s *Server) toolFn { return s.handleCreateProject }, map[string]any{"name": "delta"}),
			audit:  []auditWant{{audit.ActionCreateProject, "delta"}},
			events: []seenEvent{{events.TopicTree, "create_project", "delta"}, {events.TopicSidebar, "create", "delta"}}},
		{tool: "memory_rename_project", setup: note(map[string]string{"p/a.md": "x\n"}),
			call:   args(func(s *Server) toolFn { return s.handleRenameProject }, map[string]any{"from": "p", "to": "r"}),
			audit:  []auditWant{{audit.ActionRenameProject, "p"}},
			events: []seenEvent{{events.TopicTree, "rename_project", "p"}, {events.TopicSidebar, "update", "r"}}},
		{tool: "memory_delete_project", setup: note(map[string]string{"p/a.md": "x\n"}),
			call:   args(func(s *Server) toolFn { return s.handleDeleteProject }, map[string]any{"name": "p"}),
			audit:  []auditWant{{audit.ActionDeleteProject, "p"}},
			events: []seenEvent{{events.TopicTree, "delete_project", "p"}, {events.TopicSidebar, "delete", "p"}}},
		{tool: "memory_automations",
			server: func(t *testing.T) *Server { s, _, _, _ := automationServer(t); return s },
			setup:  none,
			call:   args(func(s *Server) toolFn { return s.handleAutomations }, map[string]any{"project": "p", "run": true}),
			audit:  []auditWant{{audit.ActionCreate, "p/handoffs/*"}},
			events: []seenEvent{{events.TopicNote, "create", "p/handoffs/*"}, {events.TopicTree, "create", "p/handoffs/*"}}},
	}
}

type toolFn = func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error)

// readOnlyTools never change the vault. A tool that is neither here nor in
// writeConformanceCases fails TestTools_EveryToolClassified.
var readOnlyTools = map[string]bool{
	"memory_attachment_info": true, "memory_audit_tail": true, "memory_backlinks": true,
	"memory_batch_get": true, "memory_bootstrap": true, "memory_get": true,
	"memory_get_frontmatter": true, "memory_get_outline": true, "memory_get_section": true,
	"memory_global_check": true, "memory_hubs": true, "memory_init_agent": true,
	"memory_lint": true, "memory_list_attachments": true, "memory_list_bootstrap_templates": true,
	"memory_list_notes": true, "memory_list_projects": true, "memory_list_tags": true,
	"memory_notes_by_importance": true, "memory_notes_by_tag": true, "memory_outlinks": true,
	"memory_path": true, "memory_pending_handoffs": true, "memory_pinned": true,
	"memory_plans": true, "memory_query": true, "memory_recent": true,
	"memory_search": true, "memory_self_stats": true, "memory_skills": true,
	"memory_stale": true, "memory_todos": true, "memory_wait_changes": true,
}

func TestTools_EveryToolClassified(t *testing.T) {
	s, _, _ := newTestServer(t)
	registered := s.impl.ListTools()
	cased := map[string]bool{}
	for _, c := range writeConformanceCases() {
		cased[c.tool] = true
	}
	var names []string
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch {
		case readOnlyTools[name] && cased[name]:
			t.Errorf("%s is listed as read-only and has a write case", name)
		case !readOnlyTools[name] && !cased[name]:
			t.Errorf("%s is not classified: list it in readOnlyTools, or give it a case in writeConformanceCases (audit, events, limiter)", name)
		}
	}
	for name := range readOnlyTools {
		if registered[name] == nil {
			t.Errorf("readOnlyTools lists %s, which is not registered", name)
		}
	}
	for name := range cased {
		if registered[name] == nil {
			t.Errorf("writeConformanceCases has %s, which is not registered", name)
		}
	}
}

// vaultState fingerprints every file under root.
func vaultState(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func auditEntries(t *testing.T, l *audit.Log) []audit.Entry {
	t.Helper()
	var out []audit.Entry
	if err := l.Each(func(e audit.Entry) { out = append(out, e) }); err != nil {
		t.Fatal(err)
	}
	return out
}

// conformanceRun prepares a case's server with an audit log and an event
// hub, runs its setup and, when limited, spends the write budget.
func conformanceRun(t *testing.T, c conformanceCase, limited bool) (*Server, context.Context, string, *audit.Log, *events.Hub) {
	t.Helper()
	var s *Server
	if c.server != nil {
		s = c.server(t)
	} else {
		s, _, _ = newTestServer(t)
	}
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	hub := events.New(events.HubOptions{})
	s.SetEvents(hub)
	ctx := context.Background()
	if c.ctx != nil {
		ctx = c.ctx()
	}
	arg := c.setup(t, s, ctx)
	if limited {
		s.SetWriteLimits(1, 0)
		res, _ := s.handleCreate(ctx, call(map[string]any{"path": "warm/up.md", "content": "x"}))
		if res.IsError {
			t.Fatalf("warm-up write: %s", expectError(t, res))
		}
	}
	return s, ctx, arg, al, hub
}

func TestTools_WriteConformance(t *testing.T) {
	for _, c := range writeConformanceCases() {
		t.Run(c.label(), func(t *testing.T) {
			s, ctx, arg, al, hub := conformanceRun(t, c, false)
			before := len(auditEntries(t, al))
			sub := hub.Subscribe(events.TopicNote, events.TopicTree, events.TopicSidebar)
			defer sub.Unsubscribe()
			res, err := c.call(ctx, s, arg)
			if err != nil {
				t.Fatal(err)
			}
			resultText(t, res)
			subst := func(p string) string { return strings.ReplaceAll(p, "$", arg) }

			got := auditEntries(t, al)[before:]
			for _, w := range c.audit {
				found := false
				for _, e := range got {
					found = found || (e.Action == w.action && pathMatches(subst(w.path), e.Path))
				}
				if !found {
					t.Errorf("no audit entry %s %s; got %+v", w.action, subst(w.path), got)
				}
			}
			evs := drainEvents(t, sub, len(c.events))
			for _, w := range c.events {
				found := false
				for _, e := range evs {
					found = found || (e.Topic == w.Topic && e.Action == w.Action && pathMatches(subst(w.Path), e.Path))
				}
				if !found {
					t.Errorf("no %s event %s %s; got %+v", w.Topic, w.Action, subst(w.Path), evs)
				}
			}
		})
		t.Run(c.label()+"/limited", func(t *testing.T) {
			s, ctx, arg, al, _ := conformanceRun(t, c, true)
			before, entries := vaultState(t, s.vault.Root), len(auditEntries(t, al))
			res, err := c.call(ctx, s, arg)
			if err != nil {
				t.Fatal(err)
			}
			if msg := expectError(t, res); !strings.Contains(msg, "rate limit") {
				t.Fatalf("expected a rate-limit refusal, got: %s", msg)
			}
			after := vaultState(t, s.vault.Root)
			for p, h := range after {
				if before[p] != h {
					t.Errorf("refused call changed %s", p)
				}
			}
			for p := range before {
				if _, ok := after[p]; !ok {
					t.Errorf("refused call removed %s", p)
				}
			}
			if n := len(auditEntries(t, al)); n != entries {
				t.Errorf("refused call wrote %d audit entries", n-entries)
			}
		})
	}
}
