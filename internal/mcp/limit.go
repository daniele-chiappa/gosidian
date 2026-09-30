package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// limitArg reads the "limit" argument of a tool call, as clampedArg.
func limitArg(ctx context.Context, req mcp.CallToolRequest, def, maxLimit int) int {
	return clampedArg(ctx, req, "limit", def, maxLimit)
}

// clampedArg reads a positive integer argument: def when it is absent or not
// positive, maxValue when it is larger, and then the result says so. A
// larger value used to fall back to def, so asking for more silently
// returned fewer (BUG-075).
func clampedArg(ctx context.Context, req mcp.CallToolRequest, name string, def, maxValue int) int {
	v := req.GetInt(name, def)
	switch {
	case v <= 0:
		return def
	case v > maxValue:
		addCallNote(ctx, fmt.Sprintf("Note: %s takes %s up to %d: %d was lowered to %d.",
			req.Params.Name, name, maxValue, v, maxValue))
		return maxValue
	}
	return v
}
