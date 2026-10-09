package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func decodeResult(t *testing.T, r *mcplib.CallToolResult, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(resultText(t, r)), out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func projectToken(id, name string) *auth.Token {
	return &auth.Token{
		ID:      id,
		Name:    name,
		Project: "p",
		Scopes:  []string{auth.ScopeRead, auth.ScopeWrite},
	}
}

func ctxWithToken(tok *auth.Token) context.Context {
	return context.WithValue(context.Background(), tokenCtxKey, tok)
}

type handoffListResult struct {
	Handoffs []handoffEntry `json:"handoffs"`
}

func createTestHandoff(t *testing.T, s *Server, ctx context.Context) string {
	t.Helper()
	res, _ := s.handleCreateHandoff(ctx, call(map[string]any{
		"project":       "p",
		"from_agent":    "orchestrator",
		"to_agent":      "go-backend",
		"summary":       "implement the thing",
		"pending_items": []any{"step one", "step two"},
	}))
	var out struct {
		Path   string `json:"path"`
		Status string `json:"status"`
	}
	decodeResult(t, res, &out)
	if out.Status != "pending" {
		t.Fatalf("created handoff status = %q, want pending", out.Status)
	}
	return out.Path
}

func TestMCP_HandoffLifecycle(t *testing.T) {
	s, v, _ := newTestServer(t)
	ctx := context.Background() // implicit admin

	path := createTestHandoff(t, s, ctx)

	// Listed as pending, addressed to go-backend.
	var list handoffListResult
	res, _ := s.handlePendingHandoffs(ctx, call(map[string]any{"project": "p", "for_agent": "go-backend"}))
	decodeResult(t, res, &list)
	if len(list.Handoffs) != 1 || list.Handoffs[0].Status != "pending" {
		t.Fatalf("pending list = %+v, want 1 pending entry", list.Handoffs)
	}

	// Claim: status flips, identity is server-stamped.
	var claim struct {
		Status    string `json:"status"`
		ClaimedBy string `json:"claimed_by"`
		ClaimedAt string `json:"claimed_at"`
	}
	res, _ = s.handleClaimHandoff(ctx, call(map[string]any{"path": path}))
	decodeResult(t, res, &claim)
	if claim.Status != "claimed" || claim.ClaimedBy != "implicit-admin@admin" || claim.ClaimedAt == "" {
		t.Fatalf("claim = %+v", claim)
	}

	// Default (pending) list no longer shows it; status=claimed does.
	res, _ = s.handlePendingHandoffs(ctx, call(map[string]any{"project": "p"}))
	decodeResult(t, res, &list)
	if len(list.Handoffs) != 0 {
		t.Fatalf("pending list after claim = %+v, want empty", list.Handoffs)
	}
	res, _ = s.handlePendingHandoffs(ctx, call(map[string]any{"project": "p", "status": "claimed"}))
	decodeResult(t, res, &list)
	if len(list.Handoffs) != 1 || list.Handoffs[0].ClaimedBy != "implicit-admin@admin" {
		t.Fatalf("claimed list = %+v", list.Handoffs)
	}

	// Double claim fails with the claimer in the message.
	res, _ = s.handleClaimHandoff(ctx, call(map[string]any{"path": path}))
	if msg := expectError(t, res); !strings.Contains(msg, "already claimed by implicit-admin@admin") {
		t.Fatalf("double claim error = %q", msg)
	}

	// Complete with outcome note.
	res, _ = s.handleCompleteHandoff(ctx, call(map[string]any{
		"path": path, "outcome": "done", "note": "shipped in commit abc123",
	}))
	var done struct {
		Status      string `json:"status"`
		CompletedBy string `json:"completed_by"`
	}
	decodeResult(t, res, &done)
	if done.Status != "done" || done.CompletedBy != "implicit-admin@admin" {
		t.Fatalf("complete = %+v", done)
	}

	// Note content: status + tag rewritten, Outcome section appended.
	note, err := v.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(note.Content)
	for _, want := range []string{"status: done", "status:done", "created_by: implicit-admin@admin", "claimed_by: implicit-admin@admin", "completed_at: ", "## Outcome\n\nshipped in commit abc123"} {
		if !strings.Contains(body, want) {
			t.Fatalf("note body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "status:pending") || strings.Contains(body, "status: pending") {
		t.Fatalf("note body still carries pending markers:\n%s", body)
	}

	// A done handoff can be neither claimed nor completed again.
	res, _ = s.handleClaimHandoff(ctx, call(map[string]any{"path": path}))
	if msg := expectError(t, res); !strings.Contains(msg, `"done", not pending`) {
		t.Fatalf("claim-after-done error = %q", msg)
	}
	res, _ = s.handleCompleteHandoff(ctx, call(map[string]any{"path": path, "outcome": "rejected"}))
	if msg := expectError(t, res); !strings.Contains(msg, "not claimed") {
		t.Fatalf("complete-after-done error = %q", msg)
	}

	// Invalid status filter and invalid outcome are rejected.
	res, _ = s.handlePendingHandoffs(ctx, call(map[string]any{"project": "p", "status": "bogus"}))
	expectError(t, res)
	res, _ = s.handleCompleteHandoff(ctx, call(map[string]any{"path": path, "outcome": "maybe"}))
	expectError(t, res)
}

// Among N concurrent claimants of the same handoff exactly one must win —
// the pending check runs under the per-note path lock.
func TestMCP_ClaimHandoffConcurrent(t *testing.T) {
	s, _, _ := newTestServer(t)
	path := createTestHandoff(t, s, context.Background())

	const n = 8
	var wg sync.WaitGroup
	success := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok := projectToken("tok"+string(rune('a'+i)), "agent-"+string(rune('a'+i)))
			r, _ := s.handleClaimHandoff(ctxWithToken(tok), call(map[string]any{"path": path}))
			success[i] = r != nil && !r.IsError
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, ok := range success {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly 1 winning claim, got %d", winners)
	}
}

func TestMCP_CompleteHandoffRequiresClaimer(t *testing.T) {
	s, _, _ := newTestServer(t)
	tokA := projectToken("aaaa1111", "agent-a")
	tokB := projectToken("bbbb2222", "agent-b")
	ctxA, ctxB := ctxWithToken(tokA), ctxWithToken(tokB)

	// A creates and claims. created_by carries A's token identity.
	path := createTestHandoff(t, s, ctxA)
	var list handoffListResult
	res, _ := s.handlePendingHandoffs(ctxA, call(map[string]any{"project": "p"}))
	decodeResult(t, res, &list)
	if len(list.Handoffs) != 1 || list.Handoffs[0].CreatedBy != "agent-a@aaaa1111" {
		t.Fatalf("pending list = %+v, want created_by agent-a@aaaa1111", list.Handoffs)
	}
	res, _ = s.handleClaimHandoff(ctxA, call(map[string]any{"path": path}))
	resultText(t, res)

	// B (different project token) cannot complete it.
	res, _ = s.handleCompleteHandoff(ctxB, call(map[string]any{"path": path, "outcome": "done"}))
	if msg := expectError(t, res); !strings.Contains(msg, "only the claimer or an admin token") {
		t.Fatalf("foreign complete error = %q", msg)
	}

	// The claimer can.
	res, _ = s.handleCompleteHandoff(ctxA, call(map[string]any{"path": path, "outcome": "done"}))
	resultText(t, res)

	// Admin override: A claims, implicit admin completes on its behalf.
	path2 := createTestHandoff(t, s, ctxA)
	res, _ = s.handleClaimHandoff(ctxA, call(map[string]any{"path": path2}))
	resultText(t, res)
	res, _ = s.handleCompleteHandoff(context.Background(), call(map[string]any{"path": path2, "outcome": "rejected"}))
	var out struct {
		Status string `json:"status"`
	}
	decodeResult(t, res, &out)
	if out.Status != "rejected" {
		t.Fatalf("admin override complete = %+v", out)
	}
}

func TestMCP_ClaimRejectsNonHandoffNotes(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/plain.md", "content": "# Plain\nnot a handoff"}))
	resultText(t, res)
	res, _ = s.handleClaimHandoff(ctx, call(map[string]any{"path": "p/plain.md"}))
	if msg := expectError(t, res); !strings.Contains(msg, "not a handoff note") {
		t.Fatalf("claim on plain note error = %q", msg)
	}
}

// For one status, the handoffs read off the disk are those the index has
// with it, inside the project's handoffs folder (IMP-161, S3-11).
func TestHandoffCandidates_ByStatusFromTheIndex(t *testing.T) {
	s, _, _ := newTestServer(t)
	for rel, status := range map[string]string{
		"p/handoffs/a.md":     "pending",
		"p/handoffs/b.md":     "done",
		"p/handoffs/old/c.md": "pending",
		"p/notes/d.md":        "pending",
		"q/handoffs/e.md":     "pending",
	} {
		content := "---\ntitle: H\ntype: handoff\nstatus: " + status + "\ntags: [type:handoff]\n---\n\n## Summary\n\nx\n"
		if err := s.writeAndIndex(rel, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.handoffCandidates("p", "pending")
	var got []string
	for _, r := range rows {
		got = append(got, r.Path)
	}
	if err != nil || strings.Join(got, ",") != "p/handoffs/a.md,p/handoffs/old/c.md" {
		t.Errorf("candidates = %v, %v", got, err)
	}
}
