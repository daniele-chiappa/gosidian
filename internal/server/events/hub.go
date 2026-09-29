// Package events implements an in-process pub/sub hub for SSE delivery.
// Publishers (vault file watcher, MCP write handlers, gitsync post-pull)
// emit events; subscribers are HTTP requests holding an open SSE
// connection. The hub is intentionally simple and stateless beyond the
// in-memory subscriber list — events are not buffered for late
// subscribers, no persistence, no replay. The browser-side EventSource
// reconnect protocol with Last-Event-ID is supported but the hub
// returns no historical events; the SPA refetches affected resources
// over the regular REST API on reconnect.
//
// Channel sizing: each subscriber has a buffered channel of fixed size.
// On overflow the oldest event is dropped (drop-oldest policy) so a
// slow subscriber cannot stall the publisher. Subscribers that fall
// behind too often log a warning and are eventually disconnected by
// their HTTP handler.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Topic is the routing key for events. Subscribers select on a
// whitelist; publishers always emit on a single topic. Reserved topics
// have well-known shapes documented next to each constant.
type Topic string

const (
	// TopicTree fires when a note is created/deleted/renamed and the
	// sidebar tree should refresh. Payload: {action, path, project}.
	TopicTree Topic = "tree"
	// TopicNote fires when a specific note's content changes (writes
	// from MCP, web, or git pull). Payload: {path, etag, source}.
	// Subscribers usually filter for a single path of interest.
	TopicNote Topic = "note"
	// TopicSidebar fires when project flags or recent state changes.
	// Payload: {action, project}.
	TopicSidebar Topic = "sidebar"
	// TopicAudit fires on high-signal audit events (login, token
	// rotation, project rename). Payload mirrors audit.Entry.
	TopicAudit Topic = "audit"
	// TopicInsight fires when a self-improvement insight is recorded.
	// Payload: {action, path, category, confidence, alert, source}. The
	// owner SPA badge subscribes to live-update the pending count.
	TopicInsight Topic = "insight"
)

// Event is the wire shape. ID is a monotonic ULID-like sequence that
// Last-Event-ID can carry; today it's just a hub-local sequence number
// so reconnect resync is best-effort, not authoritative. Seq is the same
// sequence in numeric form — the cursor for ReplaySince (long-poll
// consumers such as memory_wait_changes).
type Event struct {
	ID    string          `json:"id"`
	Seq   uint64          `json:"seq"`
	Topic Topic           `json:"topic"`
	Time  time.Time       `json:"time"`
	Data  json.RawMessage `json:"data"`
}

// Subscription is what a subscriber holds. Events arrive on Ch; the
// publisher closes Ch when the subscription is terminated.
type Subscription struct {
	Topics []Topic
	Ch     chan Event
	hub    *Hub
}

// Unsubscribe drops the subscription and signals the publisher to stop
// sending. Idempotent.
func (s *Subscription) Unsubscribe() {
	if s.hub == nil {
		return
	}
	s.hub.unsubscribe(s)
	s.hub = nil
}

// Hub is the central pub/sub broker. The zero value is not usable —
// always call New.
type Hub struct {
	mu     sync.RWMutex
	subs   map[*Subscription]struct{}
	closed bool // set by Close: later subscriptions start already ended
	bufLen int
	seq    atomic.Uint64
	logger *slog.Logger
	// ring retains the last ringCap published events (oldest first) so a
	// long-poll consumer can replay the short gap between two calls via
	// ReplaySince. Guarded by its own mutex — the subscriber path stays on
	// the RWMutex untouched.
	ringMu  sync.Mutex
	ring    []Event
	ringCap int
}

// HubOptions configure a hub. BufLen is the per-subscriber channel
// capacity; defaults to 32 if zero. RingLen is the replay-buffer size
// for ReplaySince; defaults to 256 if zero.
type HubOptions struct {
	BufLen  int
	RingLen int
	Logger  *slog.Logger
}

// New constructs a Hub. Pass an HTTP server's BaseContext-derived
// logger if available; otherwise slog.Default() is used.
func New(opts HubOptions) *Hub {
	bl := opts.BufLen
	if bl <= 0 {
		bl = 32
	}
	rl := opts.RingLen
	if rl <= 0 {
		rl = 256
	}
	lg := opts.Logger
	if lg == nil {
		lg = slog.Default()
	}
	return &Hub{
		subs:    make(map[*Subscription]struct{}),
		bufLen:  bl,
		ringCap: rl,
		logger:  lg,
	}
}

// Subscribe registers a subscriber for a list of topics. An empty
// topics slice means "all topics".
func (h *Hub) Subscribe(topics ...Topic) *Subscription {
	sub := &Subscription{
		Topics: topics,
		Ch:     make(chan Event, h.bufLen),
		hub:    h,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		// A request still being served at shutdown must not start a wait
		// the shutdown would then sit out (BUG-069).
		close(sub.Ch)
		sub.hub = nil
		return sub
	}
	h.subs[sub] = struct{}{}
	return sub
}

func (h *Hub) unsubscribe(sub *Subscription) {
	h.mu.Lock()
	if _, ok := h.subs[sub]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.subs, sub)
	h.mu.Unlock()
	close(sub.Ch)
}

// Publish dispatches an event to all subscribers whose topic list
// matches. Empty Topics in the subscription matches every topic.
// Drop-oldest policy: if a subscriber's channel is full, the oldest
// queued event is discarded to make room for the new one.
func (h *Hub) Publish(topic Topic, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		h.logger.Warn("events: marshal failed", "topic", topic, "err", err)
		return
	}
	n := h.seq.Add(1)
	ev := Event{
		ID:    fmt.Sprintf("%d", n),
		Seq:   n,
		Topic: topic,
		Time:  time.Now().UTC(),
		Data:  payload,
	}
	h.ringMu.Lock()
	if len(h.ring) >= h.ringCap {
		copy(h.ring, h.ring[1:])
		h.ring[len(h.ring)-1] = ev
	} else {
		h.ring = append(h.ring, ev)
	}
	h.ringMu.Unlock()
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subs {
		if !subscriptionMatches(sub, topic) {
			continue
		}
		select {
		case sub.Ch <- ev:
		default:
			// Drop-oldest: pop one, push new. Best-effort.
			select {
			case <-sub.Ch:
			default:
			}
			select {
			case sub.Ch <- ev:
			default:
				h.logger.Warn("events: subscriber channel still full after drop", "topic", topic)
			}
		}
	}
}

func subscriptionMatches(sub *Subscription, topic Topic) bool {
	if len(sub.Topics) == 0 {
		return true
	}
	for _, t := range sub.Topics {
		if t == topic {
			return true
		}
	}
	return false
}

// Seq returns the sequence number of the last published event (0 when
// nothing was published yet). A long-poll consumer uses it as the
// starting cursor for ReplaySince.
func (h *Hub) Seq() uint64 {
	return h.seq.Load()
}

// ReplaySince returns the retained events with Seq > cursor whose topic is
// in topics (empty topics = all), oldest first. resync reports that the
// cursor predates the retention window — or comes from another hub
// incarnation — so events were lost and the caller must reconcile with a
// full re-read (e.g. memory_recent) instead of trusting the replay.
func (h *Hub) ReplaySince(cursor uint64, topics ...Topic) (out []Event, resync bool) {
	h.ringMu.Lock()
	defer h.ringMu.Unlock()
	seq := h.seq.Load()
	if cursor > seq {
		return nil, true // cursor from a previous process run
	}
	if cursor < seq {
		oldest := seq + 1 // nothing retained → any gap is a loss
		if len(h.ring) > 0 {
			oldest = h.ring[0].Seq
		}
		if cursor+1 < oldest {
			resync = true
		}
	}
	for _, ev := range h.ring {
		if ev.Seq <= cursor {
			continue
		}
		if len(topics) > 0 && !topicIn(ev.Topic, topics) {
			continue
		}
		out = append(out, ev)
	}
	return out, resync
}

func topicIn(t Topic, topics []Topic) bool {
	for _, x := range topics {
		if x == t {
			return true
		}
	}
	return false
}

// SubCount returns the active subscriber count. Useful for /metrics.
func (h *Hub) SubCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Close terminates every subscription, and every later one starts ended.
// Idempotent; called at shutdown.
func (h *Hub) Close(ctx context.Context) {
	h.mu.Lock()
	h.closed = true
	subs := make([]*Subscription, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()
	for _, s := range subs {
		h.unsubscribe(s)
	}
}
