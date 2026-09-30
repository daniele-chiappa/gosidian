package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// resultNotices returns the notes of a tool result, checking they sit where
// a client reading only the structured content finds them, and in its JSON
// text too.
func resultNotices(t *testing.T, res *mcplib.CallToolResult) []string {
	t.Helper()
	obj, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content %T, want an object", res.StructuredContent)
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &fromText); err != nil {
		t.Fatal(err)
	}
	list, _ := obj[noticesField].([]any)
	textList, _ := fromText[noticesField].([]any)
	if len(list) != len(textList) {
		t.Fatalf("notices: %d in the structured content, %d in its text", len(list), len(textList))
	}
	out := make([]string, len(list))
	for i, n := range list {
		out[i] = n.(string)
	}
	return out
}

// A note on a JSON result goes into its structured content, which is all
// Claude Code shows, and into the JSON text; any other result gets a text
// block (BUG-077).
func TestAppendNotice(t *testing.T) {
	data := map[string]any{"hits": []any{}}
	res, err := mcplib.NewToolResultJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	appendNotice(res, "first")
	appendNotice(res, "second")
	if got := resultNotices(t, res); strings.Join(got, ",") != "first,second" || len(res.Content) != 1 {
		t.Errorf("JSON result: notices %q, %d content blocks", got, len(res.Content))
	}
	if _, touched := data[noticesField]; touched {
		t.Error("the handler's map was changed in place")
	}

	type page struct {
		Items []string `json:"items"`
	}
	res, _ = mcplib.NewToolResultJSON(page{Items: []string{"a"}})
	appendNotice(res, "on a struct")
	if got := resultNotices(t, res); len(got) != 1 || res.StructuredContent.(map[string]any)["items"] == nil {
		t.Errorf("struct result: %+v", res.StructuredContent)
	}

	res = mcplib.NewToolResultText("plain")
	appendNotice(res, "on text")
	if len(res.Content) != 2 || res.Content[1].(mcplib.TextContent).Text != "on text" || res.StructuredContent != nil {
		t.Errorf("text result: %+v", res)
	}
}
