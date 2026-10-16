package mcp

import (
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
)

func TestChatHost_Folders(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, p := range []string{
		"p/docs/Cucina/Primi/Pasta fresca/Tagliatelle.md",
		"p/docs/Cucina/Primi/Pasta fresca/Tagliatelle_img/foto.md",
		"p/docs/Cucina/Dolci/Crostata.md",
		"p/docs/Indice.md",
		"p/docs/Indice_img/logo.md",
		"p/docs/Attrezzi_img/x.md", // no note Attrezzi.md beside it: a folder like the others
		"p/other/Altro.md",
	} {
		if err := s.index.Upsert(index.NoteDoc{Path: p, Title: p, Body: "x", Size: 1}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := chatHost{s}.Folders("p/docs")
	if err != nil {
		t.Fatal(err)
	}
	want := "p/docs/Attrezzi_img|p/docs/Cucina/Dolci|p/docs/Cucina/Primi/Pasta fresca"
	if strings.Join(got, "|") != want {
		t.Errorf("folders: %v, want %s", got, want)
	}
}
