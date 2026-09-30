// Package mcp — self-improvement nudge middleware (Phase 2).
//
// A second tool-handler middleware (alongside instrumentMiddleware) that, for
// opted-in tokens with the loop enabled, appends a short nudge to successful
// tool results on a configured cadence. The nudge invites the agent to record
// real-usage friction via memory_self_improve. All cadence state is in-memory
// and advisory — lost on restart, which is fine for a best-effort prompt.
// See plan 20260608-self-improve-feedback-loop.
package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// selfImproveNudgeText is appended to a successful tool result when the
// cadence fires. It carries a capability hint (so the agent doesn't re-suggest
// features that already exist) and a privacy guardrail.
const selfImproveNudgeText = "[gosidian self-improvement] You've made several calls this session. " +
	"If you hit any friction USING gosidian itself (the MCP tools/UI) — a missing capability, a confusing result, a docs gap, a slow or token-heavy path — record it with memory_self_improve(category, title, friction, confidence). " +
	"Describe it in the abstract; never include note content, project names, paths, or user data. " +
	"gosidian already has: full-text + tag/importance search, backlinks, outlinks, graph, bootstrap, handoffs, todos, lint, scaffolding, attachments. " +
	"Ignore this if nothing comes to mind."

// nudgeMaxSessions soft-caps the per-session tracker map; on overflow the
// advisory state is dropped rather than grown unbounded.
const nudgeMaxSessions = 4096

// nudgeTracker counts tool calls per session and decides when to emit a nudge.
type nudgeTracker struct {
	mu       sync.Mutex
	sessions map[string]*nudgeSession
}

type nudgeSession struct {
	calls      int
	nudgesSent int
	lastNudge  time.Time
}

func newNudgeTracker() *nudgeTracker {
	return &nudgeTracker{sessions: make(map[string]*nudgeSession)}
}

// tick records one call for the session key and reports whether a nudge should
// be emitted now, applying the every-N cadence, the per-session cap and the
// cooldown. now is passed in for testability.
func (t *nudgeTracker) tick(key string, everyN, maxPerSession int, cooldown time.Duration, now time.Time) bool {
	if everyN <= 0 || maxPerSession <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sessions) > nudgeMaxSessions {
		t.sessions = make(map[string]*nudgeSession) // soft cap: drop advisory state
	}
	sess := t.sessions[key]
	if sess == nil {
		sess = &nudgeSession{}
		t.sessions[key] = sess
	}
	sess.calls++
	if sess.calls < everyN {
		return false
	}
	if sess.nudgesSent >= maxPerSession {
		return false
	}
	if !sess.lastNudge.IsZero() && now.Sub(sess.lastNudge) < cooldown {
		return false
	}
	sess.calls = 0
	sess.nudgesSent++
	sess.lastNudge = now
	return true
}

// selfImproveNudgeMiddleware appends the nudge to successful tool results for
// opted-in tokens on the configured cadence. It never nudges on errors, on the
// memory_self_improve call itself, or when the loop is disabled / the token
// isn't opted in.
func (s *Server) selfImproveNudgeMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := next(ctx, req)
		if !s.selfImproveEnabled || err != nil || result == nil || result.IsError {
			return result, err
		}
		if req.Params.Name == "memory_self_improve" {
			return result, err
		}
		tok := s.tokenFromContext(ctx)
		if tok == nil || !tok.SelfImproveOptIn {
			return result, err
		}
		key := correlationIDFromContext(ctx)
		if key == "" {
			key = tok.ID
		}
		if s.nudges.tick(key, s.selfImproveEveryN, s.selfImproveMaxPerSession, s.selfImproveCooldown, time.Now()) {
			appendNotice(result, selfImproveNudgeText)
		}
		return result, err
	}
}
