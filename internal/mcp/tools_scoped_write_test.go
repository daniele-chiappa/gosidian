package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

func writeVaultFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A project-scoped token must be able to use the tools that are documented
// as "forced to their project" (BUG-041): the scope check has to run on the
// concrete target path, never on "".
func TestRefreshHot_ScopedTokenAllowed(t *testing.T) {
	s, ctx := newScopedServer(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	writeVaultFile(t, s.vault.Root, "proj/hot.md", "# Hot\n\n## Recent decisions\n\n"+recentMarkerOpen+"\nstale\n"+recentMarkerClose+"\n")
	writeVaultFile(t, s.vault.Root, "proj/memory/decisions.md", "# ADR\n\n## ADR-001 Use tests\n\nbody\n")
	res, err := s.handleRefreshHot(ctx, call(map[string]any{"project": "proj"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("scoped token rejected: %s", expectError(t, res))
	}
	if !strings.Contains(resultText(t, res), `"updated":true`) {
		t.Fatalf("hot.md not refreshed: %s", resultText(t, res))
	}
}

func TestProjectScaffold_ScopedTokenAllowed(t *testing.T) {
	s, ctx := newScopedServer(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	seedTemplatesForTest(t, s.vault.Root)
	res, err := s.handleProjectScaffold(ctx, call(map[string]any{"project": "proj"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("scoped token rejected: %s", expectError(t, res))
	}
	if _, err := os.Stat(filepath.Join(s.vault.Root, "proj/hot.md")); err != nil {
		t.Fatalf("scaffold did not create proj/hot.md: %v", err)
	}
}
