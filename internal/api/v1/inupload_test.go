package v1

import (
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/infolder"
)

// An upload that ended leaves an entry for it and one for each file it
// placed: created, or updated when it replaced one; a zip's folder once.
func TestAuditUpload(t *testing.T) {
	log, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	AuditUpload(log, infolder.Upload{ID: "u1", Project: "p", Dir: "lot", Owner: "ann", OwnerID: "id1", Files: []infolder.UploadFile{
		{Path: "a", Size: 3, Result: &infolder.FileResult{Result: infolder.ResultNew, Path: "p/IN/lot/a"}},
		{Path: "b", Size: 4, Result: &infolder.FileResult{Result: infolder.ResultReplaced, Path: "p/IN/lot/b"}},
		{Path: "c", Size: 5, Result: &infolder.FileResult{Result: infolder.ResultSkipped, Path: "p/IN/lot/c"}},
		{Path: "z.zip", Size: 6, Result: &infolder.FileResult{Result: infolder.ResultUnzipped, Path: "p/IN/lot/z"}},
	}})
	got, err := log.Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		action audit.Action
		path   string
		size   int64
	}{
		{audit.ActionCreate, "p/IN/lot/a", 3},
		{audit.ActionUpdate, "p/IN/lot/b", 4},
		{audit.ActionCreate, "p/IN/lot/z", 6},
		{audit.ActionINUpload, "p/IN/lot", 13},
	}
	if len(got) != len(want) {
		t.Fatalf("entries: %+v", got)
	}
	for i, w := range want {
		if got[i].Action != w.action || got[i].Path != w.path || got[i].Size != w.size || got[i].Actor != "ann" {
			t.Errorf("entry %d: %+v, want %+v", i, got[i], w)
		}
	}
	if got[3].To != "u1" {
		t.Errorf("the upload's id: %q", got[3].To)
	}
}
