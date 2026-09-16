package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/server/events"
)

type seenEvent struct {
	Topic  events.Topic
	Action string
	Path   string
}

// drainEvents collects everything published on sub until the channel has
// been quiet for a while (at least n events are awaited before the quiet
// period starts counting), so a tool that publishes more than expected does
// not hide the later events.
func drainEvents(t *testing.T, sub *events.Subscription, n int) []seenEvent {
	t.Helper()
	var out []seenEvent
	deadline := time.After(3 * time.Second)
	for {
		var quiet <-chan time.Time
		if len(out) >= n {
			quiet = time.After(300 * time.Millisecond)
		}
		select {
		case ev, ok := <-sub.Ch:
			if !ok {
				return out
			}
			var d struct {
				Action string `json:"action"`
				Path   string `json:"path"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			out = append(out, seenEvent{Topic: ev.Topic, Action: d.Action, Path: d.Path})
		case <-quiet:
			return out
		case <-deadline:
			return out
		}
	}
}

func hasEvent(evs []seenEvent, topic events.Topic, action, path string) bool {
	for _, e := range evs {
		if e.Topic == topic && e.Action == action && e.Path == path {
			return true
		}
	}
	return false
}

func eventServer(t *testing.T) (*Server, string, *events.Hub) {
	t.Helper()
	s, _, dir := newTestServer(t)
	hub := events.New(events.HubOptions{})
	s.SetEvents(hub)
	return s, dir, hub
}

// Every tool that mutates the vault must publish on the hub: the SPA and
// memory_wait_changes have no other signal (the fsnotify watcher only
// re-indexes) — BUG-036.
func TestMutatingTools_PublishEvents(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		setup func(s *Server, dir string)
		act   func(s *Server)
		want  []seenEvent
	}{
		{"edit", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/a.md", "hello world\n")
		}, func(s *Server) {
			_, _ = s.handleEdit(ctx, call(map[string]any{"path": "p/a.md", "old_string": "hello", "new_string": "bye"}))
		}, []seenEvent{{events.TopicNote, "update", "p/a.md"}}},
		{"rename", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/a.md", "x\n")
		}, func(s *Server) {
			_, _ = s.handleRenameNote(ctx, call(map[string]any{"from": "p/a.md", "to": "p/b.md"}))
		}, []seenEvent{{events.TopicNote, "delete", "p/a.md"}, {events.TopicTree, "delete", "p/a.md"}, {events.TopicNote, "create", "p/b.md"}, {events.TopicTree, "create", "p/b.md"}}},
		{"move", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/a.md", "x\n")
			writeVaultFile(t, dir, "q/README.md", "q\n")
		}, func(s *Server) {
			_, _ = s.handleMoveNote(ctx, call(map[string]any{"path": "p/a.md", "project": "q"}))
		}, []seenEvent{{events.TopicNote, "delete", "p/a.md"}, {events.TopicNote, "create", "q/a.md"}}},
		{"append creates", func(s *Server, dir string) {},
			func(s *Server) {
				_, _ = s.handleAppend(ctx, call(map[string]any{"path": "p/new.md", "content": "first"}))
			}, []seenEvent{{events.TopicNote, "create", "p/new.md"}, {events.TopicTree, "create", "p/new.md"}}},
		{"append updates", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/a.md", "x\n")
		}, func(s *Server) {
			_, _ = s.handleAppend(ctx, call(map[string]any{"path": "p/a.md", "content": "more"}))
		}, []seenEvent{{events.TopicNote, "update", "p/a.md"}}},
		{"ask", func(s *Server, dir string) {},
			func(s *Server) {
				_, _ = s.handleAsk(ctx, call(map[string]any{"project": "p", "question": "why?"}))
			}, []seenEvent{{events.TopicNote, "create", "p/docs/open-questions.md"}}},
		{"compact", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/log.md", "# L\n\n## 2026-01-01 a\n\nx\n\n## 2026-01-02 b\n\ny\n")
		}, func(s *Server) {
			_, _ = s.handleCompact(ctx, call(map[string]any{"path": "p/log.md", "keep_last_n": 1, "archive_summary": "old"}))
		}, []seenEvent{{events.TopicNote, "update", "p/log.md"}}},
		{"refresh_hot", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/hot.md", "# H\n\n"+recentMarkerOpen+"\nstale\n"+recentMarkerClose+"\n")
			writeVaultFile(t, dir, "p/memory/decisions.md", "# D\n\n## ADR-001 one\n\nx\n")
		}, func(s *Server) {
			_, _ = s.handleRefreshHot(ctx, call(map[string]any{"project": "p"}))
		}, []seenEvent{{events.TopicNote, "update", "p/hot.md"}}},
		{"rename_project", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/a.md", "x\n")
		}, func(s *Server) {
			_, _ = s.handleRenameProject(ctx, call(map[string]any{"from": "p", "to": "r"}))
		}, []seenEvent{{events.TopicTree, "rename_project", "p"}}},
		{"delete_project", func(s *Server, dir string) {
			writeVaultFile(t, dir, "p/a.md", "x\n")
		}, func(s *Server) {
			_, _ = s.handleDeleteProject(ctx, call(map[string]any{"name": "p"}))
		}, []seenEvent{{events.TopicTree, "delete_project", "p"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, dir, hub := eventServer(t)
			tc.setup(s, dir)
			sub := hub.Subscribe(events.TopicNote, events.TopicTree)
			defer sub.Unsubscribe()
			tc.act(s)
			got := drainEvents(t, sub, len(tc.want))
			for _, w := range tc.want {
				if !hasEvent(got, w.Topic, w.Action, w.Path) {
					t.Errorf("missing event %+v; got %+v", w, got)
				}
			}
		})
	}
}

// keep the os import honest for setups that only use writeVaultFile
var _ = os.MkdirAll
var _ = filepath.Join
