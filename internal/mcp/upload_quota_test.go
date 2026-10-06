package mcp

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/uploadquota"
)

// quotaServer is a server whose upload quota fits one 1-pixel PNG and not
// two, for a token owned by account u1.
func quotaServer(t *testing.T) (*Server, context.Context, []byte, string) {
	t.Helper()
	s, plaintext := serverWithToken(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	png, _ := base64.StdEncoding.DecodeString(onePxPNG)
	s.SetUploadQuota(uploadquota.New(int64(len(png))+10, time.Hour))
	tok := s.tokens.List()[0]
	tok.OwnerUserID = "u1"
	return s, context.WithValue(context.Background(), tokenCtxKey, &tok), png, plaintext
}

// A second upload over the account's quota is refused, and nothing is
// stored; the refusal says so (IMP-034).
func TestUploadQuota_Tools(t *testing.T) {
	s, ctx, png, _ := quotaServer(t)
	data := base64.StdEncoding.EncodeToString(png)
	if res, _ := s.handleUploadAttachment(ctx, call(map[string]any{"project": "p", "data": data, "filename": "a.png"})); res.IsError {
		t.Fatalf("first upload: %s", expectError(t, res))
	}
	res, _ := s.handleUploadResource(ctx, call(map[string]any{"project": "p", "data": data, "filename": "b.png"}))
	if msg := expectError(t, res); !strings.Contains(msg, "upload quota exceeded") || !strings.Contains(msg, "Nothing was stored") {
		t.Fatalf("second upload = %q", msg)
	}
	res, _ = s.handleIngest(ctx, call(map[string]any{"project": "p", "data": data, "filename": "c.png", "as": "attachment"}))
	if msg := expectError(t, res); !strings.Contains(msg, "upload quota exceeded") {
		t.Errorf("ingest over the quota = %q", msg)
	}
	// Another account has its own quota.
	other := s.tokens.List()[0]
	other.OwnerUserID = "u2"
	octx := context.WithValue(context.Background(), tokenCtxKey, &other)
	if res, _ := s.handleUploadAttachment(octx, call(map[string]any{"project": "p", "data": data, "filename": "d.png"})); res.IsError {
		t.Errorf("another account: %s", expectError(t, res))
	}
}

// A store that fails gives its bytes back.
func TestUploadQuota_RefundOnFailure(t *testing.T) {
	s, ctx, png, _ := quotaServer(t)
	notPNG := base64.StdEncoding.EncodeToString(append([]byte("not an image at all, "), png[:20]...))
	if res, _ := s.handleUploadAttachment(ctx, call(map[string]any{"project": "p", "data": notPNG, "filename": "x.png"})); !res.IsError {
		t.Fatal("a fake PNG was stored")
	}
	data := base64.StdEncoding.EncodeToString(png)
	if res, _ := s.handleUploadAttachment(ctx, call(map[string]any{"project": "p", "data": data, "filename": "a.png"})); res.IsError {
		t.Errorf("after a failed upload: %s", expectError(t, res))
	}
}

// A source_path outside the allowed roots is refused for what it is: the
// quota's refusal would tell the file's size.
func TestUploadQuota_SourcePathOutsideRoots(t *testing.T) {
	s, ctx, _, _ := quotaServer(t)
	s.SetUploadQuota(uploadquota.New(1, time.Hour))
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, make([]byte, 500), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleUploadAttachment(ctx, call(map[string]any{"project": "p", "source_path": outside}))
	msg := expectError(t, res)
	if strings.Contains(msg, "quota") || strings.Contains(msg, "500") {
		t.Errorf("the refusal tells about a file outside the roots: %q", msg)
	}
}

// POST /mcp/upload over the quota answers 429 with Retry-After.
func TestUploadQuota_HTTPUpload(t *testing.T) {
	s, _, _, plaintext := quotaServer(t)
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
	w := post()
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "upload quota exceeded") {
		t.Fatalf("second upload: %d %s", w.Code, w.Body.String())
	}
	if ra, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || ra < 1 {
		t.Errorf("Retry-After = %q", w.Header().Get("Retry-After"))
	}
}

// The bootstrap tells an agent about the quota, and only when there is one.
func TestUploadQuota_Capabilities(t *testing.T) {
	s, _, _, _ := quotaServer(t)
	if c := s.buildCapabilities(false).Attachments.(bootstrapAttachCapability); c.QuotaBytes == 0 || c.QuotaWindow != "1h0m0s" {
		t.Errorf("capabilities = %d %q", c.QuotaBytes, c.QuotaWindow)
	}
	s.SetUploadQuota(uploadquota.New(0, 0))
	if c := s.buildCapabilities(false).Attachments.(bootstrapAttachCapability); c.QuotaBytes != 0 || c.QuotaWindow != "" {
		t.Errorf("no quota, capabilities = %d %q", c.QuotaBytes, c.QuotaWindow)
	}
}
