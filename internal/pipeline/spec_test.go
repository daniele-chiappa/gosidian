package pipeline

import (
	"strings"
	"testing"
	"time"
)

type knownTypes map[string]bool

func (k knownTypes) Known(use string) bool { return k[use] }

var testTypes = knownTypes{"note.read": true, "note.write": true, "llm.describe": true, "starlark": true, "wait.call": true}

const arrival = `
trigger:
  on: ingest
  kinds: [PDF, .docx]
  folder: inbox/
nodes:
  - id: read
    use: note.read
    with: { path: "{{ trigger.note }}" }
  - id: describe
    use: llm.describe
    each: "{{ read.images }}"
    with: { image: "{{ item.path }}", prompt: "Image {{ index }} of {{ trigger.name }}" }
    retry: { attempts: 3, backoff: 1s }
    timeout: 2m
  - id: write
    use: note.write
    after: [read, describe]
    with: { path: "{{ trigger.folder }}/out.md", body: "{{ read.body }}", images: "{{ describe }}" }
`

func TestParse_ArrivalPipeline(t *testing.T) {
	s, err := Parse(arrival, testTypes)
	if err != nil {
		t.Fatal(err)
	}
	if s.Trigger.On != TriggerIngest || strings.Join(s.Trigger.Kinds, ",") != "pdf,docx" || s.Trigger.Folder != "inbox" {
		t.Fatalf("trigger %+v", s.Trigger)
	}
	if len(s.Nodes) != 3 {
		t.Fatalf("%d nodes", len(s.Nodes))
	}
	d, _ := s.Node("describe")
	if strings.Join(d.After, ",") != "read" || d.Retry.Attempts != 3 || d.Retry.Backoff != time.Second || d.Timeout != 2*time.Minute {
		t.Fatalf("describe %+v", d)
	}
	r, _ := s.Node("read")
	if len(r.After) != 0 || r.Timeout != DefaultTimeout || r.OnError != "stop" || r.Retry.Attempts != 1 {
		t.Fatalf("read %+v", r)
	}
	if !s.Trigger.Matches("pdf", "inbox/2026") || s.Trigger.Matches("pdf", "inboxes") || s.Trigger.Matches("png", "inbox") {
		t.Fatal("Matches")
	}
}

func TestParse_NodesAsAMapKeepTheirOrder(t *testing.T) {
	s, err := Parse("nodes:\n  b: {use: note.read}\n  a: {use: note.write, with: {body: '{{ b.body }}'}}\n", testTypes)
	if err != nil {
		t.Fatal(err)
	}
	if s.Nodes[0].ID != "b" || s.Nodes[1].ID != "a" || strings.Join(s.Nodes[1].After, ",") != "b" {
		t.Fatalf("nodes %+v", s.Nodes)
	}
	if s.Trigger.On != TriggerManual {
		t.Fatalf("default trigger %q", s.Trigger.On)
	}
}

func TestParse_Refusals(t *testing.T) {
	cases := map[string]string{
		"not yaml":            "nodes: [",
		"no nodes":            "trigger: {on: manual}",
		"bad trigger":         "trigger: {on: webhook}\nnodes: [{id: a, use: note.read}]",
		"ingest without kind": "trigger: {on: ingest}\nnodes: [{id: a, use: note.read}]",
		"bad schedule":        "trigger: {on: schedule, every: someday}\nnodes: [{id: a, use: note.read}]",
		"bad id":              "nodes: [{id: A-1, use: note.read}]",
		"reserved id":         "nodes: [{id: trigger, use: note.read}]",
		"unknown type":        "nodes: [{id: a, use: shell.run}]",
		"twice":               "nodes: [{id: a, use: note.read}, {id: a, use: note.read}]",
		"unknown after":       "nodes: [{id: a, use: note.read, after: [z]}]",
		"cycle":               "nodes: [{id: a, use: note.read, after: [b]}, {id: b, use: note.read, after: [a]}]",
		"ref not before":      "nodes: [{id: a, use: note.read, after: []}, {id: b, use: note.read, after: [], with: {x: '{{ a.body }}'}}]",
		"ref unknown node":    "nodes: [{id: a, use: note.read, with: {x: '{{ z.body }}'}}]",
		"item without each":   "nodes: [{id: a, use: note.read, with: {x: '{{ item.path }}'}}]",
		"bad expression":      "nodes: [{id: a, use: note.read, with: {x: '{{ a | upper }}'}}]",
		"when not one ref":    "nodes: [{id: a, use: note.read, when: 'yes'}]",
		"bad on_error":        "nodes: [{id: a, use: note.read, on_error: ignore}]",
		"bad timeout":         "nodes: [{id: a, use: note.read, timeout: soon}]",
		"too many retries":    "nodes: [{id: a, use: note.read, retry: {attempts: 99}}]",
	}
	for name, src := range cases {
		if _, err := Parse(src, testTypes); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFindBlock(t *testing.T) {
	body := "# Arrivo\n\nText.\n\n```pipeline\nnodes: [{id: a, use: note.read}]\n```\n\n```yaml\nnot: this\n```\n"
	src, err := FindBlock([]byte(body))
	if err != nil || !strings.Contains(src, "note.read") {
		t.Fatalf("%q %v", src, err)
	}
	if src, err := FindBlock([]byte("no block")); src != "" || err != nil {
		t.Fatal("a note without a block")
	}
	two := "```pipeline\na: 1\n```\n```pipeline\nb: 2\n```\n"
	if _, err := FindBlock([]byte(two)); err == nil {
		t.Fatal("two blocks accepted")
	}
}

func TestScope_Eval(t *testing.T) {
	sc := scope{
		"trigger": map[string]any{"name": "doc.pdf", "n": float64(3)},
		"read":    map[string]any{"images": []any{map[string]any{"path": "p/a.png"}}, "ok": true},
	}
	v, err := sc.eval(map[string]any{
		"whole":  "{{ read.images }}",
		"text":   "File {{ trigger.name }} has {{ trigger.n }} pages",
		"index":  "{{ read.images.0.path }}",
		"absent": "{{ read.nothing.deeper }}",
		"list":   []any{"{{ read.ok }}", "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if imgs, ok := m["whole"].([]any); !ok || len(imgs) != 1 {
		t.Fatalf("whole %#v", m["whole"])
	}
	if m["text"] != "File doc.pdf has 3 pages" || m["index"] != "p/a.png" || m["absent"] != nil {
		t.Fatalf("%#v", m)
	}
	if l := m["list"].([]any); l[0] != true {
		t.Fatalf("list %#v", l)
	}
	if _, err := sc.eval("{{ nobody.x }}"); err == nil {
		t.Fatal("an unknown root resolved")
	}
	if !truthy([]any{1}) || truthy("") || truthy(float64(0)) || truthy(nil) || !truthy("x") {
		t.Fatal("truthy")
	}
}
