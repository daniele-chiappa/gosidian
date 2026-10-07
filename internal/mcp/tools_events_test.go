package mcp

import (
	"encoding/json"
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
				Action  string `json:"action"`
				Path    string `json:"path"`
				Project string `json:"project"` // the sidebar topic names a project
			}
			_ = json.Unmarshal(ev.Data, &d)
			if d.Path == "" {
				d.Path = d.Project
			}
			out = append(out, seenEvent{Topic: ev.Topic, Action: d.Action, Path: d.Path})
		case <-quiet:
			return out
		case <-deadline:
			return out
		}
	}
}

// The events of every tool that writes are checked by the write
// conformance suite (write_conformance_test.go), which replaced the table
// that lived here (BUG-036).
