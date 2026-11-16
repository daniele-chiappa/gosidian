package plugins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/pipeline"
)

func TestWaitOf(t *testing.T) {
	if _, ok := waitOf(map[string]any{"cost": 0.1}); ok {
		t.Error("an error without wait holds")
	}
	if _, ok := waitOf(map[string]any{"wait": false}); ok {
		t.Error("wait: false holds")
	}
	if until, ok := waitOf(map[string]any{"wait": true}); !ok || !until.IsZero() {
		t.Errorf("wait: true: %v %v", until, ok)
	}
	if until, ok := waitOf(map[string]any{"wait": map[string]any{"reason": "quota", "until": "2026-11-16T00:00:00Z"}}); !ok || !until.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("wait with until: %v %v", until, ok)
	}
	if until, ok := waitOf(map[string]any{"wait": map[string]any{"until": "soon"}}); !ok || !until.IsZero() {
		t.Errorf("wait with a time that does not read: %v %v", until, ok)
	}
}

func TestAway(t *testing.T) {
	var hold pipeline.Hold
	if err := away(context.Background(), errors.New("plugin x: connection refused")); !errors.As(err, &hold) {
		t.Errorf("a plugin away is no hold: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := away(ctx, errors.New("context canceled")); errors.As(err, &hold) {
		t.Error("a step out of time is held")
	}
}
