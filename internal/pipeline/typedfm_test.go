package pipeline

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/jobs"
)

// typedNote has a frontmatter of every shape a pipeline reads (IMP-198).
const typedNote = `---
title: 2026
draft: true
count: 3
ratio: 0.5
code: 007
created: 2026-10-17
quoted: "true"
tags: [doc, "#rev"]
list: [1, two, false]
revisioni:
  V1: 1792237963
  V2: 1792241563
nested:
  deep: {ok: true, n: [1, 2.5]}
related: [[p/other]]
links:
  - [[p/a]]
  - "[[p/b]]"
empty:
---

Text.
`

// typedWant is typedNote's frontmatter as a step's output carries it, after
// JSON: numbers as float64.
var typedWant = map[string]any{
	"title":     "2026",
	"draft":     true,
	"count":     3.0,
	"ratio":     0.5,
	"code":      "007",
	"created":   "2026-10-17",
	"quoted":    "true",
	"tags":      []any{"doc", "rev"},
	"list":      []any{1.0, "two", false},
	"revisioni": map[string]any{"V1": 1792237963.0, "V2": 1792241563.0},
	"nested":    map[string]any{"deep": map[string]any{"ok": true, "n": []any{1.0, 2.5}}},
	"related":   "[[p/other]]",
	"links":     []any{"[[p/a]]", "[[p/b]]"},
}

// note.read gives the frontmatter with its types: bools, numbers, lists,
// maps nested as written; an unquoted wikilink is the link, title and a date
// their text, a key without a value left out (IMP-198). A starlark node
// computes on them as they are.
func TestNoteRead_FrontmatterWithItsTypes(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/docs/rev.md"] = typedNote
	host.notes["p/read.md"] = pipelineNote(`
nodes:
  - {id: read, use: note.read, with: {path: docs/rev.md}}
  - id: calc
    use: starlark
    with:
      input: "{{ read.frontmatter }}"
      code: |
        r = input["revisioni"]
        output = {"next": int(r["V2"]) + 3600, "draft": input["draft"] == True, "count": input["count"] * 2,
                  "ok": input["nested"]["deep"]["ok"], "type": type(input["count"])}
`)
	run, _, err := e.Start(StartRequest{Path: "p/read.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	got := outputMap(t, stepOf(t, e, run.ID, "read"))
	if fm, _ := got["frontmatter"].(map[string]any); !reflect.DeepEqual(fm, typedWant) {
		t.Errorf("frontmatter:\n got %#v\nwant %#v", fm, typedWant)
	}
	want := map[string]any{"next": 1792245163.0, "draft": true, "count": 6.0, "ok": true, "type": "int"}
	if calc := outputMap(t, stepOf(t, e, run.ID, "calc")); !reflect.DeepEqual(calc, want) {
		t.Errorf("starlark on it: %v, want %v", calc, want)
	}
}

// note.list's fields come with their types too; a field the frontmatter
// does not hold, and tags, stay as the index gives them.
func TestNoteList_FieldsWithTheirTypes(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/docs/rev.md"] = typedNote
	host.notes["p/docs/plain.md"] = "---\ntitle: Plain\n---\n\nText.\n"
	host.notes["p/list.md"] = pipelineNote(`
nodes:
  - {id: list, use: note.list, with: {folder: docs, fields: [draft, count, revisioni, related, tags, missing]}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/list.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	notes := outputMap(t, stepOf(t, e, run.ID, "list"))["notes"].([]any)
	if len(notes) != 2 {
		t.Fatalf("notes %v", notes)
	}
	if f := notes[0].(map[string]any)["fields"]; f != nil {
		t.Errorf("plain.md has fields %v", f)
	}
	want := map[string]any{"draft": true, "count": 3.0, "revisioni": typedWant["revisioni"], "related": "[[p/other]]", "tags": []any{"doc", "rev"}}
	if f := notes[1].(map[string]any)["fields"]; !reflect.DeepEqual(f, want) {
		t.Errorf("fields:\n got %#v\nwant %#v", f, want)
	}
}

// note.write writes a whole number as one, however it came (a float64 from
// JSON): an epoch second stays 1792237963, not 1.792237963e+09, in a map
// and in a list too, and in a merge (IMP-198, the revisions of IMP-201).
func TestNoteWrite_WholeNumbersAsIntegers(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/docs/merge.md"] = "---\ntitle: Merge\nrevisioni:\n  V1: 1792237963\n---\n\nKept.\n"
	host.notes["p/write.md"] = pipelineNote(`
trigger: {on: external}
nodes:
  - id: whole
    use: note.write
    with:
      path: docs/whole
      body: x
      frontmatter: {epoch: "{{ trigger.epoch }}", ratio: "{{ trigger.ratio }}", big: "{{ trigger.big }}", revisioni: "{{ trigger.revisioni }}", list: "{{ trigger.list }}"}
  - id: merge
    use: note.write
    with: {path: docs/merge, frontmatter_merge: true, frontmatter: {revisioni: {V2: "{{ trigger.epoch }}"}}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/write.md", Trigger: TriggerExternal, Actor: "alice", Input: map[string]any{
		"epoch": 1792237963, "ratio": 0.25, "big": 1e20,
		"revisioni": map[string]any{"V1": 1792237963},
		"list":      []any{1792237963, 2.5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	whole := host.note("p/docs/whole.md")
	for _, want := range []string{"epoch: 1792237963\n", "ratio: 0.25\n", "big: 1e+20\n", "    V1: 1792237963\n", "    - 1792237963\n    - 2.5\n"} {
		if !strings.Contains(whole, want) {
			t.Errorf("the note lacks %q:\n%s", want, whole)
		}
	}
	merged := host.note("p/docs/merge.md")
	for _, want := range []string{"    V1: 1792237963\n", "    V2: 1792237963\n", "Kept."} {
		if !strings.Contains(merged, want) {
			t.Errorf("the merged note lacks %q:\n%s", want, merged)
		}
	}
	if strings.Contains(whole+merged, "e+09") {
		t.Errorf("a number in exponent form:\n%s\n%s", whole, merged)
	}
}

// A map note.write wrote, note.read gives back as a map: a pipeline that
// adds a revision to it each time keeps the ones before (IMP-201).
func TestNoteWriteRead_MapRoundTrip(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/rev.md"] = pipelineNote(`
trigger: {on: external}
nodes:
  - id: first
    use: note.write
    with: {path: docs/rev, body: x, frontmatter: {revisioni: {V1: "{{ trigger.at }}"}, draft: true}}
  - {id: read, use: note.read, after: [first], with: {path: docs/rev.md}}
  - id: add
    use: starlark
    with:
      input: "{{ read.frontmatter }}"
      code: |
        r = dict(input["revisioni"])
        r["V2"] = int(r["V1"]) + 3600
        output = {"revisioni": r}
  - id: second
    use: note.write
    with: {path: docs/rev, body: x, frontmatter: {revisioni: "{{ add.revisioni }}", draft: "{{ read.frontmatter.draft }}"}}
  - {id: again, use: note.read, after: [second], with: {path: docs/rev.md}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/rev.md", Trigger: TriggerExternal, Actor: "alice", Input: map[string]any{"at": 1792237963}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	fm, _ := outputMap(t, stepOf(t, e, run.ID, "again"))["frontmatter"].(map[string]any)
	want := map[string]any{"V1": 1792237963.0, "V2": 1792241563.0}
	if !reflect.DeepEqual(fm["revisioni"], want) || fm["draft"] != true {
		t.Errorf("read back %#v", fm)
	}
	if note := host.note("p/docs/rev.md"); !strings.Contains(note, "draft: true\n") || !strings.Contains(note, "    V2: 1792241563\n") {
		t.Errorf("the note:\n%s", note)
	}
}

func TestWholeNumbers(t *testing.T) {
	in := map[string]any{"a": 1792237963.0, "b": 0.5, "c": -3.0, "d": 1e20, "e": []any{2.0, "x"}, "f": map[string]any{"g": 4.0}}
	want := map[string]any{"a": int64(1792237963), "b": 0.5, "c": int64(-3), "d": 1e20, "e": []any{int64(2), "x"}, "f": map[string]any{"g": int64(4)}}
	if got := wholeNumbers(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
	if in["a"] != 1792237963.0 || in["e"].([]any)[0] != 2.0 {
		t.Errorf("the input changed: %#v", in)
	}
}
