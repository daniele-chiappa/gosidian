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
	search := func(limit int) (hits int, notes string, truncated bool) {
		t.Helper()
		req := call(map[string]any{"query": "gopher", "limit": limit})
		req.Params.Name = "memory_search"
		res, err := callNotesMiddleware(s.handleSearch)(ctx, req)
		if err != nil || res == nil || res.IsError || len(res.Content) == 0 {
			t.Fatalf("limit %d: %v %+v", limit, err, res)
		}
		var out struct {
			Hits      []json.RawMessage `json:"hits"`
			Truncated bool              `json:"truncated"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		var list []string
		if _, ok := res.StructuredContent.(map[string]any)[noticesField]; ok {
			list = resultNotices(t, res)
		}
		return len(out.Hits), strings.Join(list, " "), out.Truncated
	}
	if hits, notes, cut := search(1000); hits != 25 || cut || !strings.Contains(notes, "limit up to 200: 1000 was lowered to 200") {
		t.Errorf("limit 1000: %d hits, truncated %v, notes %q; want 25 hits and a note", hits, cut, notes)
	}
	// truncated says more notes match than came back (IMP-122).
	if hits, notes, cut := search(5); hits != 5 || !cut || notes != "" {
		t.Errorf("limit 5: %d hits, truncated %v, notes %q", hits, cut, notes)
	}
	if hits, _, cut := search(25); hits != 25 || cut {
		t.Errorf("limit 25 of 25: %d hits, truncated %v", hits, cut)
	}
}
