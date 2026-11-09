package v1

import (
	"encoding/json"
	"net/http"
	"testing"
)

// GET /api/v1/attachments?project=<p> lists a project's attachments to who
// may read it, the images only with kind=image: the picture a page layout
// places is chosen among them (IMP-226).
func TestAttachmentsList(t *testing.T) {
	f := newNotesFixture(t)
	f.writeRaw(t, "Alpha/attachments/a.png", pngOne)
	f.writeRaw(t, "Alpha/attachments/b.jpg", jpgTwo)
	f.writeRaw(t, "Alpha/attachments/m.pdf", "%PDF-1.7")
	f.writeRaw(t, "Alpha/docs/not-an-attachment.png", pngOne)
	f.seedNote(t, "Alpha/docs/a.md", "# a\n")
	list := func(w *recorder) []string {
		var got struct {
			Attachments []struct {
				Path  string `json:"path"`
				Size  int64  `json:"size"`
				Image bool   `json:"image"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal([]byte(w.body), &got); err != nil {
			t.Fatalf("%d %s", w.code, w.body)
		}
		var out []string
		for _, a := range got.Attachments {
			if a.Size <= 0 {
				t.Errorf("%s has no size", a.Path)
			}
			out = append(out, a.Path)
		}
		return out
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/attachments?project=Alpha", "", nil)
	if all := list(w); w.code != http.StatusOK || len(all) != 3 {
		t.Errorf("all: %d %v", w.code, all)
	}
	w = f.doAuthRecorder(http.MethodGet, "/api/v1/attachments?project=Alpha&kind=image", "", nil)
	if images := list(w); len(images) != 2 || images[0] == "Alpha/attachments/m.pdf" || images[1] == "Alpha/attachments/m.pdf" {
		t.Errorf("images: %v", images)
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/attachments", "", nil); w.code != http.StatusBadRequest {
		t.Errorf("no project: %d", w.code)
	}
	guest := f.seedTwoProjects(t)
	if w := f.req(t, http.MethodGet, "/api/v1/attachments?project=Alpha", "", guest); w.code != http.StatusNotFound {
		t.Errorf("a project the guest does not see: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/attachments?project=Alpha", "", nil); w.code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.code)
	}
}
