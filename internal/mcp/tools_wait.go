// Package mcp — memory_wait_changes tool (orchestrator-bus plan, M6).
//
// Long-poll over the in-process events hub: an agent parks one request and
// wakes up when a note changes inside its scope, instead of burning tokens
// polling memory_recent/memory_pending_handoffs in a loop. Request/response
// only — no persistent subscription, no queue: the hub's short replay ring
// (events.ReplaySince) bridges the gap between two consecutive calls, and a
// cursor that falls out of the retention window gets resync=true so the
// caller reconciles with a full re-read.
package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	waitDefaultTimeoutS = 25
	waitMaxTimeoutS     = 55 // stay under SSE keepalive / client tool timeouts
)

func (s *Server) registerWaitTool() {
	s.impl.AddTool(mcp.NewTool("memory_wait_changes",
		mcp.WithDescription("Long-poll for note changes inside the token's scope. Blocks up to timeout_s seconds and returns as soon as a note is created/updated/deleted (topics: note, tree), or with an empty events list on timeout. Pass the cursor returned by the previous call to resume without gaps; the first call (cursor 0) just establishes the cursor. resync=true means the cursor fell out of the short replay window — reconcile with memory_recent instead of trusting the stream. One wait per MCP session: poll loops should call this back-to-back, not in parallel. Use this instead of polling memory_recent/memory_pending_handoffs."),
		mcp.WithString("project", mcp.Description("Optional project filter. Empty = every project the token can see (for multi-project orchestrator tokens this means all of their projects).")),
		mcp.WithNumber("cursor", mcp.Description("Sequence cursor from the previous call's `cursor` field. Omit on the first call to start from now (no replay); always pass the returned value on subsequent calls.")),
		mcp.WithNumber("timeout_s", mcp.Description("Max seconds to block waiting for an event (default 25, max 55). The call returns earlier as soon as a matching event arrives.")),
	), s.handleWaitChanges)
}

// waitEvent is the caller-facing shape of one change event.
type waitEvent struct {
	Seq    uint64 `json:"seq"`
	Topic  string `json:"topic"`
	Action string `json:"action,omitempty"`
	Path   string `json:"path,omitempty"`
	ETag   string `json:"etag,omitempty"`
	Source string `json:"source,omitempty"`
}

// notePayload mirrors the JSON published by publishNoteChange (MCP) and the
// API-side publishers on the note/tree topics.
type notePayload struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	ETag   string `json:"etag"`
	Source string `json:"source"`
}

func (s *Server) handleWaitChanges(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	if s.events == nil {
		return mcp.NewToolResultError("events hub not available on this server"), nil
	}
	project := req.GetString("project", "")
	if project != "" {
		if !tok.AllowsProject(project) {
			return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, tok.ScopeLabel()), nil
		}
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
	}
	timeoutS := req.GetInt("timeout_s", waitDefaultTimeoutS)
	if timeoutS < 1 {
		timeoutS = 1
	}
	if timeoutS > waitMaxTimeoutS {
		timeoutS = waitMaxTimeoutS
	}
	// Omitted cursor = "start from now" (no replay). An explicit cursor —
	// including 0, which a first idle call can legitimately return — resumes
	// from that sequence number.
	cursor := s.events.Seq()
	if c := req.GetInt("cursor", -1); c >= 0 {
		cursor = uint64(c)
	}

	// One in-flight wait per MCP session; a message without a session
	// counts against its token. Keyed by the correlation id, a sessionless
	// client got a fresh random one per message, and 200 waits held 200
	// subscriptions (BUG-113, S3-6).
	waiterKey := "tok:" + tok.ID
	if h := sessionHash(ctx); h != "" {
		waiterKey += ":" + h
	}
	if _, busy := s.waiters.LoadOrStore(waiterKey, struct{}{}); busy {
		return mcp.NewToolResultError("another memory_wait_changes is already in flight for this session; call it sequentially"), nil
	}
	defer s.waiters.Delete(waiterKey)

	matches := func(ev events.Event) (waitEvent, bool) {
		var p notePayload
		if err := json.Unmarshal(ev.Data, &p); err != nil || p.Path == "" {
			return waitEvent{}, false
		}
		// The watcher re-announces every write of the API and MCP (the
		// rename of its temporary file) about 100 ms later, with no ETag:
		// a waiter woke twice for each. Its frames carry the path for the
		// web UI's tree since IMP-160; the tool waits, as before, on the
		// writes that come with an ETag.
		if p.Source == "watcher" {
			return waitEvent{}, false
		}
		if !tok.AllowsPath(p.Path) {
			return waitEvent{}, false
		}
		if project != "" && topLevelProject(p.Path) != project {
			return waitEvent{}, false
		}
		if s.projectHidden(topLevelProject(p.Path)) {
			return waitEvent{}, false
		}
		return waitEvent{
			Seq:    ev.Seq,
			Topic:  string(ev.Topic),
			Action: p.Action,
			Path:   p.Path,
			ETag:   p.ETag,
			Source: p.Source,
		}, true
	}

	// Subscribe BEFORE replaying so nothing published in between is lost;
	// the Seq-based dedupe below drops the overlap.
	sub := s.events.Subscribe(events.TopicNote, events.TopicTree)
	defer sub.Unsubscribe()

	out := make([]waitEvent, 0, 4)
	buffered, resync := s.events.ReplaySince(cursor, events.TopicNote, events.TopicTree)
	last := cursor
	for _, ev := range buffered {
		if ev.Seq > last {
			last = ev.Seq
		}
		if we, ok := matches(ev); ok {
			out = append(out, we)
		}
	}
	if resync {
		// The cursor predates the retention window: report what we still
		// have plus resync so the caller reconciles via memory_recent.
		return mcp.NewToolResultJSON(map[string]any{
			"events": out, "cursor": maxSeq(last, s.events.Seq()), "resync": true,
		})
	}
	if len(out) > 0 {
		return mcp.NewToolResultJSON(map[string]any{"events": out, "cursor": last, "resync": false})
	}

	timer := time.NewTimer(time.Duration(timeoutS) * time.Second)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-sub.Ch:
			if !ok {
				return mcp.NewToolResultJSON(map[string]any{"events": out, "cursor": last, "resync": false})
			}
			if ev.Seq <= last {
				continue // overlap with the replay pass
			}
			last = ev.Seq
			we, match := matches(ev)
			if !match {
				continue
			}
			out = append(out, we)
			out = append(out, drainMatching(sub, &last, matches)...)
			return mcp.NewToolResultJSON(map[string]any{"events": out, "cursor": last, "resync": false})
		case <-timer.C:
			return mcp.NewToolResultJSON(map[string]any{
				"events": out, "cursor": last, "resync": false, "timed_out": true,
			})
		case <-ctx.Done():
			return mcp.NewToolResultJSON(map[string]any{"events": out, "cursor": last, "resync": false})
		}
	}
}

// drainMatching empties whatever is immediately available on the channel so
// a burst of writes comes back as one result instead of N round-trips.
func drainMatching(sub *events.Subscription, last *uint64, matches func(events.Event) (waitEvent, bool)) []waitEvent {
	var out []waitEvent
	for {
		select {
		case ev, ok := <-sub.Ch:
			if !ok {
				return out
			}
			if ev.Seq <= *last {
				continue
			}
			*last = ev.Seq
			if we, match := matches(ev); match {
				out = append(out, we)
			}
		default:
			return out
		}
	}
}

func maxSeq(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
