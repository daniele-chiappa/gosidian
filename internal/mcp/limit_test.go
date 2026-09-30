package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestLimitArg(t *testing.T) {
	for _, c := range []struct {
		arg  any
		want int
		note bool
	}{
		{nil, 20, false}, {0, 20, false}, {-3, 20, false}, {7, 7, false},
		{200, 200, false}, {201, 200, true}, {100000, 200, true},
	} {
		args := map[string]any{}
		if c.arg != nil {
			args["limit"] = c.arg
		}
		req := call(args)
		req.Params.Name = "memory_search"
		notes := &callNotes{}
		ctx := context.WithValue(context.Background(), callNotesKey{}, notes)
		if got := limitArg(ctx, req, 20, 200); got != c.want {
			t.Errorf("limit %v: got %d, want %d", c.arg, got, c.want)
		}
		if (len(notes.list) > 0) != c.note {
			t.Errorf("limit %v: notes %q, want a note: %v", c.arg, notes.list, c.note)
		}
	}
}

// Asking for more than the maximum returns the maximum, not the default,
// and the result says the limit was lowered (BUG-075).
func TestSearch_LimitAboveMaximum(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for i := range 25 {
		_, _ = s.handleCreate(ctx, call(map[string]any{"path": fmt.Sprintf("n%02d.md", i), "content": "gopher note"}))
	}
	search := func(limit int) (hits int, notes string) {
		t.Helper()
		req := call(map[string]any{"query": "gopher", "limit": limit})
		req.Params.Name = "memory_search"
		res, err := callNotesMiddleware(s.handleSearch)(ctx, req)
		if err != nil || res == nil || res.IsError || len(res.Content) == 0 {
			t.Fatalf("limit %d: %v %+v", limit, err, res)
		}
		var out struct {
			Hits []json.RawMessage `json:"hits"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		return len(out.Hits), strings.Join(resultNotices(t, res), " ")
	}
	if hits, notes := search(1000); hits != 25 || !strings.Contains(notes, "limit up to 200: 1000 was lowered to 200") {
		t.Errorf("limit 1000: %d hits, notes %q; want 25 hits and a note", hits, notes)
	}
	if hits, notes := search(5); hits != 5 || notes != "" {
		t.Errorf("limit 5: %d hits, notes %q", hits, notes)
	}
}
