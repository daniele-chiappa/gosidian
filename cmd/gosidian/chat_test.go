package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
)

type changes []string

func (c *changes) Changed(path string) { *c = append(*c, path) }

// The bots' service hears of a note written as of the note, and of a
// folder or a project as of its path with a trailing slash: a restore from
// the trash by the place it brings back, a folder or a project deleted or
// renamed by its old path and its new one (BUG-152). Before, a restore
// passed the trash id and a folder its bare path, and the projects nothing.
func TestWireChat(t *testing.T) {
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got changes
	wireChat(&got, al)
	for _, tc := range []struct {
		name string
		e    audit.Entry
		want []string
	}{
		{"a note written", audit.Entry{Action: audit.ActionUpdate, Path: "p/bots/a.md"}, []string{"p/bots/a.md"}},
		{"a note deleted", audit.Entry{Action: audit.ActionDelete, Path: "p/bots/a.md"}, []string{"p/bots/a.md"}},
		{"a note renamed", audit.Entry{Action: audit.ActionRename, Path: "p/bots/a.md", To: "p/bots/b.md"}, []string{"p/bots/a.md", "p/bots/b.md"}},
		{"a note restored", audit.Entry{Action: audit.ActionCreate, Path: "1793650000000000000__p%2Fbots%2Fa.md", To: "p/bots/a.md"}, []string{"p/bots/a.md"}},
		{"a folder restored", audit.Entry{Action: audit.ActionCreate, Path: "1793650000000000000__p%2Fbots", To: "p/bots/a.md,p/bots/b.md"}, []string{"p/bots/"}},
		{"a project restored", audit.Entry{Action: audit.ActionCreate, Path: "1793650000000000000__p", To: "p/bots/a.md"}, []string{"p/"}},
		{"a folder deleted", audit.Entry{Action: audit.ActionDelete, Path: "p/bots"}, []string{"p/bots/"}},
		{"a folder renamed", audit.Entry{Action: audit.ActionRename, Path: "p/bots", To: "p/robots"}, []string{"p/bots/", "p/robots/"}},
		{"a project deleted", audit.Entry{Action: audit.ActionDeleteProject, Path: "p"}, []string{"p/"}},
		{"a project renamed", audit.Entry{Action: audit.ActionRenameProject, Path: "p", To: "q"}, []string{"p/", "q/"}},
	} {
		got = nil
		if err := al.Write(tc.e); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
