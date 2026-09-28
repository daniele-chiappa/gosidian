package ratelimit

import (
	"testing"
	"time"
)

func TestWindow(t *testing.T) {
	l := New(time.Minute, 2)
	now := time.Now()
	if !l.Allow("a", now) || !l.Allow("a", now) || l.Allow("a", now) || !l.Allow("b", now) {
		t.Error("cap per key not applied")
	}
	if !l.Allow("a", now.Add(2*time.Minute)) {
		t.Error("window did not slide")
	}
}
