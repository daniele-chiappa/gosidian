package pipeline

import (
	"sync"
	"unicode/utf8"

	"github.com/gosidian/gosidian/internal/jobs"
)

// The text of a run as it comes (IMP-191 phase 8): an llm.chat with
// stream: true sends its answer, piece by piece, to whoever started the run
// asking for it, or the run that started it with pipeline.run. A stream
// lives in memory as long as the requests that read it: a run nobody
// listens to calls its models as before, and a run resumed after a restart
// has no stream.

// maxStreamParts caps the texts of a stream: a streaming node in an each
// over thousands of items streams the first ones only.
const maxStreamParts = 64

// Kinds of StreamEvent.
const (
	StreamDelta = "delta" // Text goes on after the part's text
	StreamReset = "reset" // the part starts again: a new try of its node
)

// StreamEvent is what a stream has new for a reader: the text of a part,
// one node's call of a run, from where the reader left it.
type StreamEvent struct {
	Kind     string `json:"-"`
	Run      string `json:"run"`
	Pipeline string `json:"pipeline"`
	Node     string `json:"node"`
	// Index is the element of the node's each, absent outside one.
	Index *int   `json:"index,omitempty"`
	Text  string `json:"text,omitempty"`
}

type streamPart struct {
	run, pipeline, node string
	index               int // -1 outside an each
	try                 int
	text                []byte
}

func (p *streamPart) event(kind, text string) StreamEvent {
	ev := StreamEvent{Kind: kind, Run: p.run, Pipeline: p.pipeline, Node: p.node, Text: text}
	if p.index >= 0 {
		i := p.index
		ev.Index = &i
	}
	return ev
}

// Stream is the text that the streaming nodes of a run, and of the runs it
// starts, write for the readers that follow it, each with its cursor.
type Stream struct {
	mu    sync.Mutex
	parts []*streamPart
	// wake is closed and made again at every change; ended at the close.
	wake, ended chan struct{}
	closed      bool
	max         int // bytes of a part
	// runs write to it (streams.drop); readers read it (Engine.CloseStream).
	runs    []string
	readers int
}

func newStream(max int) *Stream {
	return &Stream{wake: make(chan struct{}), ended: make(chan struct{}), max: max}
}

// StreamCursor is where a reader is in a stream.
type StreamCursor struct {
	seen []partSeen
}

type partSeen struct{ try, off int }

// Read gives what the stream has new for the cursor, and a channel closed
// at its next change.
func (s *Stream) Read(c *StreamCursor) ([]StreamEvent, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var evs []StreamEvent
	for i, p := range s.parts {
		if i == len(c.seen) {
			c.seen = append(c.seen, partSeen{try: p.try})
		}
		sn := &c.seen[i]
		if sn.try != p.try {
			if sn.off > 0 {
				evs = append(evs, p.event(StreamReset, ""))
			}
			*sn = partSeen{try: p.try}
		}
		if len(p.text) > sn.off {
			evs = append(evs, p.event(StreamDelta, string(p.text[sn.off:])))
			sn.off = len(p.text)
		}
	}
	return evs, s.wake
}

// Ended is closed when the stream closes: the server shuts down, or its
// last reader went.
func (s *Stream) Ended() <-chan struct{} { return s.ended }

func (s *Stream) changed() {
	close(s.wake)
	s.wake = make(chan struct{})
}

func (s *Stream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ended)
		s.changed()
	}
}

// begin starts the text of a node's call in s: a call again (a new try)
// starts its part again. nil when the stream is closed or full.
func (s *Stream) begin(run, pipeline, node string, index int) *streamWriter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	for _, p := range s.parts {
		if p.run == run && p.node == node && p.index == index {
			p.try++
			p.text = nil
			s.changed()
			return &streamWriter{s: s, p: p}
		}
	}
	if len(s.parts) >= maxStreamParts {
		return nil
	}
	p := &streamPart{run: run, pipeline: pipeline, node: node, index: index}
	s.parts = append(s.parts, p)
	return &streamWriter{s: s, p: p}
}

// streamWriter is a node's call writing into its run's stream.
type streamWriter struct {
	s *Stream
	p *streamPart
}

// write adds text to the call's part, up to the stream's cap.
func (w *streamWriter) write(text string) {
	if w == nil || text == "" {
		return
	}
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.max - len(w.p.text)
	if s.closed || room <= 0 {
		return
	}
	if len(text) > room {
		text = clipUTF8(text, room)
	}
	w.p.text = append(w.p.text, text...)
	s.changed()
}

// clipUTF8 is the longest start of s of at most n bytes that ends between
// two characters.
func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// streams are the open streams by the runs that write to them.
type streams struct {
	mu   sync.Mutex
	runs map[string]*Stream
}

func (r *streams) of(run string) *Stream {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[run]
}

// attach makes run write to s.
func (r *streams) attach(run string, s *Stream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = map[string]*Stream{}
	}
	r.runs[run] = s
	s.mu.Lock()
	s.runs = append(s.runs, run)
	s.mu.Unlock()
}

// detach makes run write to no stream.
func (r *streams) detach(run string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, run)
}

// drop closes s, and its runs write to it no more.
func (r *streams) drop(s *Stream) {
	r.mu.Lock()
	s.mu.Lock()
	for _, id := range s.runs {
		if r.runs[id] == s {
			delete(r.runs, id)
		}
	}
	s.runs = nil
	s.mu.Unlock()
	r.mu.Unlock()
	s.close()
}

// all is every open stream.
func (r *streams) all() []*Stream {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[*Stream]bool{}
	var out []*Stream
	for _, s := range r.runs {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// streamWriter is the way of the node's call into its run's stream; nil
// when nobody follows the run.
func (env *Env) streamWriter() *streamWriter {
	if env.Engine == nil {
		return nil
	}
	s := env.Engine.streams.of(env.Run.ID)
	if s == nil {
		return nil
	}
	index := -1
	if i, ok := env.Scope["index"].(float64); ok {
		index = int(i)
	}
	return s.begin(env.Run.ID, env.Run.Ref, env.Node.ID, index)
}

// StartStream starts a run as Start does, with a stream of the text its
// streaming nodes write, and of the runs it starts: the stream of the run
// when it has one already (the same start again), with its text so far.
// The caller ends its reading with CloseStream.
func (e *Engine) StartStream(req StartRequest) (jobs.Run, bool, *Stream, error) {
	req.stream = newStream(e.cfg.MaxOutputBytes)
	run, existing, err := e.Start(req)
	if err != nil {
		return run, existing, nil, err
	}
	s := e.streams.of(run.ID)
	if s == nil {
		// It ended before: a stream with nothing in it.
		s = req.stream
	}
	s.mu.Lock()
	s.readers++
	s.mu.Unlock()
	return run, existing, s, nil
}

// CloseStream ends a reader's reading of s: with its last reader the
// stream closes, and its runs write to it no more.
func (e *Engine) CloseStream(s *Stream) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.readers--
	last := s.readers <= 0
	s.mu.Unlock()
	if last {
		e.streams.drop(s)
	}
}

// CloseStreams closes every stream, for the server's shutdown: their
// readers answer with the runs as they are.
func (e *Engine) CloseStreams() {
	for _, s := range e.streams.all() {
		e.streams.drop(s)
	}
}
