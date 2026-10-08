package mcp

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// A bridge_filename of "." or ".." named the bridge dir or its parent: a
// package import read it all and then removed it (BUG-102). A whole path
// still works, by its last element.
func TestBridgeFilename_NoDots(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	staged := stagePackage(t, s, "pkg", map[string]string{"a.md": "# A"})
	bridge := filepath.Dir(staged)
	for _, name := range []string{".", "..", "pkg/..", "/"} {
		res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "as": "package", "bridge_filename": name, "dest": "proj/x"}))
		if !res.IsError {
			t.Errorf("bridge_filename %q: want an error, got %s", name, resultText(t, res))
		}
		res, _ = s.handleUploadAttachment(ctx, call(map[string]any{"project": "proj", "bridge_filename": name, "filename": "a.png"}))
		if !res.IsError {
			t.Errorf("upload bridge_filename %q: want an error", name)
		}
	}
	if _, err := os.Stat(filepath.Join(staged, "a.md")); err != nil {
		t.Fatalf("the bridge dir lost its content: %v", err)
	}
	out := packageOut(t, s, ctx, map[string]any{"project": "proj", "as": "package", "bridge_filename": filepath.Join(bridge, "pkg"), "dest": "proj/x"})
	if _, err := s.vault.Load("proj/x/a.md"); err != nil {
		t.Errorf("a whole path no longer imports: %v (%v)", err, out)
	}
}

// A source_path inside the vault reads only what the token may read
// (BUG-103): the vault is always an allowed root, so a token scoped to one
// project imported another, the trash or the state dir into its own.
func TestSourcePath_InsideTheVault(t *testing.T) {
	s, ctx := newScopedServer(t, "A", []string{auth.ScopeRead, auth.ScopeWrite})
	root := s.vault.Root
	png, _ := base64.StdEncoding.DecodeString(onePxPNG)
	for rel, body := range map[string][]byte{
		"A/sub/own.md":            []byte("# own"),
		"B/secret.md":             []byte("# secret"),
		"B/attachments/x.png":     png,
		".gosidian/trash/1__x.md": []byte("# trashed"),
		"state/tokens.json":       []byte("{}"),
		"state/notes.md":          []byte("# state"),
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.vault.SetStateDir(filepath.Join(root, "state"))
	bridge := t.TempDir()
	s.SetBridgeDir(bridge)
	link := filepath.Join(bridge, "link.md")
	if err := os.Symlink(filepath.Join(root, "B", "secret.md"), link); err != nil {
		t.Fatal(err)
	}

	for name, src := range map[string]string{
		"another project": filepath.Join(root, "B"),
		"the trash":       filepath.Join(root, ".gosidian", "trash"),
		"the state dir":   filepath.Join(root, "state"),
		"the vault":       root,
	} {
		res, _ := s.handleIngest(ctx, call(map[string]any{"project": "A", "as": "package", "source_path": src, "dest": "A/copy"}))
		if !res.IsError {
			t.Errorf("package from %s: want an error, got %s", name, resultText(t, res))
		}
	}
	for name, src := range map[string]string{
		"a note of another project":  filepath.Join(root, "B", "secret.md"),
		"a link to it in the bridge": link,
		"a note in the state dir":    filepath.Join(root, "state", "notes.md"),
	} {
		res, _ := s.handleIngest(ctx, call(map[string]any{"project": "A", "source_path": src, "note_path": "A/copy.md"}))
		if !res.IsError {
			t.Errorf("note from %s: want an error, got %s", name, resultText(t, res))
		}
	}
	res, _ := s.handleUploadAttachment(ctx, call(map[string]any{"project": "A", "source_path": filepath.Join(root, "B", "attachments", "x.png")}))
	if !res.IsError {
		t.Errorf("attachment of another project: want an error, got %s", resultText(t, res))
	}
	if s.vault.Exists("A/copy.md") || s.vault.Exists("A/copy") {
		t.Error("something was imported")
	}

	// The token's own project stays a valid source.
	out := packageOut(t, s, ctx, map[string]any{"project": "A", "as": "package", "source_path": filepath.Join(root, "A", "sub"), "dest": "A/copy"})
	if _, err := s.vault.Load("A/copy/own.md"); err != nil {
		t.Errorf("own project as a source: %v (%v)", err, out)
	}
}

// A bridge dir or an upload root the operator put inside the vault is a
// staging place: what is in it imports with any token, but for the state
// dir.
func TestSourcePath_StagingInsideTheVault(t *testing.T) {
	s, ctx := newScopedServer(t, "A", []string{auth.ScopeRead, auth.ScopeWrite})
	root := s.vault.Root
	for rel, body := range map[string]string{
		".bridge/n.md":         "# staged",
		"inbox/m.md":           "# dropped",
		"inbox/state/token.md": "# state",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.SetBridgeDir(filepath.Join(root, ".bridge"))
	s.allowedUploadRoots = []string{filepath.Join(root, "inbox")}
	s.vault.SetStateDir(filepath.Join(root, "inbox", "state"))
	for src, note := range map[string]string{
		filepath.Join(root, ".bridge", "n.md"): "A/n.md",
		filepath.Join(root, "inbox", "m.md"):   "A/m.md",
	} {
		res, _ := s.handleIngest(ctx, call(map[string]any{"project": "A", "source_path": src, "note_path": note}))
		if res.IsError {
			t.Errorf("%s: %s", src, resultText(t, res))
		}
	}
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "A", "source_path": filepath.Join(root, "inbox", "state", "token.md"), "note_path": "A/t.md"}))
	if !res.IsError {
		t.Errorf("the state dir inside a staging root: want an error, got %s", resultText(t, res))
	}
}

// An attachment over the note size limit (1 MiB) and under its own 10 MiB
// uploads: the HTTP endpoint applied the note limit to it (BUG-104).
func TestHTTPUpload_LargerThanANote(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	png, _ := base64.StdEncoding.DecodeString(onePxPNG)
	big := append(png, bytes.Repeat([]byte{0}, 3<<20)...)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "big.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(big); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/upload?project=pics", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleHTTPUpload(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a 3 MiB attachment = %d %s", rec.Code, rec.Body.String())
	}
}
