package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// graphFixture seeds a small connected vault so each Tier 1 filter
// has something meaningful to assert against.
//
//	rc/a.md  ─link─►  rc/b.md  ─link─►  rc/c.md   (also tagged #demo)
//	                                       │
//	                                       ▼
//	                                  rc/d.md
//	dockers/x.md (isolated, no edges)
//
// Total: 5 notes, 3 edges (a-b, b-c, c-d). After dedup degree:
// a=1, b=2, c=2, d=1, x=0.
func graphFixture(t *testing.T) *notesFixture {
	t.Helper()
	f := newNotesFixture(t)
	f.seedNote(t, "rc/a.md", "[[rc/b]]\n")
	f.seedNote(t, "rc/b.md", "[[rc/c]]\n")
	f.seedNote(t, "rc/c.md", "---\ntags: [demo]\n---\n\n[[rc/d]]\n")
	f.seedNote(t, "rc/d.md", "leaf\n")
	f.seedNote(t, "dockers/x.md", "isolated\n")
	return f
}

func decodeGraph(t *testing.T, body string) graphResponse {
	t.Helper()
	var resp graphResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestGraph_AllNodesAndEdges(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	if w.headers.Get("ETag") == "" {
		t.Errorf("expected ETag, got empty")
	}
	if !strings.Contains(w.headers.Get("Cache-Control"), "max-age=60") {
		t.Errorf("Cache-Control: %s", w.headers.Get("Cache-Control"))
	}
	resp := decodeGraph(t, w.body)
	if resp.Stats.NodeCount != 5 {
		t.Errorf("node count: got %d want 5", resp.Stats.NodeCount)
	}
	if resp.Stats.EdgeCount != 3 {
		t.Errorf("edge count: got %d want 3", resp.Stats.EdgeCount)
	}
}

func TestGraph_ProjectFilter(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph?project=rc", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	resp := decodeGraph(t, w.body)
	if resp.Stats.NodeCount != 4 {
		t.Errorf("expected 4 rc nodes, got %d", resp.Stats.NodeCount)
	}
	for _, n := range resp.Nodes {
		if !strings.HasPrefix(n.ID, "rc/") {
			t.Errorf("foreign node leaked through project filter: %s", n.ID)
		}
	}
}

func TestGraph_TagFilter(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph?tag=demo", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	resp := decodeGraph(t, w.body)
	if resp.Stats.NodeCount != 1 {
		t.Errorf("expected 1 tagged node, got %d (ids=%v)", resp.Stats.NodeCount, nodeIDs(resp.Nodes))
	}
	if resp.Stats.NodeCount == 1 && resp.Nodes[0].ID != "rc/c.md" {
		t.Errorf("expected rc/c.md, got %s", resp.Nodes[0].ID)
	}
}

func TestGraph_MinDegreeDropsLeaves(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph?min_degree=2", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	resp := decodeGraph(t, w.body)
	// Only b and c have degree >= 2 in the seeded graph.
	if resp.Stats.NodeCount != 2 {
		t.Errorf("expected 2 nodes (b,c), got %d (%v)", resp.Stats.NodeCount, nodeIDs(resp.Nodes))
	}
}

func TestGraph_FocusEgoOneHop(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph?focus=rc/b.md&depth=1", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	resp := decodeGraph(t, w.body)
	// 1-hop from b: a, b, c. d (2 hops away) excluded.
	if resp.Stats.NodeCount != 3 {
		t.Errorf("expected 3 nodes (a,b,c), got %d (%v)", resp.Stats.NodeCount, nodeIDs(resp.Nodes))
	}
}

func TestGraph_FocusEgoTwoHop(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph?focus=rc/a.md&depth=2", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	resp := decodeGraph(t, w.body)
	// 2-hop from a: a, b, c. d (3 hops) excluded.
	if resp.Stats.NodeCount != 3 {
		t.Errorf("expected 3 nodes (a,b,c), got %d (%v)", resp.Stats.NodeCount, nodeIDs(resp.Nodes))
	}
}

func TestGraph_LimitTruncatesByDegree(t *testing.T) {
	f := graphFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph?limit=2", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status: %d body=%s", w.code, w.body)
	}
	resp := decodeGraph(t, w.body)
	if resp.Stats.NodeCount != 2 || !resp.Stats.Truncated {
		t.Errorf("expected 2 truncated nodes, got %d (truncated=%v)", resp.Stats.NodeCount, resp.Stats.Truncated)
	}
	// Top-2 by degree are b and c (both 2).
	got := map[string]bool{}
	for _, n := range resp.Nodes {
		got[n.ID] = true
	}
	if !got["rc/b.md"] || !got["rc/c.md"] {
		t.Errorf("expected top-degree b+c, got %v", nodeIDs(resp.Nodes))
	}
}

// A graph asked without a limit is cut to the default one, which the
// summary names, and says how many nodes it had (BUG-127).
func TestGraph_DefaultLimitCapsAndCounts(t *testing.T) {
	f := graphFixture(t)

	f.router.web.GraphDefaultNodes = 10
	resp := decodeGraph(t, f.doAuthRecorder(http.MethodGet, "/api/v1/graph", "", nil).body)
	if resp.Stats.NodeCount != 5 || resp.Stats.Total != 5 || resp.Stats.Truncated || resp.Stats.Filter != "" {
		t.Errorf("under the default: got %+v, want 5 of 5, not truncated, no filter", resp.Stats)
	}

	f.router.web.GraphDefaultNodes = 2
	resp = decodeGraph(t, f.doAuthRecorder(http.MethodGet, "/api/v1/graph", "", nil).body)
	if resp.Stats.NodeCount != 2 || resp.Stats.Total != 5 || !resp.Stats.Truncated || resp.Stats.Filter != "limit=2" {
		t.Errorf("over the default: got %+v, want 2 of 5, truncated, limit=2", resp.Stats)
	}
}

// The limit keeps the focus of an ego graph, though its degree is the
// lowest.
func TestGraph_LimitKeepsTheFocus(t *testing.T) {
	f := graphFixture(t)
	resp := decodeGraph(t, f.doAuthRecorder(http.MethodGet, "/api/v1/graph?focus=rc/a.md&depth=2&limit=1", "", nil).body)
	if ids := nodeIDs(resp.Nodes); len(ids) != 1 || ids[0] != "rc/a.md" || resp.Stats.Total != 3 {
		t.Errorf("got %v of %d, want [rc/a.md] of 3", ids, resp.Stats.Total)
	}
}

func TestGraph_ETag304(t *testing.T) {
	f := graphFixture(t)
	first := f.doAuthRecorder(http.MethodGet, "/api/v1/graph", "", nil)
	etag := first.headers.Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag")
	}
	second := f.doAuthRecorder(http.MethodGet, "/api/v1/graph", "", map[string]string{"If-None-Match": etag})
	if second.code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", second.code)
	}
}

func nodeIDs(nodes []graphNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// A graph asks for at most [web] graph_max_nodes, a limit or none; a default
// over the most is the most.
func TestGraph_MaxNodes(t *testing.T) {
	f := graphFixture(t)
	f.router.web.GraphMaxNodes = 3
	resp := decodeGraph(t, f.doAuthRecorder(http.MethodGet, "/api/v1/graph?limit=4", "", nil).body)
	if resp.Stats.NodeCount != 3 || resp.Stats.Total != 5 || !resp.Stats.Truncated {
		t.Errorf("limit 4 under a most of 3: %+v", resp.Stats)
	}
	if l := (WebLimits{GraphDefaultNodes: 9000, GraphMaxNodes: 6000}).withDefaults(); l.GraphDefaultNodes != 6000 || l.GraphMaxEdges != 50000 || l.CutMaxChunks != 5000 {
		t.Errorf("defaults %+v", l)
	}
}
