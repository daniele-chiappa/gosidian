package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
)

// With the project's lean_read_bootstrap flag on, a token that cannot write
// gets the reading half of the directives and only the download path among
// the attachment capabilities. With the flag off (the default), and for
// write tokens always, the payload is the full one.
func TestBootstrap_LeanReadBootstrapIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		lean   bool // flag on the project
		want   bool // lean payload expected
	}{
		{"read, flag off", []string{auth.ScopeRead}, false, false},
		{"read, flag on", []string{auth.ScopeRead}, true, true},
		{"read+write, flag on", []string{auth.ScopeRead, auth.ScopeWrite}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := newScopedServer(t, "Work", tc.scopes)
			pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := pstore.Set("Work", projects.Flags{LeanReadBootstrap: tc.lean}); err != nil {
				t.Fatal(err)
			}
			s.SetProjects(pstore)

			res, err := s.handleBootstrap(ctx, call(map[string]any{"project": "Work"}))
			if err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			if err := json.Unmarshal([]byte(resultText(t, res)), &p); err != nil {
				t.Fatal(err)
			}
			block, _ := p["directives_block"].(string)
			attach, _ := p["capabilities"].(map[string]any)["attachments"].(map[string]any)
			scope, hasScope := p["directives_scope"]
			if tc.want {
				if scope != "read" {
					t.Errorf("directives_scope = %v, want read", scope)
				}
				for _, want := range []string{"### Mappa delle cartelle del vault", "### Vocabolario tag", "### Economia dei token", "sola lettura", "<!-- /gosidian:directives -->"} {
					if !strings.Contains(block, want) {
						t.Errorf("lean block misses %q", want)
					}
				}
				for _, gone := range []string{"### Workflow end-of-task", "ingest rules", "### Handoff fra agenti"} {
					if strings.Contains(block, gone) {
						t.Errorf("lean block still carries %q", gone)
					}
				}
				if len(attach) != 1 || attach["download_endpoint_hint"] == "" {
					t.Errorf("lean attachments = %v, want the download hint only", attach)
				}
				return
			}
			if hasScope {
				t.Errorf("full payload carries directives_scope %v", scope)
			}
			if !strings.Contains(block, "### Workflow end-of-task") {
				t.Error("full payload lost the end-of-task workflow")
			}
			if attach["upload_endpoint_hint"] == nil || attach["append_endpoint_hint"] == nil {
				t.Errorf("full attachments lost the upload/append hints: %v", attach)
			}
		})
	}
}
