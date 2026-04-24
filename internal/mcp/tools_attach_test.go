package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCP_UploadAttachment(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()

	data := base64.StdEncoding.EncodeToString([]byte("fake-png-data"))
	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"data":     data,
		"filename": "photo.png",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var out struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Path, "attachments/") {
		t.Errorf("expected attachments/ prefix, got %q", out.Path)
	}
	if !strings.HasSuffix(out.Path, ".png") {
		t.Errorf("expected .png suffix, got %q", out.Path)
	}
	if !strings.Contains(out.Markdown, "![](") {
		t.Errorf("image should use ![]() syntax: %q", out.Markdown)
	}
	// File exists on disk
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(out.Path))); err != nil {
		t.Errorf("file not saved: %v", err)
	}
}

func TestMCP_UploadAttachmentWithProject(t *testing.T) {
	s, v, _ := newTestServer(t)
	ctx := context.Background()
	if _, err := v.CreateProject("Work"); err != nil {
		t.Fatal(err)
	}

	data := base64.StdEncoding.EncodeToString([]byte("pdf-content"))
	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"data":     data,
		"filename": "report.pdf",
		"project":  "Work",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var out struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Path, "Work/attachments/") {
		t.Errorf("expected Work/attachments/ prefix, got %q", out.Path)
	}
	// PDF should use link syntax, not image syntax
	if strings.Contains(out.Markdown, "![](") {
		t.Errorf("PDF should use []() syntax, not ![](): %q", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "[report.pdf]") {
		t.Errorf("markdown should reference original filename: %q", out.Markdown)
	}
}

func TestMCP_UploadAttachmentRejectsBadType(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	data := base64.StdEncoding.EncodeToString([]byte("MZ"))
	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"data":     data,
		"filename": "evil.exe",
	}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, res)
}

func TestMCP_UploadAttachmentRejectsBadBase64(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"data":     "not-valid-base64!!!",
		"filename": "test.png",
	}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, res)
}

func TestMCP_ListAttachments(t *testing.T) {
	s, v, dir := newTestServer(t)
	ctx := context.Background()

	// Seed an attachment directly on disk.
	attDir := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attDir, "abc123.png"), []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = v // keep reference

	res, err := s.handleListAttachments(ctx, call(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var out struct {
		Attachments []struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
			MIME string `json:"mime"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(out.Attachments))
	}
	if out.Attachments[0].Path != "attachments/abc123.png" {
		t.Errorf("path = %q", out.Attachments[0].Path)
	}
	if out.Attachments[0].MIME != "image/png" {
		t.Errorf("mime = %q", out.Attachments[0].MIME)
	}
}

func TestMCP_DeleteAttachment(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()

	// Seed an attachment.
	attDir := filepath.Join(dir, "attachments")
	os.MkdirAll(attDir, 0o755)
	fpath := filepath.Join(attDir, "todelete.png")
	os.WriteFile(fpath, []byte("img"), 0o644)

	res, err := s.handleDeleteAttachment(ctx, call(map[string]any{
		"path": "attachments/todelete.png",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, `"deleted":true`) && !strings.Contains(text, `"deleted": true`) {
		t.Errorf("expected deleted:true, got %s", text)
	}
	// File should be gone.
	if _, err := os.Stat(fpath); !os.IsNotExist(err) {
		t.Errorf("file still exists after delete")
	}
}

func TestMCP_DeleteAttachmentRejectsNonAttachment(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	res, err := s.handleDeleteAttachment(ctx, call(map[string]any{
		"path": "notes/secret.md",
	}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, res)
}

func TestMCP_AttachmentInfo(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()

	// Seed an attachment and a note that references it.
	attDir := filepath.Join(dir, "attachments")
	os.MkdirAll(attDir, 0o755)
	os.WriteFile(filepath.Join(attDir, "info123.png"), []byte("image-data"), 0o644)

	// Create a note that references the attachment.
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "test-note.md",
		"content": "# Test\n\n![](/vault-files/attachments/info123.png)\n",
	}))

	res, err := s.handleAttachmentInfo(ctx, call(map[string]any{
		"path": "attachments/info123.png",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var out struct {
		Path         string   `json:"path"`
		Size         int64    `json:"size"`
		MIME         string   `json:"mime"`
		ReferencedBy []string `json:"referenced_by"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Path != "attachments/info123.png" {
		t.Errorf("path = %q", out.Path)
	}
	if out.Size != 10 {
		t.Errorf("size = %d, want 10", out.Size)
	}
	if out.MIME != "image/png" {
		t.Errorf("mime = %q", out.MIME)
	}
	if len(out.ReferencedBy) != 1 || out.ReferencedBy[0] != "test-note.md" {
		t.Errorf("referenced_by = %v, want [test-note.md]", out.ReferencedBy)
	}
}

func TestMCP_UploadAttachmentFromSourcePath(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()

	// The vault root (dir) is always an implicit allowed root.
	// Create a file inside the vault to upload from.
	srcFile := filepath.Join(dir, ".uploads", "report.pdf")
	os.MkdirAll(filepath.Dir(srcFile), 0o755)
	os.WriteFile(srcFile, []byte("pdf-data-here"), 0o644)

	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"source_path": srcFile,
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var out struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Path, "attachments/") {
		t.Errorf("expected attachments/ prefix, got %q", out.Path)
	}
	if !strings.HasSuffix(out.Path, ".pdf") {
		t.Errorf("expected .pdf suffix, got %q", out.Path)
	}
	// PDF should use link syntax with basename
	if !strings.Contains(out.Markdown, "[report.pdf]") {
		t.Errorf("markdown should reference original filename: %q", out.Markdown)
	}
	// File exists on disk
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(out.Path))); err != nil {
		t.Errorf("file not saved: %v", err)
	}
}

func TestMCP_UploadAttachmentFromSourcePathWithProject(t *testing.T) {
	s, v, dir := newTestServer(t)
	ctx := context.Background()
	if _, err := v.CreateProject("Work"); err != nil {
		t.Fatal(err)
	}

	srcFile := filepath.Join(dir, ".uploads", "data.xlsx")
	os.MkdirAll(filepath.Dir(srcFile), 0o755)
	os.WriteFile(srcFile, []byte("xlsx-data"), 0o644)

	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"source_path": srcFile,
		"filename":    "custom-name.xlsx",
		"project":     "Work",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)

	var out struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	json.Unmarshal([]byte(text), &out)
	if !strings.HasPrefix(out.Path, "Work/attachments/") {
		t.Errorf("expected Work/attachments/ prefix, got %q", out.Path)
	}
	if !strings.Contains(out.Markdown, "[custom-name.xlsx]") {
		t.Errorf("markdown should use override filename: %q", out.Markdown)
	}
}

func TestMCP_UploadAttachmentFromSourcePathBlocked(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// Try to read from a path outside the vault (and no extra roots configured).
	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"source_path": "/etc/passwd",
	}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, res)
}

func TestMCP_UploadAttachmentRequiresDataOrPath(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	res, err := s.handleUploadAttachment(ctx, call(map[string]any{
		"filename": "test.png",
	}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, res)
}
