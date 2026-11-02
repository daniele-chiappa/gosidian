package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFindReply_JSONAndEventStream(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","id":3,"result":{"ok":true}}`)
	r, err := findReply(plain, "application/json", 3)
	if err != nil || string(r.Result) != `{"ok":true}` {
		t.Fatalf("plain: %v %s", err, r.Result)
	}
	stream := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" +
		"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"n\":1}}\n\n")
	r, err = findReply(stream, "text/event-stream", 7)
	if err != nil || string(r.Result) != `{"n":1}` {
		t.Fatalf("stream: %v %s", err, r.Result)
	}
	if _, err := findReply(stream, "text/event-stream", 8); err == nil {
		t.Fatal("a reply to another id must not count")
	}
}

func TestToolResult_ReadsWhatTheAgentReads(t *testing.T) {
	var both toolResult
	json.Unmarshal([]byte(`{"content":[{"text":"{\"hits\":[]}"}],"structuredContent":{"hits":[{"path":"a/b.md"}]}}`), &both)
	if got := hitPaths(both); len(got) != 1 || got[0] != "a/b.md" {
		t.Fatalf("structuredContent first: %v", got)
	}
	if both.agentBytes() != len(`{"hits":[{"path":"a/b.md"}]}`) {
		t.Fatalf("agentBytes = %d", both.agentBytes())
	}
	var text toolResult
	json.Unmarshal([]byte(`{"content":[{"text":"{\"hits\":[{\"path\":\"x.md\"},{\"path\":\"y.md\"}]}"}]}`), &text)
	if got := hitPaths(text); len(got) != 2 || got[1] != "y.md" {
		t.Fatalf("text fallback: %v", got)
	}
}

func TestAggregate_AsLevelR(t *testing.T) {
	ranks := map[string]int{"a": 1, "b": 2, "c": 6, "d": 0}
	m := aggregate(ranks, []string{"a", "b", "c", "d"})
	if m.N != 4 || m.Miss != 1 || m.R1 != 0.25 || m.R5 != 0.5 {
		t.Fatalf("%+v", m)
	}
	// MRR: (1 + 1/2 + 1/6 + 0) / 4
	if m.MRR != 0.417 {
		t.Fatalf("MRR = %v", m.MRR)
	}
	if firstGold([]string{"x", "g2", "g1"}, []string{"g1", "g2"}) != 2 {
		t.Fatal("firstGold takes the first hit that is any gold note")
	}
}

func TestSummarize_Percentiles(t *testing.T) {
	var s []time.Duration
	for i := 1; i <= 100; i++ {
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	l := summarize(s, 2)
	if l.N != 100 || l.P50 != 50 || l.P95 != 95 || l.P99 != 99 || l.Mean != 50.5 || l.Errors != 2 {
		t.Fatalf("%+v", l)
	}
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Fatalf("median odd = %v", got)
	}
	if got := median([]int64{4, 1, 3, 2}); got != 2 {
		t.Fatalf("median even = %v", got) // integer mean of 2 and 3
	}
}

func TestGoModDeps(t *testing.T) {
	mod := []byte("module x\n\ngo 1.27.0\n\nrequire (\n\ta v1\n\tb v2 // indirect\n\t// a comment\n)\n\nrequire c v3\nrequire (\n\td v4 // indirect\n)\n")
	d, i := goModDeps(mod)
	if d != 2 || i != 2 {
		t.Fatalf("direct %d indirect %d", d, i)
	}
}

func TestCodeSize_SplitsBaseAndBranches(t *testing.T) {
	cs := &codeSize{Packages: map[string]int{}}
	cs.add("internal/vault/vault.go", []byte("a\nb\n"))
	cs.add("internal/vault/vault_test.go", []byte("a\n"))
	cs.add("internal/pipeline/run.go", []byte("a\nb\nc\n"))
	cs.add("cmd/gosidian/main.go", []byte("a\n"))
	cs.add("web/src/App.vue", []byte("a\nb\n"))
	cs.add("web/src/App.spec.ts", []byte("a\n"))
	cs.add("web/package.json", []byte(`{"dependencies":{"vue":"1"},"devDependencies":{"vite":"1","vitest":"1"}}`))
	if cs.GoLines != 6 || cs.BaseLines != 3 || cs.BranchLines != 3 || cs.GoTestLines != 1 || cs.WebLines != 2 {
		t.Fatalf("%+v", cs)
	}
	if cs.Packages["internal/pipeline"] != 3 || cs.Packages["cmd/gosidian"] != 1 || cs.NPM != 1 || cs.NPMDev != 2 {
		t.Fatalf("%+v", cs)
	}
}

func TestAssetRef_FirstLoadOnly(t *testing.T) {
	page := `<link rel="icon" href="/static/dist/favicon.svg"><script type="module" src="/static/dist/assets/index-a.js"></script>
<link rel="modulepreload" href="/static/dist/assets/ui-b.js"><link rel="stylesheet" href="/static/dist/assets/index-c.css"><a href="/about">`
	var got []string
	for _, m := range assetRef.FindAllStringSubmatch(page, -1) {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != "/static/dist/favicon.svg,/static/dist/assets/index-a.js,/static/dist/assets/ui-b.js,/static/dist/assets/index-c.css" {
		t.Fatalf("%v", got)
	}
}

func TestShortImage(t *testing.T) {
	for in, want := range map[string]string{
		"registry.example.com/org/gosidian:v2.1.0": "gosidian:v2.1.0",
		"gosidian:lab": "gosidian:lab",
	} {
		if got := shortImage(in); got != want {
			t.Fatalf("%s: %s", in, got)
		}
	}
}

func TestPrintTable_OneColumnPerResult(t *testing.T) {
	a := &result{Label: "v1", Runs: 1, Start: startTimes{ColdMS: []float64{300, 500, 400}}, Retrieval: map[string]metrics{"query": {R1: 0.5, R5: 0.75, MRR: 0.6, Miss: 2}},
		Latency: map[string]latency{"memory_search": {N: 10, P50: 1.5, P95: 3}}, Code: &codeSize{BaseLines: 12345}}
	b := &result{Label: "v2", Runs: 1}
	var out bytes.Buffer
	if err := printTable(&out, []*result{a, b}); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"| | v1 | v2 |", "0.50 · 0.75 · 0.60 · 2", "400 ms", "1.50 ms · 3.00 ms", "12,345", "| — |"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
}
