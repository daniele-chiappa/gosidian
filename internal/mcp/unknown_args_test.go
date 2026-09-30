package mcp

import (
	"encoding/json"
	"fmt"
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
	// Claude Code shows only the structured content of a JSON result: the
	// note must be there (BUG-077).
	var rpc struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rpc); err != nil {
		t.Fatal(err)
	}
	if n, _ := rpc.Result.StructuredContent[noticesField].([]any); len(n) != 1 || !strings.Contains(fmt.Sprint(n[0]), "projetcs") {
		t.Errorf("note missing from the structured content: %s", body)
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
