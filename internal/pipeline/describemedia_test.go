package pipeline

import (
	"encoding/json"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
    with: {note: "{{ import.path }}", concurrency: 2, prompt_version: 3, redo: "{{ trigger.redo }}"}
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
