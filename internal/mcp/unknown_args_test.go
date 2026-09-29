package mcp

import (
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// An argument a tool does not declare is still ignored, but the result now
// says so and guesses the intended name; declared arguments add no note.
func TestUnknownArgs_NotedInResult(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	h := s.Handler("/mcp")
	init := postMCP(t, h, token, "", initializeBody)
	session := init.Header().Get("Mcp-Session-Id")

	rec := postMCP(t, h, token, session, rpcToolCall(2, "memory_list_notes", map[string]any{"projetcs": "x", "Project": "y"}))
	body := rec.Body.String()
	if !strings.Contains(body, `ignored unknown argument(s)`) || !strings.Contains(body, `\"projetcs\"`) {
		t.Fatalf("no unknown-argument note: %s", body)
	}
	if !strings.Contains(body, `\"Project\" (did you mean \"project\"?)`) {
		t.Errorf("no guess for a case-mismatched name: %s", body)
	}

	rec = postMCP(t, h, token, session, rpcToolCall(3, "memory_list_notes", map[string]any{"project": "x"}))
	if strings.Contains(rec.Body.String(), "unknown argument") {
		t.Errorf("declared argument noted as unknown: %s", rec.Body.String())
	}
}

func TestClosestArg(t *testing.T) {
	known := []string{"exclude_closed", "older_than", "project", "projects"}
	for in, want := range map[string]string{
		"Project":         "project",
		"Projects":        "projects",
		"PROJECTS":        "projects",
		"older-than":      "older_than",
		"exclude_closeds": "exclude_closed",
		"limit":           "",
	} {
		if got := closestArg(in, known); got != want {
			t.Errorf("closestArg(%q) = %q, want %q", in, got, want)
		}
	}
}
