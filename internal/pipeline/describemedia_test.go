package pipeline

import (
	"encoding/json"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// visionServer is a model that answers each image with a label and a
// description in JSON, but as answer says for a prompt it matches; it keeps
// the prompts it was given.
type visionServer struct {
	mu      sync.Mutex
	prompts []string
	answer  func(prompt string) (status int, content string)
}

func (v *visionServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		prompt, _ := req.Messages[len(req.Messages)-1].Content[0]["text"].(string)
		v.mu.Lock()
		v.prompts = append(v.prompts, prompt)
		v.mu.Unlock()
		status, content := http.StatusOK, `{"label": "A screen", "description": "It shows the window."}`
		v.mu.Lock()
		answer := v.answer
		v.mu.Unlock()
		if answer != nil {
			status, content = answer(prompt)
		}
		w.WriteHeader(status)
		c, _ := json.Marshal(content)
		_, _ = io.WriteString(w, `{"model":"vis-1","choices":[{"message":{"content":`+string(c)+`}}],"usage":{"prompt_tokens":90,"completion_tokens":10,"total_tokens":100}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (v *visionServer) calls() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.prompts...)
}

const describePipeline = `
trigger: {on: manual}
nodes:
  - id: import
    use: in.import
    with: {file: "{{ trigger.file }}", base: wiki, dest: documents/wiki}
  - id: describe
    use: llm.describe_media
    with: {note: "{{ import.path }}", concurrency: 2, prompt_version: 3, redo: "{{ trigger.redo }}", inline_max_side: "{{ trigger.inline_max }}", images: "{{ trigger.images }}"}
`

func newDescribeFixture(t *testing.T, v *visionServer, maxTokens int) (*Engine, *fakeHost) {
	t.Helper()
	srv := v.start(t)
	old := mediaRetryWait
	mediaRetryWait = time.Millisecond
	t.Cleanup(func() { mediaRetryWait = old })
	e, host := newEngine(t, Config{Providers: []Provider{{Name: "default", BaseURL: srv.URL + "/v1", Model: "vis-1"}}, MaxLLMTokens: maxTokens})
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(describePipeline)
	host.att["p/IN/wiki/A/B/Doc one.md"] = []byte(wikiDoc)
	host.att["p/IN/wiki/A/B/Doc one_img/001.png"] = pngOf(t, 40, 30, color.White)
	host.att["p/IN/wiki/A/B/Doc one_img/002.png"] = pngOf(t, 8, 8, color.Black)
	return e, host
}

func describeRun(t *testing.T, e *Engine, input map[string]any, want jobs.Status) (jobs.Run, map[string]any) {
	t.Helper()
	input["file"] = "p/IN/wiki/A/B/Doc one.md"
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/wiki-md.md", Trigger: TriggerManual, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	run = waitStatus(t, e, run.ID, want)
	var out map[string]any
	_ = json.Unmarshal(stepOf(t, e, run.ID, "describe").Output, &out)
	return run, out
}

const (
	img1Note = "p/documents/wiki/A/B/Doc one_img/001.md"
	img2Note = "p/documents/wiki/A/B/Doc one_img/002.md"
)

// Each image note gets its description, asked with the text around the
// image; a run again leaves those described, but with redo.
func TestDescribeMedia_WritesDescriptions(t *testing.T) {
	v := &visionServer{}
	e, host := newDescribeFixture(t, v, 0)
	_, out := describeRun(t, e, map[string]any{"redo": false}, jobs.Done)
	if out["described"] != 2.0 || out["failed"] != 0.0 || out["skipped"] != 0.0 {
		t.Fatalf("output %v", out)
	}
	if u := out["usage"].(map[string]any); u["total_tokens"] != 200.0 {
		t.Errorf("usage %v", u)
	}
	for _, p := range []string{img1Note, img2Note} {
		mustContain(t, p, host.note(p), "title: A screen", "label: A screen", "model: vis-1", "prompt_version: 3", "described: ", "\n\nIt shows the window.\n")
	}

	var block, inline string
	for _, p := range v.calls() {
		if strings.Contains(p, "small image") {
			inline = p
		} else {
			block = p
		}
	}
	mustContain(t, "the prompt of the image", block, `"Doc one", section: Setup > Window.`,
		"The window opens with the defaults set.\n\n[image]\n\nThe button saves it.")
	mustContain(t, "the prompt of the icon", inline, "The button [image] saves it.")

	_, out = describeRun(t, e, map[string]any{"redo": false}, jobs.Done)
	if out["described"] != 0.0 || out["skipped"] != 2.0 || len(v.calls()) != 2 {
		t.Fatalf("again: output %v, %d calls", out, len(v.calls()))
	}
	_, out = describeRun(t, e, map[string]any{"redo": true}, jobs.Done)
	if out["described"] != 2.0 || len(v.calls()) != 4 {
		t.Fatalf("redo: output %v, %d calls", out, len(v.calls()))
	}
}

// An image inside a sentence gets the inline prompt only up to
// inline_max_side: a screenshot put in a line gets the full one (BUG-124).
func TestDescribeMedia_LargeInlineImageGetsTheFullPrompt(t *testing.T) {
	v := &visionServer{}
	e, host := newDescribeFixture(t, v, 0)
	host.att["p/IN/wiki/A/B/Doc one_img/002.png"] = pngOf(t, 300, 200, color.Black)
	inlineCalls := func() int {
		n := 0
		for _, p := range v.calls() {
			if strings.Contains(p, "small image") {
				n++
			}
		}
		return n
	}
	if _, out := describeRun(t, e, map[string]any{"redo": false}, jobs.Done); out["described"] != 2.0 || inlineCalls() != 0 {
		t.Fatalf("output %v, %d inline prompts", out, inlineCalls())
	}
	if _, out := describeRun(t, e, map[string]any{"redo": true, "inline_max": 0}, jobs.Done); out["described"] != 2.0 || inlineCalls() != 1 {
		t.Fatalf("with 0: output %v, %d inline prompts", out, inlineCalls())
	}
}

// images splits the work in two passes, each with its own model: the icons
// (in a sentence, up to inline_max_side) and the others (IMP-179).
func TestDescribeMedia_PassesForIconsAndOthers(t *testing.T) {
	v := &visionServer{}
	e, host := newDescribeFixture(t, v, 0)
	_, out := describeRun(t, e, map[string]any{"images": "icons"}, jobs.Done)
	if out["described"] != 1.0 || out["excluded"] != 1.0 || len(v.calls()) != 1 || !strings.Contains(v.calls()[0], "small image") {
		t.Fatalf("icons: output %v, calls %d", out, len(v.calls()))
	}
	if strings.Contains(host.note(img1Note), "It shows the window.") || !strings.Contains(host.note(img2Note), "It shows the window.") {
		t.Fatal("the icons pass described the screenshot, or not the icon")
	}
	_, out = describeRun(t, e, map[string]any{"images": "others"}, jobs.Done)
	if out["described"] != 1.0 || out["skipped"] != 1.0 || out["excluded"] != 0.0 || strings.Contains(v.calls()[1], "small image") {
		t.Fatalf("others: output %v", out)
	}
	run, _ := describeRun(t, e, map[string]any{"images": "screens"}, jobs.Failed)
	if !strings.Contains(run.Error, "all, icons or others") {
		t.Errorf("error %q", run.Error)
	}
}

// An image over max_pixels goes to the model scaled down, as a PNG; the
// attachment stays as it is.
func TestDescribeMedia_ScalesLargeImages(t *testing.T) {
	v := &visionServer{}
	e, host := newDescribeFixture(t, v, 0)
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(strings.Replace(describePipeline, "prompt_version: 3,", "prompt_version: 3, max_pixels: 1000,", 1))
	_, out := describeRun(t, e, map[string]any{"redo": false}, jobs.Done)
	if out["described"] != 2.0 || out["downscaled"] != 1.0 {
		t.Fatalf("output %v", out)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if b := host.att["p/IN/wiki/A/B/Doc one_img/001.png"]; len(b) == 0 {
		t.Fatal("the original is gone")
	}
	for p, c := range host.notes {
		if strings.Contains(p, "_img/001") && !strings.Contains(c, "width: 40") {
			t.Errorf("the image note lost its size:\n%s", c)
		}
	}
}

// An image the model fails on gets describe_error after its tries; an
// answer that is no JSON is the description; the run goes on unless most
// images fail.
func TestDescribeMedia_FailuresAndText(t *testing.T) {
	v := &visionServer{answer: func(p string) (int, string) {
		if strings.Contains(p, "small image") {
			return http.StatusInternalServerError, "busy"
		}
		return http.StatusOK, "Just a window."
	}}
	e, host := newDescribeFixture(t, v, 0)
	_, out := describeRun(t, e, map[string]any{"redo": false}, jobs.Done)
	if out["described"] != 1.0 || out["failed"] != 1.0 {
		t.Fatalf("output %v", out)
	}
	if errs := strList(out["errors"]); len(errs) != 1 || !strings.Contains(errs[0], img2Note) {
		t.Errorf("errors %v", errs)
	}
	if n := len(v.calls()); n != 1+mediaAttempts {
		t.Errorf("%d calls, want one and %d tries", n, mediaAttempts)
	}
	c := host.note(img1Note)
	mustContain(t, "the image described in text", c, "\n\nJust a window.\n")
	if strings.Contains(c, "label:") {
		t.Errorf("a label from no JSON:\n%s", c)
	}
	mustContain(t, "the image that failed", host.note(img2Note), "describe_error: ")

	// Both fail: the step fails.
	v.mu.Lock()
	v.answer = func(string) (int, string) { return http.StatusInternalServerError, "busy" }
	v.mu.Unlock()
	run, _ := describeRun(t, e, map[string]any{"redo": true}, jobs.Failed)
	if !strings.Contains(run.Error, "2 of 2 images failed") {
		t.Errorf("run error %q", run.Error)
	}
}

// The run's cap of LLM tokens stops the describing; the images described
// before it keep their description.
func TestDescribeMedia_TokenCap(t *testing.T) {
	v := &visionServer{}
	e, host := newDescribeFixture(t, v, 650)
	host.notes["p/pipelines/wiki-md.md"] = pipelineNote(strings.Replace(describePipeline, "concurrency: 2", "concurrency: 1", 1))
	run, _ := describeRun(t, e, map[string]any{"redo": false}, jobs.Failed)
	if !strings.Contains(run.Error, "stopped after describing 1 images") || !strings.Contains(run.Error, "max_llm_tokens") {
		t.Fatalf("run error %q", run.Error)
	}
	if n := len(v.calls()); n != 1 {
		t.Errorf("%d calls, want 1", n)
	}
	if !strings.Contains(host.note(img1Note), "label: A screen") {
		t.Errorf("the image described first lost its description:\n%s", host.note(img1Note))
	}
}

func TestMediaAnswer(t *testing.T) {
	// The shape of an answer a vision model gave (BUG-125): a Windows path
	// whose last backslash closes the description's quote too soon.
	broken := "{\n  \"label\": \"Cartella REPORTS con foglio di stile XML\",\n  \"description\": \"Finestra di esplorazione file mostra la cartella REPORTS in D:\\\\Data\\\\reports\\\\q3\\\\*\\\\\"  ,  \"contiene file PDF e un foglio di stile XSL.\"\n}"
	for name, tc := range map[string]struct {
		out         map[string]any
		label, desc string
		broken      bool
	}{
		"json":   {map[string]any{"json": map[string]any{"label": "A screen", "description": "It shows."}, "text": "{}"}, "A screen", "It shows.", false},
		"prose":  {map[string]any{"text": "Just a window."}, "", "Just a window.", false},
		"broken": {map[string]any{"text": broken}, "Cartella REPORTS con foglio di stile XML", `Finestra di esplorazione file mostra la cartella REPORTS in D:\Data\reports\q3\*\ contiene file PDF e un foglio di stile XSL.`, true},
		"fenced": {map[string]any{"text": "```json\n{\"label\": \"Menu\", \"description\": \"Il menu\n}```"}, "Menu", "Il menu", true},
		"empty":  {map[string]any{"text": "{\"oops\": 1"}, "", "", true},
	} {
		label, desc, b := mediaAnswer(tc.out)
		if label != tc.label || desc != tc.desc || b != tc.broken {
			t.Errorf("%s: %q %q %v, want %q %q %v", name, label, desc, b, tc.label, tc.desc, tc.broken)
		}
	}
}

// A JSON that does not read is tried again; at the last try, what reads of
// it is kept (BUG-125).
func TestDescribeMedia_BrokenJSON(t *testing.T) {
	var calls atomic.Int32
	v := &visionServer{answer: func(p string) (int, string) {
		if strings.Contains(p, "small image") {
			return http.StatusOK, `{"label": "Icona", "description": "Un'icona."}`
		}
		if calls.Add(1) == 1 {
			return http.StatusOK, `{"label": "Rotta", "description": "metà"  ,  "e metà"`
		}
		return http.StatusOK, `{"label": "A screen", "description": "It shows the window."}`
	}}
	e, host := newDescribeFixture(t, v, 0)
	_, out := describeRun(t, e, map[string]any{"redo": false}, jobs.Done)
	if out["described"] != 2.0 || out["failed"] != 0.0 || calls.Load() != 2 {
		t.Fatalf("output %v, %d calls for the screen", out, calls.Load())
	}
	mustContain(t, "the screen tried again", host.note(img1Note), "label: A screen", "\n\nIt shows the window.\n")

	// Broken every time: the last try keeps what reads.
	v.mu.Lock()
	v.answer = func(string) (int, string) {
		return http.StatusOK, `{"label": "Rotta", "description": "metà"  ,  "e metà"`
	}
	v.mu.Unlock()
	_, out = describeRun(t, e, map[string]any{"redo": true}, jobs.Done)
	if out["described"] != 2.0 || out["failed"] != 0.0 {
		t.Fatalf("broken every time: %v", out)
	}
	mustContain(t, "what reads of a broken answer", host.note(img1Note), "label: Rotta", "\n\nmetà e metà\n")
}
