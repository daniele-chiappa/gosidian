package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
)

// seedAuditLog wires a fresh audit log into the server and populates it with
// a known mix of entries. Returns the log so tests can add more if needed.
func seedAuditLog(t *testing.T, s *Server) *audit.Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(l)

	base := time.Now().Add(-2 * time.Hour)
	entries := []audit.Entry{
		{TS: base.Add(5 * time.Minute), Source: audit.SourceHTTP, Actor: "alice", Action: audit.ActionCreate, Path: "projA/x.md", Size: 10},
		{TS: base.Add(10 * time.Minute), Source: audit.SourceMCP, Actor: "bob", Action: audit.ActionUpdate, Path: "projA/y.md", Size: 20},
		{TS: base.Add(15 * time.Minute), Source: audit.SourceMCP, Actor: "bob", Action: audit.ActionAppend, Path: "projB/z.md", Size: 30},
		{TS: base.Add(20 * time.Minute), Source: audit.SourceHTTP, Actor: "alice", Action: audit.ActionDelete, Path: "projB/z.md"},
	}
	for _, e := range entries {
		if err := l.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestMCP_AuditTail_NoFilters(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = seedAuditLog(t, s)

	res, err := s.handleAuditTail(context.Background(), call(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	var payload struct {
		Entries []auditEntryOut `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if len(payload.Entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(payload.Entries))
	}
}

func TestMCP_AuditTail_FilterByActor(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = seedAuditLog(t, s)

	res, _ := s.handleAuditTail(context.Background(), call(map[string]any{"actor": "alice"}))
	body := resultText(t, res)
	var payload struct {
		Entries []auditEntryOut `json:"entries"`
	}
	_ = json.Unmarshal([]byte(body), &payload)
	if len(payload.Entries) != 2 {
		t.Errorf("expected 2 alice entries, got %d: %+v", len(payload.Entries), payload.Entries)
	}
	for _, e := range payload.Entries {
		if e.Actor != "alice" {
			t.Errorf("unexpected actor %q", e.Actor)
		}
	}
}

func TestMCP_AuditTail_FilterByAction(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = seedAuditLog(t, s)

	res, _ := s.handleAuditTail(context.Background(), call(map[string]any{"action": "update"}))
	body := resultText(t, res)
	var payload struct {
		Entries []auditEntryOut `json:"entries"`
	}
	_ = json.Unmarshal([]byte(body), &payload)
	if len(payload.Entries) != 1 || payload.Entries[0].Action != "update" {
		t.Errorf("expected single update entry, got %+v", payload.Entries)
	}
}

func TestMCP_AuditTail_RejectUnknownAction(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = seedAuditLog(t, s)

	res, _ := s.handleAuditTail(context.Background(), call(map[string]any{"action": "nonsense"}))
	msg := expectError(t, res)
	if msg == "" {
		t.Error("expected non-empty error message for unknown action")
	}
}

func TestMCP_AuditTail_FilterByPathPrefix(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = seedAuditLog(t, s)

	res, _ := s.handleAuditTail(context.Background(), call(map[string]any{"path_prefix": "projB/"}))
	body := resultText(t, res)
	var payload struct {
		Entries []auditEntryOut `json:"entries"`
	}
	_ = json.Unmarshal([]byte(body), &payload)
	if len(payload.Entries) != 2 {
		t.Errorf("expected 2 projB entries, got %d", len(payload.Entries))
	}
}

func TestMCP_AuditTail_Limit(t *testing.T) {
	s, _, _ := newTestServer(t)
	_ = seedAuditLog(t, s)

	res, _ := s.handleAuditTail(context.Background(), call(map[string]any{"limit": float64(2)}))
	body := resultText(t, res)
	var payload struct {
		Entries []auditEntryOut `json:"entries"`
	}
	_ = json.Unmarshal([]byte(body), &payload)
	if len(payload.Entries) != 2 {
		t.Errorf("expected 2 (limit=2), got %d", len(payload.Entries))
	}
}

func TestMCP_AuditTail_NoAuditLogReturnsEmpty(t *testing.T) {
	s, _, _ := newTestServer(t)
	// Do NOT call SetAuditLog — s.audit stays nil.

	res, err := s.handleAuditTail(context.Background(), call(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	if body == "" {
		t.Fatal("expected non-empty body")
	}
	// Just ensure it doesn't panic and returns a well-formed empty list.
	var payload struct {
		Entries []auditEntryOut `json:"entries"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if len(payload.Entries) != 0 {
		t.Errorf("expected 0 entries without audit log, got %d", len(payload.Entries))
	}
}
