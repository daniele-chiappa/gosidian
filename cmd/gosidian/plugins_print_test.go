package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/plugins"
)

// plugin show names a manifest's apps, with the address each frames, for
// the owner to read before accepting it (IMP-226).
func TestPrintManifest_Apps(t *testing.T) {
	m, err := plugins.ParseManifest([]byte(`{"name":"layout","version":"0.1.0","gosidian":1,
		"apps":[{"name":"layout","title":"Impaginazione","url":"https://layout.example.com/app/","alt_urls":["http://127.0.0.1:8099/app/"],"note_type":"layout","bridge":"layout/1",
		"engine":"designcraft v0.17.0","description":"Opens and saves a layout note"}],"permissions":{"files":false,"network":[]}}`), "layout")
	if err != nil {
		t.Fatal(err)
	}
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	printManifest(m)
	os.Stdout = stdout
	w.Close()
	out, _ := io.ReadAll(r)
	for _, want := range []string{"Apps", "layout/layout", "notes type: layout", "bridge layout/1", "https://layout.example.com/app/", "http://127.0.0.1:8099/app/", "designcraft v0.17.0", "Opens and saves a layout note"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("plugin show lacks %q:\n%s", want, out)
		}
	}
}
