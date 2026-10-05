package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

// Who wrote: a web UI write by its username, an MCP write by the token's
// name and its account ("admin" for a CLI token), the name alone once the
// token is gone; only writes of notes and projects count.
func TestAuthorEvent(t *testing.T) {
	store, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, tok, err := store.Create("claude-cli", nil, []string{auth.ScopeRead, auth.ScopeWrite}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	names := &authorNames{tokens: store}
	ts := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		e    audit.Entry
		want string
		ok   bool
	}{
		{audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionUpdate, Path: "p/a.md"}, "daniele", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: tok.ID, Actor: "claude-cli@1a2b3c4d", Action: audit.ActionCreate, Path: "p/a.md"}, "claude-cli (admin)", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: "gone1234", Actor: "old-agent@99", Action: audit.ActionAppend, Path: "p/a.md"}, "old-agent", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: tok.ID, Actor: "claude-cli@1a2b3c4d", Action: audit.ActionRename, Path: "p/a.md", To: "p/b.md"}, "claude-cli (admin)", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: tok.ID, Action: audit.ActionUploadAttachment, Path: "p/attachments/x.png"}, "", false},
		{audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionTokenCreate, Path: "abcd"}, "", false},
	} {
		ev, ok := authorEvent(tc.e, names)
		if ok != tc.ok || ok && (ev.By != tc.want || ev.Action != string(tc.e.Action) || ev.Path != tc.e.Path || ev.To != tc.e.To || !ev.TS.Equal(ts)) {
			t.Errorf("%s %s: %+v %v, want by %q ok=%v", tc.e.Action, tc.e.Actor, ev, ok, tc.want, tc.ok)
		}
	}
}
