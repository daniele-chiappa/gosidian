package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// Without agent_profile the instruction file picks it; an explicit profile
// wins, and a file shared by several agents falls back to generic.
func TestMCP_InitAgent_ProfileFromFilename(t *testing.T) {
	s, _, _ := newTestServer(t)
	profile := func(args map[string]any) string {
		t.Helper()
		args["project"] = "p"
		res, _ := s.handleInitAgent(context.Background(), call(args))
		var r initAgentResponse
		if err := json.Unmarshal([]byte(resultText(t, res)), &r); err != nil {
			t.Fatal(err)
		}
		return r.AgentProfile
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "generic"},
		{map[string]any{"filename_hint": "CLAUDE.md"}, "claude"},
		{map[string]any{"filename_hint": ".cursor/rules/gosidian.mdc"}, "cursor"},
		{map[string]any{"filename_hint": "AGENTS.md"}, "generic"},
		{map[string]any{"filename_hint": "CLAUDE.md", "agent_profile": "codex"}, "codex"},
	} {
		if got := profile(c.args); got != c.want {
			t.Errorf("%v: agent_profile %q, want %q", c.args, got, c.want)
		}
	}
}
