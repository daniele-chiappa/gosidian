package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/plugins"
)

// The cut view (IMP-170): /preview with cut gives the note's sections and
// paragraphs as note.sections cuts them, each rendered, its images small
// beside their text; a collection that cannot be read leaves the states
// out and says why.
func TestPreview_Cut(t *testing.T) {
	f := newNotesFixture(t)
	article := "---\ntitle: Stampanti\n---\n\n# Stampanti\n\n## La schermata\n\nPremere ![](/vault-files/p/attachments/icon.png) per cercare.\n\n![](/vault-files/p/attachments/shot.png)\n\n![](/vault-files/p/attachments/none.png)\n"
	f.seedNote(t, "p/docs/stampanti.md", article)
	f.seedNote(t, "p/docs/stampanti_img/001.md", "---\ntype: image\nmedia: p/attachments/icon.png\narticle: \"[[p/docs/stampanti]]\"\nlabel: Binocolo\nwidth: 16\nheight: 16\n---\n\nApre la ricerca.\n")
	f.seedNote(t, "p/docs/stampanti_img/002.md", "---\ntype: image\nmedia: p/attachments/shot.png\narticle: \"[[p/docs/stampanti]]\"\nlabel: Elenco\nwidth: 900\nheight: 600\n---\n\nUna griglia.\n")
	ask := func(cut map[string]any) cutResponse {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"markdown": article, "path": "p/docs/stampanti.md", "cut": cut})
		w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", string(body), nil)
		if w.code != http.StatusOK {
			t.Fatalf("status %d: %s", w.code, w.body)
		}
		var res previewResponse
		if err := json.Unmarshal([]byte(w.body), &res); err != nil || res.Cut == nil || res.HTML != "" {
			t.Fatalf("response %s: %v", w.body, err)
		}
		return *res.Cut
	}
	c := ask(map[string]any{"media_words": map[string]any{"icon": "icona", "image": "immagine"}})
	if c.Sections != 1 || c.Paragraphs != 3 || len(c.Blocks) != 4 || c.Media.Images != 3 || c.Media.Missing != 1 || c.MaxChars != 6000 || c.States != nil {
		t.Fatalf("cut %+v", c)
	}
	sec := c.Blocks[0]
	if sec.Level != 1 || sec.Section != "La schermata" || !strings.Contains(sec.Head, "La schermata") || sec.State != "" || len(sec.Hash) != 16 {
		t.Errorf("section %+v", sec)
	}
	if p := c.Blocks[1]; !strings.Contains(p.HTML, "[icona: Binocolo]") || !strings.Contains(p.HTML, `class="gosidian-cut-thumb"`) || p.Parent != sec.ID {
		t.Errorf("paragraph with an icon %+v", p)
	}
	if p := c.Blocks[2]; !strings.Contains(p.HTML, "[immagine: Elenco] Una griglia.") || !strings.Contains(p.HTML, `src="/vault-files/p/attachments/shot.png"`) {
		t.Errorf("paragraph of a screenshot %+v", p)
	}
	if p := c.Blocks[3]; !strings.Contains(p.HTML, `class="gosidian-cut-missing"`) {
		t.Errorf("an image without description %+v", p)
	}
	// media_paragraphs: join, as note.sections: the screenshot and the
	// image without a description go with the sentence before them, shown
	// with their thumbnails.
	c = ask(map[string]any{"media_words": map[string]any{"icon": "icona", "image": "immagine"}, "media_paragraphs": "join"})
	if c.Paragraphs != 1 || len(c.Blocks) != 2 || c.Blocks[0].Hash != sec.Hash || !strings.Contains(c.Blocks[1].HTML, "[immagine: Elenco] Una griglia.") ||
		!strings.Contains(c.Blocks[1].HTML, "per cercare.") || !strings.Contains(c.Blocks[1].HTML, `class="gosidian-cut-missing"`) {
		t.Errorf("cut with media_paragraphs join %+v", c)
	}
	// icon_descriptions: first: the icon's description after its label.
	c = ask(map[string]any{"media_words": map[string]any{"icon": "icona", "image": "immagine"}, "icon_descriptions": "first"})
	if len(c.Blocks) != 4 || !strings.Contains(c.Blocks[1].HTML, "[icona: Binocolo (Apre la ricerca.)]") || c.Blocks[0].Hash == sec.Hash {
		t.Errorf("cut with icon_descriptions first %+v", c)
	}
	c = ask(map[string]any{"collection": "vec-pg/pezzi"})
	if c.Error == "" || c.States != nil || len(c.Blocks) != 4 {
		t.Errorf("a collection that cannot be read %+v", c)
	}
	for _, bad := range []map[string]any{{"max_chars": 10}, {"context": "all"}, {"media_paragraphs": "drop"}, {"icon_descriptions": "some"}} {
		body, _ := json.Marshal(map[string]any{"markdown": article, "path": "p/docs/stampanti.md", "cut": bad})
		if w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", string(body), nil); w.code != http.StatusBadRequest {
			t.Errorf("%v: status %d", bad, w.code)
		}
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/cut/collections?project=p", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"collections":[]`) {
		t.Errorf("collections without plugins: %d %s", w.code, w.body)
	}
}

// archivePlugin is an archive of vectors with read tools list and
// collections (list as listAccess says): list gives the chunks one a page,
// with a cursor, as an archive cuts a long list.
func archivePlugin(t *testing.T, listAccess string, chunks func() []map[string]any) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("vec", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(plugins.ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "vec", "version": "1.0", "gosidian": 1,
			"tools": []any{
				map[string]any{"name": "list", "access": listAccess, "description": "The chunks"},
				map[string]any{"name": "collections", "access": "read", "description": "The collections"},
			},
			"permissions": map[string]any{"files": false, "network": []string{}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	s.AddTool(mcp.NewTool("collections"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b := `{"collections":[{"name":"docs","model":"m","count":3}]}`
		return mcp.NewToolResultStructured(json.RawMessage(b), b), nil
	})
	s.AddTool(mcp.NewTool("list"), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		all := chunks()
		i, _ := strconv.Atoi(req.GetString("cursor", "0"))
		out := map[string]any{"chunks": []any{}, "next": ""}
		if i < len(all) {
			out["chunks"] = []any{all[i]}
			if i+1 < len(all) {
				out["next"] = strconv.Itoa(i + 1)
			}
		}
		b, _ := json.Marshal(out)
		return mcp.NewToolResultStructured(json.RawMessage(b), string(b)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// Against a collection, each block is the same, changed or missing, by
// the chunks of every page of list; a list tool that is no read tool is
// not called for a reader.
func TestPreview_CutAgainstACollection(t *testing.T) {
	for _, access := range []string{"read", "write"} {
		f := newNotesFixture(t)
		article := "# Printers\n\n## The screen\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph.\n"
		f.seedNote(t, "p/docs/printers.md", article)
		var stored []map[string]any
		ts := archivePlugin(t, access, func() []map[string]any { return stored })
		reg, err := plugins.New([]plugins.Config{{Name: "vec", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		reg.Check(ctx)
		st, _ := reg.State("vec")
		if _, err := reg.Accept(ctx, "vec", st.SHA, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Expose("vec", "p", "test"); err != nil {
			t.Fatal(err)
		}
		f.router.deps.Plugins = reg
		ask := func(cut map[string]any) cutResponse {
			t.Helper()
			body, _ := json.Marshal(map[string]any{"markdown": article, "path": "p/docs/printers.md", "cut": cut})
			w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", string(body), nil)
			var res previewResponse
			if w.code != http.StatusOK || json.Unmarshal([]byte(w.body), &res) != nil || res.Cut == nil {
				t.Fatalf("status %d: %s", w.code, w.body)
			}
			return *res.Cut
		}
		blocks := ask(map[string]any{}).Blocks
		if len(blocks) != 4 {
			t.Fatalf("blocks %+v", blocks)
		}
		stored = []map[string]any{
			{"id": blocks[0].ID + "@v1", "meta": map[string]any{"base": blocks[0].ID, "hash": blocks[0].Hash}},
			{"id": blocks[1].ID + "@v1", "meta": map[string]any{"base": blocks[1].ID, "hash": "0000000000000000"}},
			{"id": blocks[2].ID + "@v1", "meta": map[string]any{"base": blocks[2].ID, "hash": blocks[2].Hash}},
			{"id": "p/docs/printers.md#gone@v1", "meta": map[string]any{"base": "p/docs/printers.md#gone", "hash": "1111111111111111"}},
		}
		c := ask(map[string]any{"collection": "vec/docs"})
		if access == "write" {
			if c.Error == "" || c.States != nil {
				t.Errorf("a list tool that writes was called for a reader: %+v", c)
			}
			if w := f.doAuthRecorder(http.MethodGet, "/api/v1/cut/collections?project=p", "", nil); !strings.Contains(w.body, `"collections":[]`) {
				t.Errorf("collections with a list tool that writes: %s", w.body)
			}
			continue
		}
		var states []string
		for _, b := range c.Blocks {
			states = append(states, b.State)
		}
		if c.Error != "" || strings.Join(states, " ") != "same changed same missing" || c.Stored != 4 || c.Orphans != 1 {
			t.Errorf("states %v, stored %d, orphans %d, error %q", states, c.Stored, c.Orphans, c.Error)
		}
		if w := f.doAuthRecorder(http.MethodGet, "/api/v1/cut/collections?project=p", "", nil); !strings.Contains(w.body, `"source":"vec/docs"`) {
			t.Errorf("collections: %s", w.body)
		}
		if w := f.doAuthRecorder(http.MethodPost, "/api/v1/preview", `{"markdown":"x","path":"p/docs/printers.md","cut":{"inline_max_side":-1}}`, nil); w.code != http.StatusBadRequest {
			t.Errorf("inline_max_side -1: %d", w.code)
		}
	}
}
