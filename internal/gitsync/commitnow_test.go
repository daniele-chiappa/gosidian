package gitsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitOut runs git in dir and returns its output, trimmed.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func startSync(t *testing.T, dir string, edit func(*Sync)) *Sync {
	t.Helper()
	cfg := testCfg()
	cfg.Debounce = 10 * time.Second // CommitNow must not wait for it
	s := New(dir, cfg)
	if edit != nil {
		edit(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// CommitNow commits at once with its message and the sync's author, tags
// the commit, tells the listeners; again with nothing pending it finds
// the tag on HEAD; a tag on another commit is refused; with nothing
// pending a new tag goes on HEAD (IMP-201).
func TestSync_CommitNow(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	var told []CommitChanges
	s := startSync(t, dir, func(s *Sync) { s.OnCommit(func(c CommitChanges) { told = append(told, c) }) })
	ctx := context.Background()

	writeFile(t, dir, "docs/manual-A/en.md", "---\nrevisione: V1\n---\n# Manual A\n")
	s.TriggerCommit()
	res, err := s.CommitNow(ctx, "revision V1 of manual-A-en", "doc/manual-A-en/V1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || len(res.SHA) != 40 || !strings.HasPrefix(res.SHA, res.Short) || res.Short == "" || res.Tag != "doc/manual-A-en/V1" || res.Pushed {
		t.Fatalf("result %+v", res)
	}
	if got := gitOut(t, dir, "log", "-1", "--format=%H|%s|%an <%ae>"); got != res.SHA+"|revision V1 of manual-A-en|Test Bot <bot@example.com>" {
		t.Errorf("HEAD: %s", got)
	}
	if got := gitOut(t, dir, "cat-file", "-t", "refs/tags/doc/manual-A-en/V1"); got != "tag" {
		t.Errorf("not an annotated tag: %s", got)
	}
	if got := gitOut(t, dir, "rev-parse", "doc/manual-A-en/V1^{commit}"); got != res.SHA {
		t.Errorf("the tag is on %s", got)
	}
	if got := gitOut(t, dir, "show", "doc/manual-A-en/V1:docs/manual-A/en.md"); !strings.Contains(got, "revisione: V1") {
		t.Errorf("the revision from its tag: %s", got)
	}
	if len(told) != 1 || told[0].SHA != res.SHA || len(told[0].Files) != 1 || told[0].Files[0].Path != "docs/manual-A/en.md" {
		t.Errorf("listeners told %+v", told)
	}
	// The pending debounced commit was taken over: a flush finds nothing.
	s.Flush()
	if n := gitOut(t, dir, "rev-list", "--count", "HEAD"); n != "2" { // init + V1
		t.Errorf("%s commits", n)
	}

	// Again: nothing pending, the tag already on HEAD.
	again, err := s.CommitNow(ctx, "revision V1 of manual-A-en", "doc/manual-A-en/V1")
	if err != nil || again.Committed || again.SHA != res.SHA || again.Tag != "doc/manual-A-en/V1" {
		t.Fatalf("again: %+v %v", again, err)
	}

	// The same tag on a new commit: refused, the tag stays.
	writeFile(t, dir, "docs/manual-A/en.md", "---\nrevisione: V2\n---\n# Manual A\n")
	moved, err := s.CommitNow(ctx, "revision V2", "doc/manual-A-en/V1")
	if err == nil || !strings.Contains(err.Error(), "never moved") || !moved.Committed {
		t.Fatalf("moving a tag: %+v %v", moved, err)
	}
	if got := gitOut(t, dir, "rev-parse", "doc/manual-A-en/V1^{commit}"); got != res.SHA {
		t.Errorf("the tag moved to %s", got)
	}

	// Nothing pending: the new tag goes on HEAD.
	v2, err := s.CommitNow(ctx, "revision V2", "doc/manual-A-en/V2")
	if err != nil || v2.Committed || v2.SHA != moved.SHA {
		t.Fatalf("a tag on HEAD: %+v %v", v2, err)
	}
	if got := gitOut(t, dir, "rev-parse", "doc/manual-A-en/V2^{commit}"); got != moved.SHA {
		t.Errorf("V2 is on %s", got)
	}

	// Without a tag and nothing pending: HEAD, no commit.
	none, err := s.CommitNow(ctx, "nothing", "")
	if err != nil || none.Committed || none.SHA != moved.SHA || none.Tag != "" {
		t.Fatalf("nothing pending: %+v %v", none, err)
	}
	if st := s.Status(); !st.Healthy {
		t.Errorf("status %+v", st)
	}
}

// A bad tag or message commits nothing; git sync off says so.
func TestSync_CommitNowRefusals(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	s := startSync(t, dir, nil)
	writeFile(t, dir, "a.md", "a")
	ctx := context.Background()
	for _, c := range []struct{ msg, tag string }{
		{"m", "v2.71.3"}, {"m", "doc/a b"}, {"m", "-doc/a"}, {"m", "doc/a..b"}, {"m", "doc/a.lock"},
		{"", "doc/a/V1"}, {" \n ", ""}, {strings.Repeat("x", MaxCommitMessage+1), ""}, {"a\x00b", ""},
	} {
		if _, err := s.CommitNow(ctx, c.msg, c.tag); err == nil {
			t.Errorf("CommitNow(%.20q, %q) accepted", c.msg, c.tag)
		}
	}
	if n := gitOut(t, dir, "rev-list", "--count", "HEAD"); n != "1" {
		t.Errorf("a refused CommitNow committed: %s commits", n)
	}

	cfg := testCfg()
	cfg.Enabled = false
	if _, err := New(t.TempDir(), cfg).CommitNow(ctx, "m", ""); !errors.Is(err, ErrDisabled) {
		t.Errorf("git sync off: %v", err)
	}
}

// With push on, the commit and its tag reach the remote; a tag whose push
// failed goes with the next push of the branch (--follow-tags).
func TestSync_CommitNowPushesTheTag(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitOut(t, ".", "init", "-q", "--bare", bare)
	dir := t.TempDir()
	s := startSync(t, dir, func(s *Sync) { s.cfg.Push, s.cfg.Remote = true, bare })
	ctx := context.Background()

	writeFile(t, dir, "docs/a.md", "V1")
	res, err := s.CommitNow(ctx, "revision V1", "doc/manual-A-en/V1")
	if err != nil || !res.Pushed || res.PushError != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := gitOut(t, bare, "rev-parse", "main", "doc/manual-A-en/V1^{commit}"); got != res.SHA+"\n"+res.SHA {
		t.Errorf("remote: %s", got)
	}

	// The remote away: committed and tagged here, the push failed.
	gitOut(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	writeFile(t, dir, "docs/a.md", "V2")
	v2, err := s.CommitNow(ctx, "revision V2", "doc/manual-A-en/V2")
	if err != nil || v2.Pushed || v2.PushError == "" || !v2.Committed || v2.Tag == "" {
		t.Fatalf("a failed push: %+v %v", v2, err)
	}
	if st := s.Status(); st.Healthy {
		t.Error("a failed push left the sync healthy")
	}

	// The remote back: the next debounced commit takes the tag along.
	gitOut(t, dir, "remote", "set-url", "origin", bare)
	writeFile(t, dir, "docs/b.md", "b")
	s.TriggerCommit()
	s.Flush()
	if got := gitOut(t, bare, "rev-parse", "doc/manual-A-en/V2^{commit}"); got != v2.SHA {
		t.Errorf("V2 on the remote: %s", got)
	}
	if st := s.Status(); !st.Healthy {
		t.Errorf("status %+v", st)
	}
}

// CommitNow runs one at a time with the debounced commits and the writes
// around it: nothing fails, nothing is left out.
func TestSync_CommitNowConcurrent(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	s := New(dir, cfg) // debounce 50 ms: the timer fires among the calls
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 8)
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 5; i++ {
				_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("w%d.md", w)), []byte(fmt.Sprint(i)), 0o644)
				s.TriggerCommit()
				if w%2 == 0 {
					if _, err := s.CommitNow(ctx, fmt.Sprintf("w%d run %d", w, i), fmt.Sprintf("doc/w%d/V%d", w, i)); err != nil {
						errs <- err
					}
				}
			}
		}()
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	s.Flush()
	if out := gitOut(t, dir, "status", "--porcelain"); out != "" {
		t.Errorf("left out:\n%s", out)
	}
	if tags := gitOut(t, dir, "tag", "-l"); len(strings.Fields(tags)) != 10 {
		t.Errorf("tags:\n%s", tags)
	}
}

func TestCheckTag(t *testing.T) {
	for _, ok := range []string{"doc/manual-A-en/V2", "doc/x/1.0", "release-notes/2026", "doc/Contabilità/V1", "a/b"} {
		if err := CheckTag(ok); err != nil {
			t.Errorf("CheckTag(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "V2", "doc", "v2.71.3", "v2/doc/x", "V1/x", "refs/tags/x", "heads/x", "origin/main", "HEAD/x",
		"-doc/x", "/doc/x", "doc/x/", "doc//x", "doc/../x", "doc/.x", "doc/x.lock", "doc/x.", "doc/x@{1}",
		"doc/a b", "doc/a~1", "doc/a^", "doc/a:b", "doc/a?", "doc/a*", "doc/a[", "doc/a\\b", "doc/a\tb", "doc/a\x7f",
		"doc/" + strings.Repeat("x", MaxTagLen), "doc/\xff",
	} {
		if err := CheckTag(bad); err == nil {
			t.Errorf("CheckTag(%q) accepted", bad)
		}
	}
}
