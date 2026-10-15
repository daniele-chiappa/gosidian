package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gosidian/gosidian/internal/marks"
)

// /marks gives the sets of marks of a note the reader may see (IMP-188),
// each stale when the note changed since it was made.
func TestMarks(t *testing.T) {
	f := newNotesFixture(t)
	content := "# Doc\n\n## A\n\nText.\n"
	f.seedNote(t, "p/doc.md", content)
	sum := sha256.Sum256([]byte(content))
	for _, s := range []marks.Set{
		{Path: "p/doc.md", Name: "cut", Pipeline: "p/pipelines/cut.md", Run: "r1", Hash: hex.EncodeToString(sum[:]), Marks: []marks.Mark{{Section: "A", Block: 1, Color: "red", Label: "x"}}},
		{Path: "p/doc.md", Name: "old", Hash: "an older text", Marks: []marks.Mark{{Section: "A", Color: "grey"}}},
	} {
		if err := f.marks.Put(s); err != nil {
			t.Fatal(err)
		}
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/marks?path=p/doc.md", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	var got struct {
		Sets []struct {
			Set      string       `json:"set"`
			Pipeline string       `json:"pipeline"`
			Stale    bool         `json:"stale"`
			Marks    []marks.Mark `json:"marks"`
		} `json:"sets"`
	}
	if err := json.Unmarshal([]byte(w.body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sets) != 2 || got.Sets[0].Set != "cut" || got.Sets[0].Stale || got.Sets[0].Marks[0].Label != "x" || got.Sets[0].Pipeline != "p/pipelines/cut.md" || !got.Sets[1].Stale {
		t.Errorf("sets %s", w.body)
	}
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/marks?path=p/nope.md", "", nil); w.code != http.StatusOK || w.body == "" {
		t.Errorf("a note without marks: %d %s", w.code, w.body)
	}
}
