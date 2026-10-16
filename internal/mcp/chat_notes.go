package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/chatbot"
)

// noteChatProblems leaves a note on the current tool call for a ```chat
// block of the note just written that does not read as a bot (IMP-191), as
// noteGraphProblems does: it warns and does not refuse, and the lint rule
// chat-invalid lists it until it is fixed.
func (s *Server) noteChatProblems(ctx context.Context, rel string, content []byte) {
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		return
	}
	_, problems, ok := chatbot.FromNote(rel, content)
	if !ok || len(problems) == 0 {
		return
	}
	msgs := make([]string, 0, len(problems))
	for _, p := range problems {
		msgs = append(msgs, p.String())
	}
	addCallNote(ctx, fmt.Sprintf(
		"%s: the ```chat block does not read as a bot (%s). The note is saved as written; fix the block with memory_edit (fields: name, plugin, pipeline, title, welcome, scopes, access, sources, conversations, limits; docs/chat.md).",
		rel, strings.Join(msgs, "; ")))
}
