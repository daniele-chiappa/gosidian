package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The questions are frozen: questions.jsonl must be what the generator
// writes. After a deliberate change, rewrite it with
// go run ./bench/backlog -questions bench/backlog/questions.jsonl.
func TestQuestionsFile(t *testing.T) {
	qs, err := questions(generate())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("questions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if got := marshalQuestions(qs); !bytes.Equal(got, want) {
		t.Fatal("questions.jsonl differs from the generator's output")
	}
}

// Every question's own answer passes its patterns.
func TestAnswersPass(t *testing.T) {
	qs, err := questions(generate())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		for _, p := range q.Accept {
			if !regexp.MustCompile("(?i)" + p).MatchString(q.Answer) {
				t.Errorf("%s: %q does not accept its answer %q", q.ID, p, q.Answer)
			}
		}
		for _, p := range q.Reject {
			if regexp.MustCompile("(?i)" + p).MatchString(q.Answer) {
				t.Errorf("%s: %q rejects its answer %q", q.ID, p, q.Answer)
			}
		}
	}
}

func TestPatterns(t *testing.T) {
	for _, c := range []struct {
		pat, text string
		want      bool
	}{
		{idPat("IMP-066"), "IMP-066 and IMP-116", true},
		{idPat("IMP-066"), "IMP-66", true},
		{idPat("IMP-006"), "IMP-066", false},
		{idPat("IMP-066"), "IMP-0661", false},
		{countPat(23), "There are 23 entries.", true},
		{countPat(23), "due 2026-10-23", false},
		{countPat(23), "IMP-123", false},
		{countPat(5), "five of them", true},
		{datePat(day("2026-08-01")), "due August 1, 2026", true},
		{datePat(day("2026-08-01")), "due 1 Aug 2026", true},
		{datePat(day("2026-08-01")), "due August 10", false},
		{statusPat("in-progress"), "it is in progress", true},
	} {
		if got := regexp.MustCompile("(?i)" + c.pat).MatchString(c.text); got != c.want {
			t.Errorf("%q on %q: got %v, want %v", c.pat, c.text, got, c.want)
		}
	}
}

// The two vaults hold the same entries with the same fields.
func TestShapesAgree(t *testing.T) {
	dir := t.TempDir()
	es := generate()
	for _, form := range []string{"sections", "database"} {
		if err := writeVault(filepath.Join(dir, form), form, es); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	sections := map[string]map[string]string{}
	text := read(filepath.Join(dir, "sections", project, "docs", "improvements.md"))
	for _, sec := range strings.Split(text, "\n## ")[1:] {
		id := strings.Fields(sec)[0]
		fields := map[string]string{}
		for _, m := range regexp.MustCompile(`(?m)^- ([A-Za-z ]+): (.+)$`).FindAllStringSubmatch(sec, -1) {
			fields[strings.ReplaceAll(strings.ToLower(m[1]), " ", "_")] = m[2]
		}
		sections[id] = fields
	}
	if len(sections) != entries {
		t.Fatalf("%d sections, want %d", len(sections), entries)
	}
	field := regexp.MustCompile(`(?m)^(status|priority|area|owner|created|due|closed|blocked_by): (.+)$`)
	link := regexp.MustCompile(`IMP-\d+`)
	for _, e := range es {
		rows := map[string]string{}
		for _, m := range field.FindAllStringSubmatch(read(filepath.Join(dir, "database", project, "docs", "improvements", e.ID+".md")), -1) {
			v := strings.Trim(m[2], `"`)
			if m[1] == "blocked_by" {
				v = link.FindString(v)
			}
			rows[m[1]] = v
		}
		if !reflect.DeepEqual(rows, sections[e.ID]) {
			t.Errorf("%s: database %v, sections %v", e.ID, rows, sections[e.ID])
		}
	}
	for _, name := range []string{"README.md", "hot.md", "log.md"} {
		if read(filepath.Join(dir, "sections", project, name)) != read(filepath.Join(dir, "database", project, name)) {
			t.Errorf("%s differs between the shapes", name)
		}
	}
}
