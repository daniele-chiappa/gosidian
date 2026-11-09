package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// memory_get says a note's ```graph blocks in words, as it says its
// diagrams (IMP-224, F-32): the links counted among the notes the token
// reads, its most linked notes; a plugin's graph by its view alone; a block
// that does not read, its problems.
func TestGet_GraphsInWords(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/a.md": "# A\n\n[[p/b]]\n",
		"p/b.md": "# B\n\n[[p/c]]\n",
		"p/c.md": "# C\n",
		"q/x.md": "# X\n\n[[p/a]] [[q/y]]\n",
		"q/y.md": "# Y\n",
		"p/g.md": "---\ntitle: G\ntype: graph\n---\n\n```graph\nsource: links\nproject: p\n```\n\n```graph\nsource: links\n```\n\n```graph\nsource: vec-pg/docs\nwith: {collection: docs}\n```\n\n```graph\nmode: 4d\n```\n",
	} {
		req := call(map[string]any{"path": path, "content": content})
		req.Params.Name = "memory_create"
		if res, err := callNotesMiddleware(s.handleCreate)(ctx, req); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	type words struct {
		Line     int    `json:"line"`
		Source   string `json:"source"`
		Text     string `json:"text"`
		Problems []struct {
			Msg string `json:"msg"`
		} `json:"problems"`
	}
	get := func(ctx context.Context) []words {
		t.Helper()
		res, err := s.handleGet(ctx, call(map[string]any{"path": "p/g.md"}))
		if err != nil || res.IsError {
			t.Fatalf("get: %v %+v", err, res)
		}
		var got struct {
			Graphs []words `json:"graphs"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Graphs
	}
	all := get(ctx)
	if len(all) != 4 {
		t.Fatalf("graphs = %+v", all)
	}
	for i, want := range []string{
		"The links of project p: 4 notes, 2 links. Most linked: [[p/b]] 2, [[p/a]] 1, [[p/c]] 1.",
		"The links of every project: 6 notes, 4 links. Most linked: [[p/a]] 2, [[p/b]] 2, [[q/x]] 2, [[p/c]] 1, [[q/y]] 1.",
	} {
		if all[i].Text != want || all[i].Source != "links" || all[i].Line == 0 {
			t.Errorf("block %d: %+v, want text %q", i, all[i], want)
		}
	}
	if all[2].Source != "vec-pg/docs" || !strings.Contains(all[2].Text, "plugin vec-pg") || !strings.Contains(all[2].Text, "not asked") {
		t.Errorf("plugin block: %+v", all[2])
	}
	if len(all[3].Problems) == 0 || all[3].Text != "" {
		t.Errorf("a block that does not read: %+v", all[3])
	}

	// A token of p alone counts the notes of p alone.
	pctx := context.WithValue(ctx, tokenCtxKey, &auth.Token{Name: "p-agent", Projects: []string{"p"}, Scopes: []string{auth.ScopeRead}})
	if got := get(pctx)[1].Text; got != "The links of every project: 4 notes, 2 links. Most linked: [[p/b]] 2, [[p/a]] 1, [[p/c]] 1." {
		t.Errorf("a token of p: %q", got)
	}
}
