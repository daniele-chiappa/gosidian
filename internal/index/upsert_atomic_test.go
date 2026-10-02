package index

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestUpsert_ReadersNeverSeeUnresolvedLinks: a note's links are resolved in
// the transaction that writes them, so a reader running while the note is
// re-indexed (lint right after memory_append, or the watcher's second
// Upsert) never finds them unresolved (BUG-079). The old Upsert committed
// the links with target_path NULL and resolved them in a second
// transaction: the reader caught that window on most rounds.
func TestUpsert_ReadersNeverSeeUnresolvedLinks(t *testing.T) {
	idx := openTest(t)
	const nTargets = 40
	var body strings.Builder
	body.WriteString("# Big\n")
	for k := 0; k < nTargets; k++ {
		upsert(t, idx, fmt.Sprintf("p/notes/target-%02d.md", k), fmt.Sprintf("Target %02d", k), "# t")
		fmt.Fprintf(&body, "- [[target-%02d]]\n", k)
	}
	body.WriteString("- [[big]]\n") // a link to the note itself
	doc := NoteDoc{Path: "p/big.md", Title: "big", Body: body.String(), ModTime: 1, Size: int64(body.Len())}
	if err := idx.Upsert(doc); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var unresolved, reads atomic.Int64
	var wg sync.WaitGroup
	// Stop the reader before the cleanup closes the index, also when a
	// t.Fatal ends the test early.
	defer func() { stop.Store(true); wg.Wait() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			outs, err := idx.Outlinks("p/big.md")
			if err != nil {
				t.Error(err)
				return
			}
			reads.Add(1)
			for _, o := range outs {
				if o.TargetPath == "" {
					unresolved.Add(1)
				}
			}
		}
	}()
	for round := 0; round < 100; round++ {
		doc.ModTime = int64(round + 2)
		if err := idx.Upsert(doc); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if reads.Load() == 0 {
		t.Fatal("the reader never ran")
	}
	if n := unresolved.Load(); n > 0 {
		t.Errorf("readers saw %d unresolved links over %d reads while the note was re-indexed", n, reads.Load())
	}

	outs, err := idx.Outlinks("p/big.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != nTargets+1 {
		t.Fatalf("outlinks = %d, want %d", len(outs), nTargets+1)
	}
	for _, o := range outs {
		if o.Target == "big" && o.TargetPath != "p/big.md" {
			t.Errorf("self-link resolved to %q, want p/big.md", o.TargetPath)
		}
	}
}

// TestUpsert_InboundResolvesEveryForm: links written before their target
// exists resolve when it is created, in every form resolveTarget accepts,
// not only an exact path, title or basename: a hot.md listing a plan
// before the plan is created no longer keeps the link unresolved until the
// next boot.
func TestUpsert_InboundResolvesEveryForm(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "proj/hot.md", "hot", "## Active plans\n\n- [[plans/20261001-x]]\n- [[20261001-x#Steps]]\n- [[Ship The Thing]]\n- [[20261001-x]]\n- [[unrelated-x]]\n")
	upsert(t, idx, "proj/plans/20261001-x.md", "20261001-x", "---\ntitle: Ship The Thing\n---\n\n## Steps\n")

	outs, err := idx.Outlinks("proj/hot.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs {
		want := "proj/plans/20261001-x.md"
		if o.Target == "unrelated-x" {
			want = ""
		}
		if o.TargetPath != want {
			t.Errorf("[[%s]] resolved to %q, want %q", o.Target, o.TargetPath, want)
		}
	}
}

// TestDelete_ReresolvesInboundLinks: a project rename indexes each note at
// its new path, then deletes the old one. The delete cleared the links that
// reached the old path, so x's [[y]] stayed unresolved although y was
// already indexed at the new path (BUG-081); it now resolves again.
func TestDelete_ReresolvesInboundLinks(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "a/x.md", "x", "see [[y]]")
	upsert(t, idx, "a/y.md", "y", "see [[x]]")
	// The order of vault.RenameProject: upsert the new path, delete the old.
	for _, name := range []string{"x", "y"} {
		other := map[string]string{"x": "y", "y": "x"}[name]
		upsert(t, idx, "b/"+name+".md", name, "see [["+other+"]]")
		if err := idx.Delete("a/" + name + ".md"); err != nil {
			t.Fatal(err)
		}
	}
	for src, want := range map[string]string{"b/x.md": "b/y.md", "b/y.md": "b/x.md"} {
		outs, err := idx.Outlinks(src)
		if err != nil {
			t.Fatal(err)
		}
		if len(outs) != 1 || outs[0].TargetPath != want {
			t.Errorf("%s outlinks = %+v, want one link to %s", src, outs, want)
		}
	}
	// A link whose only target is deleted ends unresolved.
	upsert(t, idx, "c/lonely.md", "lonely", "# l")
	upsert(t, idx, "c/ref.md", "ref", "[[lonely]]")
	if err := idx.Delete("c/lonely.md"); err != nil {
		t.Fatal(err)
	}
	if outs, _ := idx.Outlinks("c/ref.md"); len(outs) != 1 || outs[0].TargetPath != "" {
		t.Errorf("link to a deleted note = %+v, want unresolved", outs)
	}
}

// TestUpsertUnresolved_LeavesLinksForResolveAll: the boot scan's bulk path
// keeps resolution for one ResolveAll at the end.
func TestUpsertUnresolved_LeavesLinksForResolveAll(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/target.md", "Target", "# t")
	if err := idx.UpsertUnresolved(NoteDoc{Path: "p/src.md", Title: "src", Body: "[[target]]", ModTime: 1, Size: 10}); err != nil {
		t.Fatal(err)
	}
	outs, _ := idx.Outlinks("p/src.md")
	if len(outs) != 1 || outs[0].TargetPath != "" {
		t.Fatalf("before ResolveAll: %+v, want one unresolved link", outs)
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	outs, _ = idx.Outlinks("p/src.md")
	if len(outs) != 1 || outs[0].TargetPath != "p/target.md" {
		t.Fatalf("after ResolveAll: %+v, want p/target.md", outs)
	}
}
