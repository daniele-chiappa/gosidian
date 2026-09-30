package mcp

import (
	"context"
	"encoding/json"
	"maps"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// noticesField is the list a note goes to in a structured tool result.
const noticesField = "notices"

// appendNotice adds a note for the agent to a tool result. A result with an
// object as structured content gets it in that object's "notices" list, and
// its JSON text block is rendered again from it: clients such as Claude Code
// show only the structured content when there is one, so a note in a text
// block of its own never reached them (BUG-077). Any other result gets the
// note as a text block.
func appendNotice(r *mcp.CallToolResult, note string) {
	obj, ok := structuredObject(r.StructuredContent)
	if !ok {
		r.Content = append(r.Content, mcp.NewTextContent(note))
		return
	}
	list, _ := obj[noticesField].([]any)
	obj[noticesField] = append(list, note)
	r.StructuredContent = obj
	// The first text block is the JSON rendering of the structured content
	// (mcp.NewToolResultJSON); anything else there is left alone.
	if len(r.Content) > 0 {
		if tc, isText := r.Content[0].(mcp.TextContent); isText && strings.HasPrefix(tc.Text, "{") && json.Valid([]byte(tc.Text)) {
			if b, err := json.Marshal(obj); err == nil {
				r.Content[0] = mcp.NewTextContent(string(b))
			}
		}
	}
}

// structuredObject returns v as a fresh JSON object, false when it is not
// one. A map is copied so a handler's value is never changed in place.
func structuredObject(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return maps.Clone(t), true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var obj map[string]any
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

// callNotes collects the notes a tool handler leaves for its result, such as
// a limit it lowered; callNotesMiddleware appends them.
type callNotes struct{ list []string }

type callNotesKey struct{}

func callNotesMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		notes := &callNotes{}
		result, err := next(context.WithValue(ctx, callNotesKey{}, notes), req)
		// An error result says what went wrong; a note on an argument it
		// never got to use would only blur that.
		if err == nil && result != nil && !result.IsError {
			for _, n := range notes.list {
				appendNotice(result, n)
			}
		}
		return result, err
	}
}

// addCallNote leaves a note for the result of the current tool call. Outside
// one (a handler called directly, in a test) it is dropped.
func addCallNote(ctx context.Context, note string) {
	if notes, ok := ctx.Value(callNotesKey{}).(*callNotes); ok {
		notes.list = append(notes.list, note)
	}
}
