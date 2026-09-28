package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// unknownArgsMiddleware notes the arguments a tool does not declare. They used
// to be dropped silently, so a caller that passed `project` to a tool taking
// `projects` believed it had filtered. The call still runs — a client whose
// schema is newer than this server must not break — but the result carries a
// note naming what was ignored and, when a name is close to a declared one,
// the likely intended argument. Error results get the note too: a misnamed
// required argument is the usual cause of the error.
func (s *Server) unknownArgsMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := next(ctx, req)
		if err != nil || result == nil {
			return result, err
		}
		unknown, known := s.unknownArgs(req)
		if len(unknown) == 0 {
			return result, err
		}
		slog.Info("mcp.call unknown arguments", "tool", req.Params.Name, "args", strings.Join(unknown, ","))
		result.Content = append(result.Content, mcp.NewTextContent(unknownArgsNote(req.Params.Name, unknown, known)))
		return result, err
	}
}

// unknownArgs returns the argument names the tool's input schema does not
// declare, and the declared ones, both sorted. A tool registered with a raw
// schema is not checked.
func (s *Server) unknownArgs(req mcp.CallToolRequest) (unknown, known []string) {
	if s.impl == nil {
		return nil, nil
	}
	tool := s.impl.GetTool(req.Params.Name)
	if tool == nil || tool.Tool.RawInputSchema != nil {
		return nil, nil
	}
	props := tool.Tool.InputSchema.Properties
	for name := range req.GetArguments() {
		if _, ok := props[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil, nil
	}
	for name := range props {
		known = append(known, name)
	}
	sort.Strings(unknown)
	sort.Strings(known)
	return unknown, known
}

// unknownArgsNote renders the note appended to the result.
func unknownArgsNote(tool string, unknown, known []string) string {
	parts := make([]string, 0, len(unknown))
	for _, u := range unknown {
		p := fmt.Sprintf("%q", u)
		if guess := closestArg(u, known); guess != "" {
			p += fmt.Sprintf(" (did you mean %q?)", guess)
		}
		parts = append(parts, p)
	}
	accepted := "no arguments"
	if len(known) > 0 {
		accepted = strings.Join(known, ", ")
	}
	return fmt.Sprintf("Note: %s ignored unknown argument(s) %s. It accepts: %s.", tool, strings.Join(parts, ", "), accepted)
}

// closestArg guesses the declared argument a misnamed one was meant to be:
// same name up to case, '-' vs '_', or a trailing plural 's'.
func closestArg(name string, known []string) string {
	norm := func(v string) string {
		return strings.TrimSuffix(strings.ReplaceAll(strings.ToLower(v), "-", "_"), "s")
	}
	n := norm(name)
	for _, k := range known {
		if norm(k) == n {
			return k
		}
	}
	return ""
}
