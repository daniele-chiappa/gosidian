package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type queryResponse struct {
	Notes []struct {
		Path   string         `json:"path"`
		Title  string         `json:"title"`
		Fields map[string]any `json:"fields"`
	} `json:"notes"`
	Count     int  `json:"count"`
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

func decodeQuery(t *testing.T, body string) queryResponse {
	t.Helper()
	var out queryResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func TestQuery_OwnerAndProjectScope(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/plans/a.md", "---\ntype: plan\nstatus: draft\nimportance: 4\n---\n\n# A")
	f.seedNote(t, "alpha/plans/b.md", "---\ntype: plan\ntags: [status:done]\n---\n\n# B")
	f.seedNote(t, "beta/plans/c.md", "---\ntype: plan\nstatus: draft\n---\n\n# C")

	w := f.doAuthRecorder(http.MethodPost, "/api/v1/query", `{"where":[{"field":"type","value":"plan"},{"field":"status","op":"eq","value":"draft"}],"sort":"path"}`, nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	out := decodeQuery(t, w.body)
	if out.Total != 2 || len(out.Notes) != 2 || out.Notes[0].Path != "alpha/plans/a.md" || out.Notes[1].Path != "beta/plans/c.md" {
		t.Fatalf("draft plans: %s", w.body)
	}
	if out.Notes[0].Fields["status"] != "draft" || out.Notes[0].Fields["type"] != "plan" {
		t.Errorf("default fields: %v", out.Notes[0].Fields)
	}

	w = f.doAuthRecorder(http.MethodPost, "/api/v1/query", `{"project":"alpha","where":[{"field":"status","op":"in","value":["done","draft"]}],"fields":["importance"],"limit":1}`, nil)
	out = decodeQuery(t, w.body)
	if w.code != http.StatusOK || out.Total != 2 || out.Count != 1 || !out.Truncated || strings.HasPrefix(out.Notes[0].Path, "beta/") {
		t.Errorf("alpha, limit 1: %s", w.body)
	}
}

func TestQuery_GuestSeesOnlyReadableProjects(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)
	f.seedNote(t, "pubproj/plans/p.md", "---\ntype: plan\nstatus: draft\n---\n\n# P")
	f.seedNote(t, "privproj/plans/q.md", "---\ntype: plan\nstatus: draft\n---\n\n# Q")

	rec := f.req(t, http.MethodPost, "/api/v1/query", `{"where":[{"field":"type","value":"plan"}]}`, guest)
	if rec.code != http.StatusOK {
		t.Fatalf("guest query status=%d body=%s", rec.code, rec.body)
	}
	if !strings.Contains(rec.body, "pubproj/plans/p.md") || strings.Contains(rec.body, "privproj") {
		t.Errorf("guest query must see the public project only: %s", rec.body)
	}
	rec = f.req(t, http.MethodPost, "/api/v1/query", `{"project":"privproj","where":[{"field":"type","value":"plan"}]}`, guest)
	if out := decodeQuery(t, rec.body); rec.code != http.StatusOK || out.Total != 0 || len(out.Notes) != 0 {
		t.Errorf("guest asking for the private project: %d %s", rec.code, rec.body)
	}
}

func TestQuery_BadRequests(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "alpha/a.md", "---\ntype: plan\n---\n\n# A")
	for name, body := range map[string]string{
		"no where":      `{}`,
		"unknown op":    `{"where":[{"field":"type","op":"like","value":"p%"}]}`,
		"object value":  `{"where":[{"field":"type","value":{"x":1}}]}`,
		"bad order":     `{"where":[{"field":"type","value":"plan"}],"order":"up"}`,
		"unknown field": `{"where":[{"field":"type","value":"plan"}],"page":2}`,
		"no field":      `{"where":[{"op":"eq","value":"plan"}]}`,
	} {
		if w := f.doAuthRecorder(http.MethodPost, "/api/v1/query", body, nil); w.code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s, want 400", name, w.code, w.body)
		}
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/query", "", nil); w.code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status=%d, want 405", w.code)
	}
}
