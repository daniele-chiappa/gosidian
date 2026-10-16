package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// UpdateNote makes the fake a NoteUpdater.
func (h *fakeHost) UpdateNote(project, path string, build func([]byte) ([]byte, error), actor string) (WriteResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if actor != "pipeline:"+h.runRef(path) {
		return WriteResult{}, errors.New("written as " + actor)
	}
	var existing []byte
	c, exists := h.notes[path]
	if exists {
		existing = []byte(c)
	}
	out, err := build(existing)
	if err != nil {
		return WriteResult{}, err
	}
	h.notes[path] = string(out)
	return WriteResult{Path: path, Created: !exists}, nil
}

const chatPipeline = `
trigger: {on: chat}
answer: reply
nodes:
  - id: reply
    use: starlark
    with:
      input: "{{ trigger }}"
      code: |
        output = {"text": "eco: " + (input.get("message") or ""), "kind": input["kind"]}
`

func TestEngine_ChatTrigger(t *testing.T) {
	e, host := newEngine(t, Config{MaxSyncWait: 3 * time.Second})
	host.notes["p/bot.md"] = pipelineNote(chatPipeline)
	host.notes["p/ext.md"] = pipelineNote("trigger: {on: external}\nnodes: [{id: a, use: starlark, with: {code: 'output = 1'}}]")

	// A run token starts a chat run of a chat pipeline: interactive, with
	// the kind set.
	run, _, err := e.Start(StartRequest{Path: "p/bot.md", Trigger: TriggerExternal, Input: map[string]any{"message": "ciao", "session": "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != TriggerChat || run.Priority != jobs.PriorityInteractive {
		t.Fatalf("trigger %q priority %d", run.Trigger, run.Priority)
	}
	run, err = e.Wait(context.Background(), run.ID, e.SyncWait(time.Hour))
	if err != nil || run.Status != jobs.Done {
		t.Fatalf("wait: %s %v", run.Status, err)
	}
	steps, _ := e.Runner().Store().Steps(run.ID)
	answer, ok := AnswerOf(run, steps)
	if a, _ := answer.(map[string]any); !ok || a["text"] != "eco: ciao" || a["kind"] != "message" {
		t.Errorf("answer: %v %v", answer, ok)
	}
	if s := Summary(run, steps); s.Answer == nil {
		t.Errorf("the summary has no answer: %+v", s)
	}
	if b := Brief(run); !b.Interactive {
		t.Errorf("brief: %+v", b)
	}
	// A rating is a chat run too.
	run, _, err = e.Start(StartRequest{Path: "p/bot.md", Trigger: TriggerChat, Input: map[string]any{"kind": "rating", "turn": run.ID, "rating": -3}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	// By hand too, with a chat's input.
	if _, _, err := e.Start(StartRequest{Path: "p/bot.md", Trigger: TriggerManual, Input: map[string]any{"text": "no message"}}); err == nil || !strings.Contains(err.Error(), "message") {
		t.Errorf("a manual start without a message: %v", err)
	}
	// Another pipeline's runs stay as they were.
	run, _, err = e.Start(StartRequest{Path: "p/ext.md", Trigger: TriggerExternal})
	if err != nil || run.Trigger != TriggerExternal || run.Priority != jobs.PriorityNormal {
		t.Errorf("external: %+v %v", run, err)
	}
	if _, ok := AnswerOf(waitStatus(t, e, run.ID, jobs.Done), nil); ok {
		t.Error("an answer from a pipeline that names none")
	}
	if _, _, err := e.Start(StartRequest{Path: "p/ext.md", Trigger: TriggerChat, Input: map[string]any{"message": "x"}}); err == nil {
		t.Error("a chat start of an external pipeline")
	}
}

func TestEngine_WaitStopsAtAWaitAndAfterItsTime(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pl.md"] = pipelineNote(`
nodes:
  - {id: hold, use: wait.call, with: {timeout: 1h}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/pl.md", Trigger: TriggerManual})
	got, err := e.Wait(context.Background(), run.ID, 5*time.Second)
	if err != nil || got.Status != jobs.Waiting {
		t.Fatalf("a run that waits for a call: %s %v", got.Status, err)
	}
	if e.SyncWait(0) != 0 || e.SyncWait(5*time.Minute) != time.Minute {
		t.Errorf("SyncWait: %s %s", e.SyncWait(0), e.SyncWait(5*time.Minute))
	}
}

func TestCheckChatInput(t *testing.T) {
	good := []map[string]any{
		{"message": "ciao"},
		{"kind": "message", "message": "ciao", "history": []any{map[string]any{"role": "user", "content": "a"}, map[string]any{"role": "assistant", "content": "b"}},
			"scopes": []any{"magazzino"}, "session": "s", "user": "mario", "extra": 1},
		{"kind": "rating", "turn": "abc", "rating": 10.0, "comment": "ottima"},
		{"kind": "rating", "turn": "abc", "rating": -10},
	}
	for i, in := range good {
		out, err := CheckChatInput(in)
		if err != nil {
			t.Errorf("good %d: %v", i, err)
			continue
		}
		if out["kind"] == nil {
			t.Errorf("good %d: no kind", i)
		}
	}
	bad := map[string]any{
		"not an object":    "ciao",
		"no message":       map[string]any{"message": "  "},
		"unknown kind":     map[string]any{"kind": "vote", "message": "x"},
		"kind not a text":  map[string]any{"kind": 3, "message": "x"},
		"rating too high":  map[string]any{"kind": "rating", "turn": "t", "rating": 11},
		"rating not whole": map[string]any{"kind": "rating", "turn": "t", "rating": 2.5},
		"rating, no turn":  map[string]any{"kind": "rating", "rating": 1},
		"history role":     map[string]any{"message": "x", "history": []any{map[string]any{"role": "system", "content": "a"}}},
		"history content":  map[string]any{"message": "x", "history": []any{map[string]any{"role": "user"}}},
		"scopes not texts": map[string]any{"message": "x", "scopes": []any{1}},
		"session too long": map[string]any{"message": "x", "session": strings.Repeat("s", 300)},
		"user not a text":  map[string]any{"message": "x", "user": 5},
	}
	for name, in := range bad {
		if _, err := CheckChatInput(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParse_AnswerAndPriority(t *testing.T) {
	s, err := Parse("priority: interactive\nanswer: b\nnodes: [{id: a, use: starlark}, {id: b, use: starlark}]", nil)
	if err != nil || s.Answer != "b" || !s.Interactive() {
		t.Fatalf("%+v %v", s, err)
	}
	if s, err := Parse("trigger: {on: chat}\nnodes: [{id: a, use: starlark}]", nil); err != nil || !s.Interactive() {
		t.Errorf("chat: %+v %v", s, err)
	}
	if s, err := Parse("priority: normal\nnodes: [{id: a, use: starlark}]", nil); err != nil || s.Interactive() || s.Priority != "" {
		t.Errorf("normal: %+v %v", s, err)
	}
	for _, src := range []string{
		"answer: z\nnodes: [{id: a, use: starlark}]",
		"priority: urgent\nnodes: [{id: a, use: starlark}]",
	} {
		if _, err := Parse(src, nil); err == nil {
			t.Errorf("%q: accepted", src)
		}
	}
}

func TestNoteWrite_AppendAndMerge(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/conv.md"] = pipelineNote(`
trigger: {on: manual}
nodes:
  - id: turn
    use: note.write
    when: "{{ trigger.append }}"
    with:
      path: chat/s1
      mode: append
      frontmatter: {type: chat-conversation, turns: "{{ trigger.n }}", ratings: {}}
      body: "## Turno {{ trigger.n }}\n\n{{ trigger.text }}"
  - id: rate
    use: note.write
    after: []
    when: "{{ trigger.rate }}"
    with:
      path: chat/s1
      frontmatter_merge: true
      frontmatter: "{{ trigger.fm }}"
`)
	run := func(in map[string]any) {
		t.Helper()
		r, _, err := e.Start(StartRequest{Path: "p/conv.md", Trigger: TriggerManual, Input: in})
		if err != nil {
			t.Fatal(err)
		}
		if got := waitStatus(t, e, r.ID, jobs.Done); got.Status != jobs.Done {
			t.Fatal(got.Error)
		}
	}
	run(map[string]any{"append": true, "rate": false, "n": 1, "text": "Domanda e risposta 1"})
	run(map[string]any{"append": true, "rate": false, "n": 2, "text": "Domanda e risposta 2"})
	run(map[string]any{"append": false, "rate": true, "fm": map[string]any{"ratings": map[string]any{"t1": 7}}})
	run(map[string]any{"append": false, "rate": true, "fm": map[string]any{"ratings": map[string]any{"t2": -3}, "type": nil}})
	got := host.note("p/chat/s1.md")
	for _, want := range []string{"turns: 2", "t1: 7", "t2: -3", "## Turno 1\n\nDomanda e risposta 1\n\n## Turno 2\n\nDomanda e risposta 2\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the conversation lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "chat-conversation") {
		t.Errorf("a key set to null stayed:\n%s", got)
	}
	if strings.Count(got, "pipeline_run:") != 1 {
		t.Errorf("the provenance twice:\n%s", got)
	}
}

func TestEngine_AChildOfAnInteractiveRunIsInteractive(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/child.md"] = pipelineNote("nodes: [{id: a, use: starlark, with: {code: 'output = 1'}}]")
	host.notes["p/bot.md"] = pipelineNote(`
trigger: {on: chat}
nodes:
  - {id: sub, use: pipeline.run, with: {pipeline: child.md, wait: false}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/bot.md", Trigger: TriggerChat, Input: map[string]any{"message": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "sub").Output, &out)
	id, _ := out["run"].(string)
	child, err := e.Runner().Store().Run(id)
	if err != nil || child.Priority != jobs.PriorityInteractive {
		t.Errorf("the child %+v %v", child, err)
	}
}
