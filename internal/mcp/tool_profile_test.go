package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// rpcMessage drives the real JSON-RPC dispatch (the same path an SSE client
// hits), so the ToolFilter is exercised exactly as in production.
func rpcMessage(t *testing.T, s *Server, ctx context.Context, method, params string) string {
	t.Helper()
	msg := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	res := s.impl.HandleMessage(ctx, json.RawMessage(msg))
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listToolNames(t *testing.T, s *Server, ctx context.Context) map[string]struct{} {
	t.Helper()
	out := rpcMessage(t, s, ctx, "tools/list", `{}`)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse tools/list: %v — %s", err, out)
	}
	names := make(map[string]struct{}, len(resp.Result.Tools))
	for _, tl := range resp.Result.Tools {
		names[tl.Name] = struct{}{}
	}
	return names
}

func coreCtx(tok *auth.Token) context.Context {
	return context.WithValue(context.Background(), tokenCtxKey, tok)
}

func TestToolProfile_CoreFiltersListAndCall(t *testing.T) {
	s, _, _ := newTestServer(t)
	worker := &auth.Token{ID: "w1", Name: "worker", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}, ToolProfile: auth.ToolProfileCore}

	// Full surface (auth disabled → admin → full profile).
	full := listToolNames(t, s, context.Background())
	if len(full) <= len(coreToolSet) {
		t.Fatalf("full profile should see the whole catalogue, got %d tools", len(full))
	}

	// Core token: exactly the static core set (media/table flags off,
	// no self-improve opt-in on this token).
	core := listToolNames(t, s, coreCtx(worker))
	if len(core) != len(coreToolSet) {
		t.Errorf("core list = %d tools, want %d: %v", len(core), len(coreToolSet), core)
	}
	for name := range coreToolSet {
		if _, ok := core[name]; !ok {
			t.Errorf("core set missing %q in tools/list", name)
		}
	}
	if _, ok := core["memory_audit_tail"]; ok {
		t.Error("full-only tool leaked into the core list")
	}

	// tools/call: hidden tool blocked, core tool served.
	if out := rpcMessage(t, s, coreCtx(worker), "tools/call", `{"name":"memory_audit_tail","arguments":{}}`); !strings.Contains(out, "not found") {
		t.Errorf("call on filtered-out tool should be rejected, got: %s", out)
	}
	if out := rpcMessage(t, s, coreCtx(worker), "tools/call", `{"name":"memory_list_projects","arguments":{}}`); strings.Contains(out, "not found") {
		t.Errorf("core tool should be callable: %s", out)
	}
	// The same hidden tool works on the full profile.
	if out := rpcMessage(t, s, context.Background(), "tools/call", `{"name":"memory_audit_tail","arguments":{}}`); strings.Contains(out, "not found") {
		t.Errorf("full profile should reach memory_audit_tail: %s", out)
	}
}

func TestToolProfile_DynamicAdmissions(t *testing.T) {
	s, _, _ := newTestServer(t)
	worker := &auth.Token{ID: "w2", Name: "worker", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}, ToolProfile: auth.ToolProfileCore}

	// ADR-018: the legacy upload matrix stays hidden from core even with the
	// flags on — memory_ingest is the single door and routes internally.
	s.vault.SetTableNotes(true)
	s.vault.SetMediaNotes(true)
	names := listToolNames(t, s, coreCtx(worker))
	for _, legacy := range []string{
		"memory_create_table_note", "memory_create_media_note",
		"memory_upload_attachment", "memory_upload_resource",
	} {
		if _, ok := names[legacy]; ok {
			t.Errorf("%s should stay full-profile-only under ADR-018", legacy)
		}
	}
	if _, ok := names["memory_ingest"]; !ok {
		t.Error("memory_ingest should be in the core surface")
	}

	// Self-improve opt-in admits memory_self_improve.
	if _, ok := names["memory_self_improve"]; ok {
		t.Error("self_improve should be hidden without opt-in")
	}
	worker.SelfImproveOptIn = true
	names = listToolNames(t, s, coreCtx(worker))
	if _, ok := names["memory_self_improve"]; !ok {
		t.Error("self_improve should appear for an opted-in core token")
	}
}

// A note format off costs no schema either (roadmap M1.3): the creators of
// table and media notes, which could only refuse while their format is off,
// are listed only while it is on, and are "not found" on tools/call
// meanwhile. memory_ingest stays the door for those files either way.
func TestToolProfile_FormatToolsFollowTheirFormat(t *testing.T) {
	s, _, _ := newTestServer(t)
	admin := context.Background()
	lists := func(t *testing.T, table, media bool) {
		t.Helper()
		names := listToolNames(t, s, admin)
		if has(names, "memory_create_table_note") != table || has(names, "memory_create_media_note") != media {
			t.Errorf("table and media creators listed %v and %v, want %v and %v",
				has(names, "memory_create_table_note"), has(names, "memory_create_media_note"), table, media)
		}
		if !has(names, "memory_ingest") {
			t.Error("memory_ingest is not listed")
		}
	}
	lists(t, false, false)
	for _, name := range []string{"memory_create_table_note", "memory_create_media_note"} {
		if out := rpcMessage(t, s, admin, "tools/call", `{"name":"`+name+`","arguments":{}}`); !strings.Contains(out, "not found") {
			t.Errorf("%s answers with its format off: %s", name, out)
		}
	}
	s.vault.SetTableNotes(true)
	lists(t, true, false)
	s.vault.SetMediaNotes(true)
	lists(t, true, true)
}

// A branch off costs no schema (roadmap M2.2): its tools are listed only
// while it is on, and are "not found" on tools/call while it is off.
func TestToolProfile_BranchToolsFollowTheirBranch(t *testing.T) {
	pipelineTools := []string{"memory_pipelines", "memory_pipeline_run", "memory_create_team"}
	s := inUploadServer(t)
	admin := context.Background()
	names := listToolNames(t, s, admin)
	for _, name := range pipelineTools {
		if has(names, name) {
			t.Errorf("%s is listed with the pipelines off", name)
		}
	}
	if out := rpcMessage(t, s, admin, "tools/call", `{"name":"memory_pipelines","arguments":{}}`); !strings.Contains(out, "not found") {
		t.Errorf("memory_pipelines answers with the pipelines off: %s", out)
	}
	s.SetPipelinesSwitch(true)
	names = listToolNames(t, s, admin)
	for _, name := range pipelineTools {
		if !has(names, name) {
			t.Errorf("%s is not listed with the pipelines on", name)
		}
	}

	// memory_in_upload: only to a token that may write in a project with
	// use_in (p has it, q has not).
	for _, c := range []struct {
		tok  *auth.Token
		want bool
	}{
		{&auth.Token{ID: "w1", Name: "writer", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}, true},
		{&auth.Token{ID: "r1", Name: "reader", Project: "p", Scopes: []string{auth.ScopeRead}}, false},
		{&auth.Token{ID: "o1", Name: "other", Project: "q", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}, false},
	} {
		if got := has(listToolNames(t, s, ctxWithToken(c.tok)), "memory_in_upload"); got != c.want {
			t.Errorf("%s sees memory_in_upload %v, want %v", c.tok.Name, got, c.want)
		}
	}
	plain, _, _ := newTestServer(t)
	if has(listToolNames(t, plain, admin), "memory_in_upload") {
		t.Error("memory_in_upload is listed on a server with no IN folder")
	}
}
