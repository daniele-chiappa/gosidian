package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// memory_get with media: "inline" serves a note with its images as text,
// from their image notes (IMP-170); the file stays as it is.
func TestMCP_GetMediaInline(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	article := "---\ntitle: Stampanti\n---\n\n# Stampanti\n\nPremere ![](/vault-files/rag/attachments/icon.png) per cercare.\n\n![](/vault-files/rag/attachments/shot.png)\n"
	for path, content := range map[string]string{
		"rag/docs/stampanti.md":         article,
		"rag/docs/stampanti_img/001.md": "---\ntype: image\nmedia: rag/attachments/icon.png\narticle: \"[[rag/docs/stampanti]]\"\nlabel: Binocolo\nwidth: 16\nheight: 16\n---\n\nApre la ricerca.\n",
		"rag/docs/stampanti_img/002.md": "---\ntype: image\nmedia: rag/attachments/shot.png\narticle: \"[[rag/docs/stampanti]]\"\nlabel: Elenco\nwidth: 900\nheight: 600\n---\n\nUna griglia.\n",
	} {
		resultText(t, mustRes(s.handleCreate(ctx, call(map[string]any{"path": path, "content": content}))))
	}
	var got struct {
		Content     string         `json:"content"`
		MediaInline map[string]int `json:"media_inline"`
	}
	text := resultText(t, mustRes(s.handleGet(ctx, call(map[string]any{"path": "rag/docs/stampanti.md", "media": "inline"}))))
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Content, "---\ntitle: Stampanti\n---\n") || !strings.Contains(got.Content, "Premere [icon: Binocolo] per cercare.") ||
		!strings.Contains(got.Content, "[image: Elenco] Una griglia.") || got.MediaInline["images"] != 2 || got.MediaInline["described"] != 2 {
		t.Errorf("media inline: %s", text)
	}
	text = resultText(t, mustRes(s.handleGet(ctx, call(map[string]any{"path": "rag/docs/stampanti.md"}))))
	if strings.Contains(text, "media_inline") || !strings.Contains(text, "icon.png") {
		t.Errorf("without media: %s", text)
	}
	expectError(t, mustRes(s.handleGet(ctx, call(map[string]any{"path": "rag/docs/stampanti.md", "media": "all"}))))
}

func mustRes(r *mcplib.CallToolResult, err error) *mcplib.CallToolResult {
	if err != nil {
		panic(err)
	}
	return r
}
