package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

func TestStream_ReadersAndTries(t *testing.T) {
	s := newStream(12)
	w := s.begin("r1", "p/a.md", "llm", -1)
	w.write("Ciao")
	var a StreamCursor
	if evs, _ := s.Read(&a); len(evs) != 1 || evs[0].Kind != StreamDelta || evs[0].Text != "Ciao" || evs[0].Node != "llm" || evs[0].Index != nil {
		t.Fatalf("first read: %+v", evs)
	}
	w.write(" è")
	if evs, _ := s.Read(&a); len(evs) != 1 || evs[0].Text != " è" {
		t.Fatalf("the rest: %+v", evs)
	}
	if evs, _ := s.Read(&a); len(evs) != 0 {
		t.Fatalf("nothing new: %+v", evs)
	}
	// A new try starts the part again, up to the cap of 12 bytes.
	w = s.begin("r1", "p/a.md", "llm", -1)
	w.write("Un altro testo")
	evs, _ := s.Read(&a)
	if len(evs) != 2 || evs[0].Kind != StreamReset || evs[1].Text != "Un altro tes" {
		t.Fatalf("the second try: %+v", evs)
	}
	// A reader that comes later reads the text so far, with no reset.
	var b StreamCursor
	if evs, _ := s.Read(&b); len(evs) != 1 || evs[0].Kind != StreamDelta || evs[0].Text != "Un altro tes" {
		t.Fatalf("a later reader: %+v", evs)
	}
	// The element of an each is a part of its own.
	s.begin("r1", "p/a.md", "llm", 2).write("x")
	if evs, _ := s.Read(&b); len(evs) != 1 || evs[0].Index == nil || *evs[0].Index != 2 {
		t.Fatalf("an element: %+v", evs)
	}
	// The cap cuts between two characters.
	u := newStream(5)
	u.begin("r", "p", "n", -1).write("abcdè")
	if evs, _ := u.Read(&StreamCursor{}); len(evs) != 1 || evs[0].Text != "abcd" {
		t.Fatalf("cut: %+v", evs)
	}
	s.close()
	select {
	case <-s.Ended():
	default:
		t.Fatal("not ended")
	}
	if s.begin("r2", "p", "n", -1) != nil {
		t.Fatal("a closed stream takes a part")
	}
}

// ssePiece writes one event of a provider's stream.
func ssePiece(w http.ResponseWriter, data string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

// readStreamUntil reads s until it has n events, or fails after 5 s.
func readStreamUntil(t *testing.T, s *Stream, cur *StreamCursor, n int) []StreamEvent {
	t.Helper()
	var all []StreamEvent
	deadline := time.After(5 * time.Second)
	for {
		evs, wake := s.Read(cur)
		all = append(all, evs...)
		if len(all) >= n {
			return all
		}
		select {
		case <-wake:
		case <-deadline:
			t.Fatalf("the stream has %+v, not %d events", all, n)
		}
	}
}

// TestEngine_LLMStreamsThroughPipelineRun: the text of a streaming node
// of a child run reaches whoever follows its parent as it comes; a run
// nobody follows calls the model whole.
func TestEngine_LLMStreamsThroughPipelineRun(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		mu.Lock()
		bodies = append(bodies, got)
		mu.Unlock()
		if got["stream"] != true {
			_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"Intera"}}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		ssePiece(w, `{"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Buon"},"finish_reason":null}]}`)
		<-release
		ssePiece(w, `{"model":"m","choices":[{"index":0,"delta":{"content":"giorno "},"finish_reason":"stop"}]}`)
		ssePiece(w, `{"model":"m","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`)
		ssePiece(w, "[DONE]")
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{Providers: []Provider{{Name: "o", BaseURL: srv.URL + "/v1", Model: "m"}}})
	host.notes["p/pipelines/child.md"] = pipelineNote(`
nodes:
  - {id: llm, use: llm.chat, with: {prompt: "ciao", stream: true}}
`)
	host.notes["p/pipelines/parent.md"] = pipelineNote(`
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: pipelines/child}}
`)
	run, _, s, err := e.StartStream(StartRequest{Path: "p/pipelines/parent.md", Trigger: TriggerManual, IdemKey: "uno"})
	if err != nil {
		t.Fatal(err)
	}
	var cur StreamCursor
	evs := readStreamUntil(t, s, &cur, 1)
	child := evs[0].Run
	if evs[0].Kind != StreamDelta || evs[0].Text != "Buon" || evs[0].Node != "llm" || evs[0].Pipeline != "p/pipelines/child.md" || child == run.ID {
		t.Fatalf("the first piece: %+v", evs[0])
	}
	// The same start again reads the same stream, from its start.
	again, existing, s2, err := e.StartStream(StartRequest{Path: "p/pipelines/parent.md", Trigger: TriggerManual, IdemKey: "uno"})
	if err != nil || !existing || again.ID != run.ID || s2 != s {
		t.Fatalf("the same start: %v %v %v", err, existing, s2 == s)
	}
	if evs := readStreamUntil(t, s2, &StreamCursor{}, 1); evs[0].Text != "Buon" {
		t.Fatalf("the second reader: %+v", evs)
	}
	e.CloseStream(s2)
	close(release)
	waitStatus(t, e, run.ID, jobs.Done)
	if evs := readStreamUntil(t, s, &cur, 1); evs[0].Text != "giorno " {
		t.Fatalf("the rest: %+v", evs)
	}
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, child, "llm").Output, &out)
	usage, _ := out["usage"].(map[string]any)
	if out["text"] != "Buongiorno" || out["finish_reason"] != "stop" || usage["total_tokens"] != float64(9) || usage["estimated"] != nil {
		t.Fatalf("the node's output: %v", out)
	}
	mu.Lock()
	first := bodies[0]
	mu.Unlock()
	if opts, _ := first["stream_options"].(map[string]any); opts["include_usage"] != true {
		t.Fatalf("the streaming call: %v", first)
	}
	// With its last reader the stream closes, for the child too.
	e.CloseStream(s)
	select {
	case <-s.Ended():
	default:
		t.Fatal("the stream is open")
	}
	if e.streams.of(run.ID) != nil || e.streams.of(child) != nil {
		t.Fatal("a run still writes to the stream")
	}

	plain, _, err := e.Start(StartRequest{Path: "p/pipelines/child.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, plain.ID, jobs.Done)
	_ = json.Unmarshal(stepOf(t, e, plain.ID, "llm").Output, &out)
	mu.Lock()
	last := bodies[len(bodies)-1]
	mu.Unlock()
	if out["text"] != "Intera" || last["stream"] != false || last["stream_options"] != nil {
		t.Fatalf("a run nobody follows: %v, asked %v", out, last)
	}
}

// TestEngine_LLMStreamTriedAgain: a stream cut short fails the try, the
// next one starts the text again (reset); a stream with no usage counts a
// token a piece.
func TestEngine_LLMStreamTriedAgain(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			ssePiece(w, `{"choices":[{"index":0,"delta":{"content":"Prima"}}]}`)
			<-gate
			return
		}
		ssePiece(w, `{"choices":[{"index":0,"delta":{"content":"Seconda"}}]}`)
		ssePiece(w, `{"choices":[{"index":0,"delta":{"content":" volta"},"finish_reason":"stop"}]}`)
		ssePiece(w, "[DONE]")
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{Providers: []Provider{{Name: "o", BaseURL: srv.URL + "/v1", Model: "m"}}})
	host.notes["p/pipelines/bot.md"] = pipelineNote(`
nodes:
  - {id: llm, use: llm.chat, retry: {attempts: 2, backoff: 10ms}, with: {prompt: "ciao", stream: true}}
`)
	run, _, s, err := e.StartStream(StartRequest{Path: "p/pipelines/bot.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	defer e.CloseStream(s)
	var cur StreamCursor
	if evs := readStreamUntil(t, s, &cur, 1); evs[0].Text != "Prima" {
		t.Fatalf("the first try: %+v", evs)
	}
	close(gate)
	waitStatus(t, e, run.ID, jobs.Done)
	evs := readStreamUntil(t, s, &cur, 2)
	if evs[0].Kind != StreamReset || evs[1].Kind != StreamDelta || evs[1].Text != "Seconda volta" {
		t.Fatalf("the second try: %+v", evs)
	}
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "llm").Output, &out)
	usage, _ := out["usage"].(map[string]any)
	if out["text"] != "Seconda volta" || usage["completion_tokens"] != float64(2) || usage["estimated"] != true {
		t.Fatalf("the output: %v", out)
	}
}

// TestEngine_LLMStreamFromAWholeAnswer: a provider that answers whole when
// asked for a stream gives the stream its text at once.
func TestEngine_LLMStreamFromAWholeAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":" Tutta insieme. "},"finish_reason":"stop"}],"usage":{"total_tokens":4}}`)
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{Providers: []Provider{{Name: "o", BaseURL: srv.URL + "/v1", Model: "m"}}})
	host.notes["p/pipelines/bot.md"] = pipelineNote(`
nodes:
  - {id: llm, use: llm.chat, with: {prompt: "ciao", stream: true}}
`)
	run, _, s, err := e.StartStream(StartRequest{Path: "p/pipelines/bot.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	defer e.CloseStream(s)
	waitStatus(t, e, run.ID, jobs.Done)
	if evs := readStreamUntil(t, s, &StreamCursor{}, 1); evs[0].Text != "Tutta insieme." {
		t.Fatalf("the stream: %+v", evs)
	}
}
