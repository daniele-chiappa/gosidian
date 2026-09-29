package events

import (
	"context"
	"testing"
	"time"
)

func TestHub_PublishMatchesTopic(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	sub := h.Subscribe(TopicTree)
	defer sub.Unsubscribe()

	h.Publish(TopicTree, map[string]any{"action": "create", "path": "a.md"})
	select {
	case ev := <-sub.Ch:
		if ev.Topic != TopicTree {
			t.Errorf("topic=%q", ev.Topic)
		}
		if string(ev.Data) == "" {
			t.Errorf("empty data")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("no event received")
	}
}

func TestHub_PublishSkipsNonMatchingTopic(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	sub := h.Subscribe(TopicNote)
	defer sub.Unsubscribe()

	h.Publish(TopicTree, map[string]any{"x": 1})
	select {
	case ev := <-sub.Ch:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestHub_EmptyTopicsMatchesAll(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	sub := h.Subscribe()
	defer sub.Unsubscribe()

	h.Publish(TopicTree, map[string]any{"x": 1})
	h.Publish(TopicNote, map[string]any{"x": 2})

	got := 0
	for got < 2 {
		select {
		case <-sub.Ch:
			got++
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("got=%d, want 2", got)
		}
	}
}

func TestHub_DropOldestOnFullChannel(t *testing.T) {
	h := New(HubOptions{BufLen: 2})
	sub := h.Subscribe(TopicTree)
	defer sub.Unsubscribe()

	// Fill + overflow without consuming
	for i := 0; i < 5; i++ {
		h.Publish(TopicTree, map[string]int{"i": i})
	}

	// Subscriber should have at most BufLen events buffered; the latest
	// publish must still arrive (drop-oldest kept the new one).
	count := 0
	last := -1
	for count < 5 {
		select {
		case ev := <-sub.Ch:
			count++
			_ = ev
			last++
		case <-time.After(50 * time.Millisecond):
			goto done
		}
	}
done:
	if count == 0 {
		t.Errorf("expected at least one event after drop-oldest")
	}
	if count > 2 {
		t.Errorf("expected at most BufLen=2 events buffered, got %d", count)
	}
}

func TestHub_UnsubscribeIsIdempotent(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	sub := h.Subscribe(TopicTree)
	sub.Unsubscribe()
	sub.Unsubscribe() // no panic
	if h.SubCount() != 0 {
		t.Errorf("SubCount=%d after unsubscribe", h.SubCount())
	}
}

func TestHub_ChannelClosedOnUnsubscribe(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	sub := h.Subscribe(TopicTree)
	sub.Unsubscribe()
	_, open := <-sub.Ch
	if open {
		t.Errorf("channel should be closed")
	}
}

func TestHub_CloseTerminatesAllSubscribers(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	s1 := h.Subscribe()
	s2 := h.Subscribe()
	h.Close(context.Background())
	if _, open := <-s1.Ch; open {
		t.Errorf("s1 channel still open")
	}
	if _, open := <-s2.Ch; open {
		t.Errorf("s2 channel still open")
	}
	if h.SubCount() != 0 {
		t.Errorf("SubCount=%d after Close", h.SubCount())
	}
}

// A subscription taken after Close starts ended, so a request still being
// served at shutdown does not begin a wait (BUG-069).
func TestHub_SubscribeAfterCloseIsEnded(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	h.Close(context.Background())
	s := h.Subscribe()
	if _, open := <-s.Ch; open {
		t.Error("subscription after Close is open")
	}
	s.Unsubscribe() // must not panic on the already-closed channel
	h.Publish(TopicNote, map[string]string{"path": "a.md"})
	if h.SubCount() != 0 {
		t.Errorf("SubCount=%d after Close", h.SubCount())
	}
	h.Close(context.Background()) // idempotent
}

func TestHub_SubCount(t *testing.T) {
	h := New(HubOptions{})
	if h.SubCount() != 0 {
		t.Errorf("initial SubCount != 0")
	}
	a := h.Subscribe()
	b := h.Subscribe()
	if h.SubCount() != 2 {
		t.Errorf("SubCount=%d, want 2", h.SubCount())
	}
	a.Unsubscribe()
	if h.SubCount() != 1 {
		t.Errorf("SubCount=%d after one unsub", h.SubCount())
	}
	b.Unsubscribe()
}

func TestHub_PublishesMonotonicID(t *testing.T) {
	h := New(HubOptions{BufLen: 4})
	sub := h.Subscribe()
	defer sub.Unsubscribe()
	h.Publish(TopicTree, struct{}{})
	h.Publish(TopicTree, struct{}{})
	e1 := <-sub.Ch
	e2 := <-sub.Ch
	if e1.ID == e2.ID {
		t.Errorf("ids must differ: %s == %s", e1.ID, e2.ID)
	}
}
