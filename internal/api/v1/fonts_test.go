package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fonts/fontstest"
)

const fontsURL = "/api/v1/fonts"
const adminFontsURL = "/api/v1/admin/fonts"

type fontAnswer struct {
	ID        string `json:"id"`
	Family    string `json:"family"`
	Style     string `json:"style"`
	Format    string `json:"format"`
	Embedding string `json:"embedding"`
	Bytes     int64  `json:"bytes"`
	AddedAt   string `json:"added_at"`
}

func TestFonts_TheOwnerAddsAndAnAccountReads(t *testing.T) {
	f := newNotesFixture(t)
	font := string(fontstest.Build(fontstest.TrueType, "Corpo", "Bold", 4))

	w := f.doAuthRecorder(http.MethodPost, adminFontsURL, font, map[string]string{"Content-Type": "font/ttf"})
	if w.code != http.StatusCreated {
		t.Fatalf("add: %d %s", w.code, w.body)
	}
	var added fontAnswer
	if err := json.Unmarshal([]byte(w.body), &added); err != nil {
		t.Fatal(err)
	}
	if added.Family != "Corpo" || added.Style != "Bold" || added.Format != "ttf" || added.Embedding != "preview" || added.Bytes != int64(len(font)) || added.AddedAt == "" {
		t.Fatalf("added %+v", added)
	}
	if w := f.doAuthRecorder(http.MethodPost, adminFontsURL, font, nil); w.code != http.StatusOK {
		t.Fatalf("the same file again: %d %s", w.code, w.body)
	}

	_, member := f.memberUser(t, "reader")
	list := f.req(t, http.MethodGet, fontsURL, "", member)
	if list.code != http.StatusOK || !strings.Contains(list.body, `"family":"Corpo"`) {
		t.Fatalf("list: %d %s", list.code, list.body)
	}
	var page struct {
		Items []fontAnswer `json:"items"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal([]byte(list.body), &page); err != nil || page.Total != 1 || page.Items[0].ID != added.ID {
		t.Fatalf("list: %v %s", err, list.body)
	}
	got := f.req(t, http.MethodGet, fontsURL+"/"+added.ID, "", member)
	if got.code != http.StatusOK || got.body != font {
		t.Fatalf("the file: %d", got.code)
	}
	h := got.headers
	if h.Get("Content-Type") != "font/ttf" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Cache-Control"), "immutable") || !strings.Contains(h.Get("Cache-Control"), "private") {
		t.Fatalf("headers: %v", h)
	}
}

func TestFonts_OnlyTheOwnerChangesTheCatalog(t *testing.T) {
	f := newNotesFixture(t)
	font := string(fontstest.Build(fontstest.TrueType, "Corpo", "Regular", 0))
	_, member := f.memberUser(t, "reader")
	if w := f.req(t, http.MethodPost, adminFontsURL, font, member); w.code != http.StatusForbidden {
		t.Fatalf("a member adds: %d", w.code)
	}
	w := f.doAuthRecorder(http.MethodPost, adminFontsURL, font, nil)
	var added fontAnswer
	_ = json.Unmarshal([]byte(w.body), &added)
	if w := f.req(t, http.MethodDelete, adminFontsURL+"/"+added.ID, "", member); w.code != http.StatusForbidden {
		t.Fatalf("a member removes: %d", w.code)
	}
	if w := f.request(http.MethodGet, fontsURL, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("nobody lists: %d", w.Code)
	}
}

func TestFonts_Remove(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodPost, adminFontsURL, string(fontstest.Build(fontstest.TrueType, "Corpo", "Regular", 0)), nil)
	var added fontAnswer
	_ = json.Unmarshal([]byte(w.body), &added)
	if w := f.doAuthRecorder(http.MethodDelete, adminFontsURL+"/"+added.ID, "", nil); w.code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodGet, fontsURL+"/"+added.ID, "", nil); w.code != http.StatusNotFound {
		t.Fatalf("after: %d", w.code)
	}
	if w := f.doAuthRecorder(http.MethodDelete, adminFontsURL+"/"+added.ID, "", nil); w.code != http.StatusNotFound {
		t.Fatalf("twice: %d", w.code)
	}
	if w := f.doAuthRecorder(http.MethodGet, fontsURL+"/..%2Fauth.json", "", nil); w.code != http.StatusNotFound {
		t.Fatalf("a path for an id: %d", w.code)
	}
}

func TestFonts_RefusesWhatIsNoFontAndWhatIsTooLarge(t *testing.T) {
	f := newNotesFixture(t)
	w := f.doAuthRecorder(http.MethodPost, adminFontsURL, "wOFF, a web font and no file of ours", nil)
	if w.code != http.StatusUnprocessableEntity || !strings.Contains(w.body, ".ttf") {
		t.Fatalf("no font: %d %s", w.code, w.body)
	}
	f.router.web.LayoutMaxFontBytes = 20
	w = f.doAuthRecorder(http.MethodPost, adminFontsURL, string(fontstest.Build(fontstest.TrueType, "Corpo", "Regular", 0)), nil)
	if w.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d %s", w.code, w.body)
	}
}

func TestFonts_MethodsOfEachAddress(t *testing.T) {
	f := newNotesFixture(t)
	for _, c := range [][2]string{{http.MethodPost, fontsURL}, {http.MethodDelete, fontsURL + "/0123456789abcdef"}, {http.MethodGet, adminFontsURL}, {http.MethodGet, adminFontsURL + "/0123456789abcdef"}} {
		if w := f.doAuthRecorder(c[0], c[1], "", nil); w.code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", c[0], c[1], w.code)
		}
	}
}
