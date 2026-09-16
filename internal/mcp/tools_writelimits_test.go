package mcp

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// limitedServer returns a server whose write budget (1/min) has already been
// spent by a content write, plus fixtures for the structural tools.
func limitedServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, _, dir := newTestServer(t)
	s.SetWriteLimits(1, 0)
	for _, p := range []string{"proj/a.md", "proj/attachments/x.png"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte("# a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := s.handleCreate(context.Background(), call(map[string]any{"path": "proj/warm.md", "content": "x"}))
	if res.IsError {
		t.Fatalf("warm-up write must pass: %s", expectError(t, res))
	}
	return s, dir
}

// Every mutation — not only content writes — must consult the per-token
// write limiter (BUG-035).
func TestWriteLimiter_CoversStructuralAndUploadTools(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(onePxPNG)
	cases := []struct {
		name string
		call func(s *Server) (*mcplibResult, error)
	}{
		{"delete", func(s *Server) (*mcplibResult, error) {
			return s.handleDelete(context.Background(), call(map[string]any{"path": "proj/a.md"}))
		}},
		{"rename", func(s *Server) (*mcplibResult, error) {
			return s.handleRenameNote(context.Background(), call(map[string]any{"from": "proj/a.md", "to": "proj/z.md"}))
		}},
		{"move", func(s *Server) (*mcplibResult, error) {
			return s.handleMoveNote(context.Background(), call(map[string]any{"path": "proj/a.md", "project": "other"}))
		}},
		{"delete_attachment", func(s *Server) (*mcplibResult, error) {
			return s.handleDeleteAttachment(context.Background(), call(map[string]any{"path": "proj/attachments/x.png"}))
		}},
		{"ingest attachment bytes", func(s *Server) (*mcplibResult, error) {
			return s.handleIngest(context.Background(), call(map[string]any{
				"project": "proj", "data": base64.StdEncoding.EncodeToString(png), "filename": "shot.png", "as": "attachment",
			}))
		}},
		{"mint ingest ticket", func(s *Server) (*mcplibResult, error) {
			return s.handleIngest(context.Background(), call(map[string]any{"project": "proj", "transfer": "http"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := limitedServer(t)
			res, err := tc.call(s)
			if err != nil {
				t.Fatal(err)
			}
			if msg := expectError(t, res); !strings.Contains(msg, "rate limit") {
				t.Fatalf("expected a rate-limit rejection, got: %s", msg)
			}
		})
	}
}

func TestHTTPUpload_HonoursWriteLimiter(t *testing.T) {
	s, plaintext := serverWithToken(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	s.SetWriteLimits(1, 0)
	post := func() *httptest.ResponseRecorder {
		body, ct := multipartPNG(t)
		r := httptest.NewRequest(http.MethodPost, "/upload?project=proj", body)
		r.Header.Set("Authorization", "Bearer "+plaintext)
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		s.handleHTTPUpload(w, r)
		return w
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("first upload: %d %s", w.Code, w.Body.String())
	}
	if w := post(); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second upload must hit the limiter: %d %s", w.Code, w.Body.String())
	}
}

// A token may hold only a bounded number of unredeemed tickets: minting in
// a loop must not grow the ticket map without limit.
func TestMintIngestTicket_CapPerToken(t *testing.T) {
	s, ctx := newScopedServer(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	s.SetWriteLimits(1000, 0)
	for i := 0; i < maxIngestTicketsPerToken; i++ {
		res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "transfer": "http"}))
		if res.IsError {
			t.Fatalf("mint %d: %s", i+1, expectError(t, res))
		}
	}
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "transfer": "http"}))
	if msg := expectError(t, res); !strings.Contains(msg, "pending ingest tickets") {
		t.Fatalf("expected the per-token ticket cap, got: %s", msg)
	}
}

type mcplibResult = mcplib.CallToolResult
