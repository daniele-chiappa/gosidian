package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
)

// postTicket POSTs body as multipart field "file" to the ticket endpoint on
// the server's HTTP handler and returns the status code plus response body.
func postTicket(t *testing.T, h http.Handler, endpoint, filename string, body []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, endpoint, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	respBody, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(respBody)
}

func TestIngestTicket_E2ETableNote(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetTableNotes(true)
	h := s.Handler("")

	out := ingestOut(t, s, ctx, map[string]any{
		"project": "audit", "transfer": "http",
		"title": "Remote Log", "caption": "CSV arrivato via ticket.",
	})
	endpoint, _ := out["endpoint"].(string)
	if !strings.HasPrefix(endpoint, "/ingest/") {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
	if out["single_use"] != true {
		t.Error("ticket should be marked single_use")
	}

	code, body := postTicket(t, h, endpoint, "log.csv", []byte(sampleCSV))
	if code != http.StatusOK {
		t.Fatalf("redeem: HTTP %d — %s", code, body)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("parse redemption body: %v", err)
	}
	if res["kind"] != "table" {
		t.Errorf("expected table note from redemption, got %v", res["kind"])
	}
	if _, err := s.vault.Load("audit/remote-log.md"); err != nil {
		t.Errorf("table note not created: %v", err)
	}

	// Single-use: the same ticket is gone.
	code, body = postTicket(t, h, endpoint, "log.csv", []byte(sampleCSV))
	if code != http.StatusNotFound || !strings.Contains(body, "single-use") {
		t.Errorf("second redemption should 404 with the single-use hint, got %d — %s", code, body)
	}
}

func TestIngestTicket_TransferRejectsInlineSource(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "transfer": "http", "data": b64("x"), "filename": "x.txt",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "do not pass a source") {
		t.Errorf("expected inline-source rejection, got %q", msg)
	}
}

func TestIngestTicket_Expired(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.ingestTicketTTL = time.Nanosecond
	h := s.Handler("")

	out := ingestOut(t, s, ctx, map[string]any{"project": "proj", "transfer": "http"})
	endpoint, _ := out["endpoint"].(string)
	time.Sleep(2 * time.Millisecond)

	code, body := postTicket(t, h, endpoint, "x.md", []byte("# x\n"))
	if code != http.StatusGone {
		t.Errorf("expired ticket should 410, got %d — %s", code, body)
	}
}

func TestIngestTicket_NoteViaTicket(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	h := s.Handler("")

	out := ingestOut(t, s, ctx, map[string]any{
		"project": "proj", "transfer": "http", "note_path": "proj/report.md",
	})
	endpoint, _ := out["endpoint"].(string)

	code, body := postTicket(t, h, endpoint, "report.md", []byte("# Report\n\nvia ticket\n"))
	if code != http.StatusOK {
		t.Fatalf("redeem: HTTP %d — %s", code, body)
	}
	note, err := s.vault.Load("proj/report.md")
	if err != nil {
		t.Fatalf("note not created: %v", err)
	}
	if !strings.Contains(string(note.Content), "via ticket") {
		t.Errorf("note body mismatch: %q", note.Content)
	}
}

func TestIngestURL_DisabledWithoutAllowlist(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "url": "http://127.0.0.1:1/x.csv",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "url ingestion is disabled") {
		t.Errorf("expected disabled error, got %q", msg)
	}
}

func TestIngestURL_E2ETableNote(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.vault.SetTableNotes(true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleCSV))
	}))
	defer ts.Close()
	s.SetIngestURLAllowlist([]string{ts.URL})

	out := ingestOut(t, s, ctx, map[string]any{
		"project": "audit", "url": ts.URL + "/export.csv", "caption": "Export via URL.",
	})
	if out["kind"] != "table" {
		t.Errorf("expected table note, got %v", out["kind"])
	}
	if _, err := s.vault.Load("audit/export.md"); err != nil {
		t.Errorf("table note not created: %v", err)
	}
}

func TestIngestURL_OutsideAllowlist(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.SetIngestURLAllowlist([]string{"http://10.0.0.1:9999/"})
	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "url": "http://127.0.0.1:1/x.csv",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "not inside the ingest URL allowlist") {
		t.Errorf("expected allowlist rejection, got %q", msg)
	}
}

func TestIngestURL_RedirectOutsideAllowlistRejected(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleCSV))
	}))
	defer outside.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, outside.URL+"/x.csv", http.StatusFound)
	}))
	defer redirector.Close()
	s.SetIngestURLAllowlist([]string{redirector.URL})

	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "url": redirector.URL + "/x.csv",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "outside the ingest URL allowlist") {
		t.Errorf("expected redirect rejection, got %q", msg)
	}
}

func TestIngestURL_TooLarge(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	big := bytes.Repeat([]byte("a"), (10<<20)+1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	}))
	defer ts.Close()
	s.SetIngestURLAllowlist([]string{ts.URL})

	res, _ := s.handleIngest(ctx, call(map[string]any{
		"project": "proj", "url": ts.URL + "/big.txt",
	}))
	if msg := expectError(t, res); !strings.Contains(msg, "too large") {
		t.Errorf("expected size rejection, got %q", msg)
	}
}

func TestCapabilities_SurfaceBridgeDirAndIngestURL(t *testing.T) {
	s, _, _ := newTestServer(t)
	attachments := func() bootstrapAttachCapability {
		return s.buildCapabilities(false).Attachments.(bootstrapAttachCapability)
	}
	caps := attachments()
	if caps.BridgeDir != "" || caps.IngestURLEnabled {
		t.Errorf("unconfigured server should not advertise bridge/url: %+v", caps)
	}

	bridge := t.TempDir()
	s.SetBridgeDir(bridge)
	s.SetAllowedUploadRoots([]string{"/srv/exports"})
	s.SetIngestURLAllowlist([]string{"http://127.0.0.1:4001/"})
	caps = attachments()
	if caps.BridgeDir != bridge {
		t.Errorf("bridge_dir = %q, want %q", caps.BridgeDir, bridge)
	}
	if len(caps.AllowedUploadRoots) != 1 || caps.AllowedUploadRoots[0] != "/srv/exports" {
		t.Errorf("allowed_upload_roots = %v", caps.AllowedUploadRoots)
	}
	if !caps.IngestURLEnabled {
		t.Error("ingest_url_enabled should be true with a non-empty allowlist")
	}
	if len(caps.Tools) == 0 || caps.Tools[0] != "memory_ingest" {
		t.Errorf("tools should lead with memory_ingest: %v", caps.Tools)
	}
}

// Regression: with both transports mounted (single-port /mcp + legacy ""),
// a ticket minted over the /mcp session must advertise the /mcp-prefixed
// endpoint — a shared Server field let the last Handler() call win and the
// mint over /mcp/sse pointed at the SPA fallback instead (caught by the
// v2.19.0 prod smoke).
func TestIngestTicket_EndpointCarriesTransportBasePath(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	h := s.Handler("/mcp") // single-port mux
	_ = s.Handler("")      // legacy listener mounted after — must not clobber

	mcpCtx := context.WithValue(ctx, basePathCtxKey, "/mcp")
	res, _ := s.handleIngest(mcpCtx, call(map[string]any{
		"project": "proj", "transfer": "http", "note_path": "proj/from-mcp.md",
	}))
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := out["endpoint"].(string)
	if !strings.HasPrefix(endpoint, "/mcp/ingest/") {
		t.Fatalf("endpoint %q should carry the /mcp transport prefix", endpoint)
	}

	code, body := postTicket(t, h, endpoint, "from-mcp.md", []byte("# ok\n"))
	if code != http.StatusOK {
		t.Fatalf("redeem on the advertised endpoint: HTTP %d — %s", code, body)
	}
	if _, err := s.vault.Load("proj/from-mcp.md"); err != nil {
		t.Errorf("note not created: %v", err)
	}
}
