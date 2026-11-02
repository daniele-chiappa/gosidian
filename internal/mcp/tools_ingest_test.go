package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func ingestOut(t *testing.T, s *Server, ctx context.Context, args map[string]any) map[string]any {
	t.Helper()
	res, _ := s.handleIngest(ctx, call(args))
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	return out
}

func TestIngest_AutoCSVToTableNote(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetTableNotes(true)

	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "audit", "data": b64(sampleCSV), "filename": "log.csv",
		"title": "Access Log", "caption": "Accessi al portale.",
	}))
	out := resultText(t, res)
	if !strings.Contains(out, `"kind":"table"`) || !strings.Contains(out, `"rows":2`) {
		t.Errorf("expected table-note result, got: %s", out)
	}
	if _, err := s.vault.Load("audit/access-log.md"); err != nil {
		t.Errorf("table note not created: %v", err)
	}
}

func TestIngest_AutoCSVFallsBackToAttachmentWhenFlagOff(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	// table_notes off by default.
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "audit", "data": b64(sampleCSV), "filename": "log.csv",
	}))
	out := resultText(t, res)
	if !strings.Contains(out, `"kind":"attachment"`) {
		t.Errorf("expected attachment fallback, got: %s", out)
	}
	if !strings.Contains(out, "table_notes is disabled") {
		t.Errorf("fallback should carry a teaching warning, got: %s", out)
	}
}

func TestIngest_AutoImageToMediaNote(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetMediaNotes(true)

	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "shots", "data": base64.StdEncoding.EncodeToString(pngBytes),
		"filename": "home.png", "caption": "Screenshot della home.",
	}))
	out := resultText(t, res)
	if !strings.Contains(out, `"kind":"image"`) {
		t.Errorf("expected media-note result, got: %s", out)
	}
}

func TestIngest_MarkdownFromData(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	body := "---\ntitle: Report\n---\n\n# Report\n\nciao\n"
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "data": b64(body), "filename": "report.md",
	}))
	out := resultText(t, res)
	if !strings.Contains(out, `"kind":"note"`) || !strings.Contains(out, `"created":true`) {
		t.Errorf("expected note result, got: %s", out)
	}
	note, err := s.vault.Load("proj/report.md")
	if err != nil {
		t.Fatalf("note not created: %v", err)
	}
	if string(note.Content) != body {
		t.Errorf("note body mismatch:\n%s", note.Content)
	}
	// The note must be searchable right away (synchronous index upsert).
	if hits, err := s.index.Search("Report", 10); err != nil || len(hits) == 0 {
		t.Errorf("ingested note not indexed (hits=%d err=%v)", len(hits), err)
	}
}

func TestIngest_MarkdownFromBridgeConsumesStagedFile(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	bridge := t.TempDir()
	s.SetBridgeDir(bridge)
	staged := filepath.Join(bridge, "big-report.md")
	if err := os.WriteFile(staged, []byte("# Big\n\nreport body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "bridge_filename": "big-report.md",
	}))
	out := resultText(t, res)
	if !strings.Contains(out, `"path":"proj/big-report.md"`) {
		t.Errorf("unexpected path in result: %s", out)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("staged bridge file should be consumed after ingest")
	}
}

func TestIngest_MarkdownFromSourcePathOutsideRootsRejected(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	outside := filepath.Join(t.TempDir(), "x.md")
	if err := os.WriteFile(outside, []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "source_path": outside,
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "allowed upload root") {
		t.Errorf("expected allowed-roots error, got %q", msg)
	}
}

func TestIngest_HTMLGatedOnFlag(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	args := map[string]any{
		"project": "proj", "data": b64("<h1>dash</h1>"), "filename": "dash.html",
	}
	res, _ := s.handleIngest(ctx, call(args))
	if msg := expectError(t, res); !strings.Contains(msg, "html notes are disabled") {
		t.Errorf("expected html-disabled error, got %q", msg)
	}

	s.vault.SetHTMLNotes(true)
	res, _ = s.handleIngest(ctx, call(args))
	if out := resultText(t, res); !strings.Contains(out, `"path":"proj/dash.html"`) {
		t.Errorf("expected html note created, got: %s", out)
	}
}

func TestIngest_OverwriteSemantics(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	base := map[string]any{"project": "proj", "filename": "n.md"}

	mk := func(extra map[string]any) map[string]any {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	// Create.
	out := ingestOut(t, s, ctx, mk(map[string]any{"data": b64("v1\n")}))
	etag, _ := out["etag"].(string)
	if etag == "" {
		t.Fatal("create should return an etag")
	}

	// Same path again without overwrite → teaching error.
	res, _ := s.handleIngest(ctx, call(mk(map[string]any{"data": b64("v2\n")})))
	if msg := expectError(t, res); !strings.Contains(msg, "overwrite:true") {
		t.Errorf("expected overwrite hint in error, got %q", msg)
	}

	// Overwrite with stale if_match → CAS rejection.
	res, _ = s.handleIngest(ctx, call(mk(map[string]any{
		"data": b64("v2\n"), "overwrite": true, "if_match": "stale",
	})))
	if msg := expectError(t, res); !strings.Contains(msg, "etag") {
		t.Errorf("expected etag mismatch, got %q", msg)
	}

	// Overwrite with the right etag succeeds.
	out = ingestOut(t, s, ctx, mk(map[string]any{
		"data": b64("v2\n"), "overwrite": true, "if_match": etag,
	}))
	if created, _ := out["created"].(bool); created {
		t.Error("overwrite should report created:false")
	}
	if note, _ := s.vault.Load("proj/n.md"); string(note.Content) != "v2\n" {
		t.Errorf("overwrite did not replace body: %q", note.Content)
	}
}

func TestIngest_AsOverrides(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetTableNotes(true)

	// Force a CSV to a plain attachment.
	out := ingestOut(t, s, ctx, map[string]any{
		"project": "proj", "data": b64(sampleCSV), "filename": "log.csv", "as": "attachment",
	})
	if out["kind"] != "attachment" {
		t.Errorf("as:attachment not honoured: %v", out["kind"])
	}

	// as:note on a CSV is a contradiction.
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "data": b64(sampleCSV), "filename": "log.csv", "as": "note",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "as:note requires") {
		t.Errorf("expected as:note error, got %q", msg)
	}

	// .md as plain attachment is impossible (not attachable).
	res, _ = s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "data": b64("# x\n"), "filename": "x.md", "as": "attachment",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "as:note") {
		t.Errorf("expected redirect to as:note, got %q", msg)
	}
}

func TestIngest_NoSourceTeachingError(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj"}))
	msg := expectError(t, res)
	for _, want := range []string{"bridge_filename", "source_path", "attachment", "data"} {
		if !strings.Contains(msg, want) {
			t.Errorf("no-source error should mention %q, got %q", want, msg)
		}
	}
}

func TestIngest_NotePathExtensionMismatch(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "data": b64("# x\n"), "filename": "x.md", "note_path": "proj/x.html",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "does not match the source extension") {
		t.Errorf("expected extension mismatch error, got %q", msg)
	}

	// A note_path without extension inherits the source one.
	out := ingestOut(t, s, ctx, map[string]any{
		"project": "proj", "data": b64("# x\n"), "filename": "x.md", "note_path": "proj/renamed",
	})
	if out["path"] != "proj/renamed.md" {
		t.Errorf("expected proj/renamed.md, got %v", out["path"])
	}
}

// A note format off is the instance's configuration, read at start: every
// message that says it is off says so, not a project's switch in the web UI
// that does not exist (BUG-141).
func TestFormatOff_MessagesNameTheInstanceConfig(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	refused := func(res *mcplib.CallToolResult, _ error) string { return expectError(t, res) }
	answered := func(res *mcplib.CallToolResult, _ error) string { return resultText(t, res) }
	png := base64.StdEncoding.EncodeToString(pngBytes)
	for _, c := range []struct {
		name, key, msg string
	}{
		{"memory_create of a .html", "html_notes", refused(s.handleCreate(ctx, call(map[string]any{
			"path": "proj/dash.html", "content": "<h1>dash</h1>"})))},
		{"memory_ingest of a .html", "html_notes", refused(s.handleIngest(ctx, call(map[string]any{
			"project": "proj", "data": b64("<h1>dash</h1>"), "filename": "dash.html"})))},
		{"memory_create_table_note", "table_notes", refused(s.handleCreateTableNote(ctx, call(map[string]any{
			"project": "audit", "data": csvB64(sampleCSV), "filename": "log.csv", "caption": "x"})))},
		{"memory_create_media_note", "media_notes", refused(s.handleCreateMediaNote(ctx, call(map[string]any{
			"project": "pics", "data": png, "filename": "dot.png", "caption": "x"})))},
		{"memory_ingest of a .csv", "table_notes", answered(s.handleIngest(ctx, call(map[string]any{
			"project": "audit", "data": b64(sampleCSV), "filename": "log.csv"})))},
		{"memory_ingest of an image", "media_notes", answered(s.handleIngest(ctx, call(map[string]any{
			"project": "pics", "data": png, "filename": "dot.png"})))},
	} {
		for _, want := range []string{"[vault] " + c.key, "GOSIDIAN_VAULT_" + strings.ToUpper(c.key), "restart"} {
			if !strings.Contains(c.msg, want) {
				t.Errorf("%s: the message lacks %q: %s", c.name, want, c.msg)
			}
		}
		for _, wrong := range []string{"web UI", "toggle", "per-project", "flip"} {
			if strings.Contains(c.msg, wrong) {
				t.Errorf("%s: the message speaks of %q: %s", c.name, wrong, c.msg)
			}
		}
	}
}
