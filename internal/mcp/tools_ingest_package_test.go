package mcp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/server/events"
)

// stagePackage writes a package folder in a fresh bridge dir of s.
func stagePackage(t *testing.T, s *Server, name string, files map[string]string) string {
	t.Helper()
	bridge := t.TempDir()
	s.SetBridgeDir(bridge)
	for p, body := range files {
		abs := filepath.Join(bridge, name, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(bridge, name)
}

var guide = map[string]string{
	"INDEX.md":         "# Guide\n\n- [Install](setup/install.md#ports)\n- [Gone](setup/gone.md)\n",
	"setup/install.md": "---\ntitle: Install\ntags: [proj]\n---\n\n## Ports\n\nBack to the [index](../INDEX.md).\n",
	"tool.bin":         "binary",
}

func packageOut(t *testing.T, s *Server, ctx context.Context, args map[string]any) map[string]any {
	t.Helper()
	res, err := s.handleIngest(ctx, call(args))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("result: %v", err)
	}
	return out
}

// A folder staged in the bridge dir, imported whole: notes keep their paths,
// links between them become wikilinks, a note without frontmatter gets the
// minimum, other files are left out and listed, the staged folder is
// consumed.
func TestIngestPackage_FolderFromBridge(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	staged := stagePackage(t, s, "guide", guide)

	dry := packageOut(t, s, ctx, map[string]any{"project": "proj", "as": "package", "bridge_filename": "guide", "dest": "proj/docs/guide", "dry_run": true})
	if dry["dry_run"] != true || len(dry["notes"].([]any)) != 2 {
		t.Fatalf("dry run = %v", dry)
	}
	if _, err := s.vault.Load("proj/docs/guide/INDEX.md"); err == nil {
		t.Fatal("a dry run writes nothing")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatal("a dry run keeps the staged package")
	}

	out := packageOut(t, s, ctx, map[string]any{"project": "proj", "as": "package", "bridge_filename": "guide", "dest": "proj/docs/guide"})
	if out["kind"] != "package" || out["unresolved"] != float64(1) || len(out["skipped"].([]any)) != 1 {
		t.Errorf("out = %v", out)
	}
	idx, err := s.vault.Load("proj/docs/guide/INDEX.md")
	if err != nil {
		t.Fatal(err)
	}
	if c := string(idx.Content); !strings.HasPrefix(c, "---\ntitle: \"Guide\"\ntags: [\"proj\"]\n---\n") ||
		!strings.Contains(c, "[[proj/docs/guide/setup/install#ports|Install]]") || !strings.Contains(c, "[Gone](setup/gone.md)") {
		t.Errorf("INDEX:\n%s", c)
	}
	if bl, _ := s.index.Backlinks("proj/docs/guide/setup/install.md"); len(bl) != 1 || bl[0].Path != "proj/docs/guide/INDEX.md" {
		t.Errorf("the links resolve in the index: %+v", bl)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("the staged package is consumed")
	}

	// Again: the notes exist now, so nothing is written without overwrite.
	stagePackage(t, s, "guide", guide)
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "as": "package", "bridge_filename": "guide", "dest": "proj/docs/guide"}))
	if msg := expectError(t, res); !strings.Contains(msg, "2 note(s) already exist") || !strings.Contains(msg, "nothing written") {
		t.Errorf("conflict: %q", msg)
	}
	out = packageOut(t, s, ctx, map[string]any{"project": "proj", "as": "package", "bridge_filename": "guide", "dest": "proj/docs/guide", "overwrite": true})
	notes := out["notes"].([]any)
	if len(notes) != 2 || notes[0].(map[string]any)["created"] != nil {
		t.Errorf("overwrite: %v", notes)
	}
}

// A write that fails midway takes back the notes written before it.
func TestIngestPackage_AllOrNothing(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	stagePackage(t, s, "pkg", map[string]string{"a.md": "# A", "z.md": "# Z"})
	// A folder where z.md would go: its write fails after a.md's.
	if err := os.MkdirAll(filepath.Join(s.vault.Root, "proj", "imp", "z.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "as": "package", "bridge_filename": "pkg", "dest": "proj/imp"}))
	if msg := expectError(t, res); !strings.Contains(msg, "taken back") {
		t.Errorf("err = %q", msg)
	}
	if _, err := s.vault.Load("proj/imp/a.md"); err == nil {
		t.Error("a.md should have been taken back")
	}
	if hits, _, _ := s.index.Query(index.QueryOptions{Folders: []string{"proj/imp"}}); len(hits) != 0 {
		t.Errorf("index keeps %d notes", len(hits))
	}
}

func TestIngestPackage_Refusals(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	bridge := stagePackage(t, s, "pkg", map[string]string{"a.md": "# A"})
	if err := os.WriteFile(filepath.Join(bridge, "..", "plain.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{
		"no dest":         {"bridge_filename": "pkg"},
		"dest elsewhere":  {"bridge_filename": "pkg", "dest": "other/x"},
		"no source":       {"dest": "proj/x"},
		"missing":         {"bridge_filename": "nope", "dest": "proj/x"},
		"outside roots":   {"source_path": t.TempDir(), "dest": "proj/x"},
		"not a package":   {"bridge_filename": "plain.txt", "dest": "proj/x"},
		"from attachment": {"attachment": "proj/attachments/x.png", "dest": "proj/x"},
	} {
		args["project"], args["as"] = "proj", "package"
		res, _ := s.handleIngest(ctx, call(args))
		if !res.IsError {
			t.Errorf("%s: want an error, got %s", name, resultText(t, res))
		}
	}
}

// A .zip uploaded with transfer:"http" (remote agents): the ticket keeps the
// package intent, the upload is imported.
func TestIngestPackage_ZipViaTicket(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	h := s.Handler("")
	out := ingestOut(t, s, ctx, map[string]any{"project": "proj", "transfer": "http", "as": "package", "dest": "proj/docs/zipped"})
	endpoint, _ := out["endpoint"].(string)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"pack/INDEX.md": "# Zip\n\n[a](a.md)\n", "pack/a.md": "# A\n"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	code, body := postTicket(t, h, endpoint, "pack.zip", buf.Bytes())
	if code != http.StatusOK {
		t.Fatalf("redeem: HTTP %d — %s", code, body)
	}
	idx, err := s.vault.Load("proj/docs/zipped/INDEX.md")
	if err != nil {
		t.Fatalf("not imported: %v (%s)", err, body)
	}
	if !strings.Contains(string(idx.Content), "[[proj/docs/zipped/a|a]]") {
		t.Errorf("INDEX:\n%s", idx.Content)
	}
}

// A package that overwrites notes goes through the shrink guard of
// memory_update, and every note it writes sends its note event with the
// new ETag, so an open editor learns it changed (BUG-113, S3-5).
func TestIngestPackage_OverwriteGuardAndEvents(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	big := "# Big\n\n" + strings.Repeat("a line of a note that matters\n", 200)
	if err := s.vault.Save("proj/imp/big.md", []byte(big)); err != nil {
		t.Fatal(err)
	}
	hub := events.New(events.HubOptions{BufLen: 64})
	s.SetEvents(hub)
	sub := hub.Subscribe(events.TopicNote)
	defer sub.Unsubscribe()

	stagePackage(t, s, "pkg", map[string]string{"big.md": "x", "new.md": "# New"})
	res, _ := s.handleIngest(ctx, call(map[string]any{"project": "proj", "as": "package", "bridge_filename": "pkg", "dest": "proj/imp", "overwrite": true}))
	if !res.IsError || !strings.Contains(toolErrorText(res), "shrink") {
		t.Fatalf("an emptying overwrite: want a refusal, got %+v", res)
	}
	if n, _ := s.vault.Load("proj/imp/big.md"); string(n.Content) != big {
		t.Fatal("the note was emptied")
	}
	if s.vault.Exists("proj/imp/new.md") {
		t.Fatal("a refused package wrote a note")
	}

	out := packageOut(t, s, ctx, map[string]any{"project": "proj", "as": "package", "bridge_filename": "pkg", "dest": "proj/imp", "overwrite": true, "allow_shrink": true})
	if out["kind"] != "package" {
		t.Fatalf("with allow_shrink: %v", out)
	}
	seen := map[string]string{}
	for len(seen) < 2 {
		select {
		case ev := <-sub.Ch:
			var p struct{ Action, Path, Etag string }
			_ = json.Unmarshal(ev.Data, &p)
			seen[p.Path] = p.Action + ":" + p.Etag
		case <-time.After(time.Second):
			t.Fatalf("note events = %v, want one per note", seen)
		}
	}
	if !strings.HasPrefix(seen["proj/imp/big.md"], "update:") || !strings.HasPrefix(seen["proj/imp/new.md"], "create:") || strings.HasSuffix(seen["proj/imp/big.md"], ":") {
		t.Errorf("note events = %v", seen)
	}
}
