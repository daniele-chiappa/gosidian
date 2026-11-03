package gitsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/projects"
)

// A commit of the log, as the history of a note shows it.
type logEntry struct {
	author, subject, body string
	files                 []string // name-status lines, "A\tp/a.md"
}

// gitLog reads the commits of dir, newest first, but the one Start makes.
func gitLog(t *testing.T, dir string) []logEntry {
	t.Helper()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	var out []logEntry
	for _, sha := range strings.Fields(git("log", "--format=%H")) {
		e := logEntry{
			author:  git("show", "-s", "--format=%an <%ae>", sha),
			subject: git("show", "-s", "--format=%s", sha),
			body:    git("show", "-s", "--format=%b", sha),
		}
		if e.subject == "chore: init gosidian vault" {
			continue
		}
		if files := git("diff-tree", "--no-commit-id", "--name-status", "-r", "-M", "--root", sha); files != "" {
			e.files = strings.Split(files, "\n")
		}
		out = append(out, e)
	}
	return out
}

// startWrites starts a Sync on a fresh vault and gives back a writer of its
// files. Its debounce is an hour: a write recorded asks for a commit, which
// the test makes with flushNow, so none comes between two of its steps.
func startWrites(t *testing.T) (*Sync, string, func(rel, content string)) {
	t.Helper()
	return startWritesAfter(t, time.Hour)
}

// startWritesAfter is startWrites with a debounce of its own, for a test
// that waits for the commit to come by itself.
func startWritesAfter(t *testing.T, debounce time.Duration) (*Sync, string, func(rel, content string)) {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Debounce = debounce
	s := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, dir, write
}

func flushNow(s *Sync) {
	s.TriggerCommit()
	s.Flush()
}

// A note written through gosidian is committed in the name of who wrote it,
// with the email of the configuration, and its subject says what was done
// to which note (IMP-222, F-01). The history said "Test Bot" and "auto:
// <date>" for every save.
func TestSync_CommitInTheNameOfTheWriter(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "daniele")
	flushNow(s)
	log := gitLog(t, dir)
	if len(log) != 1 {
		t.Fatalf("%d commits, want 1: %+v", len(log), log)
	}
	if log[0].author != "daniele <bot@example.com>" || log[0].subject != "create p/a.md" {
		t.Errorf("commit = %q %q, want daniele's create p/a.md", log[0].author, log[0].subject)
	}

	// A file no write of gosidian names (a pull, an editor outside it) keeps
	// the configured author and the auto message.
	write("p/outside.md", "# Outside\n")
	flushNow(s)
	log = gitLog(t, dir)
	if len(log) != 2 || log[0].author != "Test Bot <bot@example.com>" || !strings.HasPrefix(log[0].subject, "auto: ") {
		t.Errorf("an outside change = %+v, want the configured author and auto:", log[0])
	}
}

// Two writers in the same debounce window get a commit each, with their
// own files, and what nobody wrote through gosidian a last one: the
// history of each note names who wrote it. Each commit is told to the
// listeners.
func TestSync_ACommitPerWriter(t *testing.T) {
	s, dir, write := startWrites(t)
	var told [][]Change
	s.OnCommit(func(c CommitChanges) { told = append(told, c.Files) })
	write("p/a.md", "# A\n")
	write("p/b.md", "# B\n")
	write("p/c.md", "# C\n")
	s.RecordWrite("create", "p/a.md", "", "daniele")
	s.RecordWrite("create", "p/b.md", "", "claude-cli (daniele)")
	flushNow(s)

	log := gitLog(t, dir)
	if len(log) != 3 {
		t.Fatalf("%d commits, want 3: %+v", len(log), log)
	}
	byAuthor := map[string]logEntry{}
	for _, e := range log {
		byAuthor[e.author] = e
	}
	for author, want := range map[string]string{
		"daniele <bot@example.com>":              "A\tp/a.md",
		"claude-cli (daniele) <bot@example.com>": "A\tp/b.md",
		"Test Bot <bot@example.com>":             "A\tp/c.md",
	} {
		if e := byAuthor[author]; !slices.Equal(e.files, []string{want}) {
			t.Errorf("%s committed %q, want %q", author, e.files, want)
		}
	}
	// The writers first, in the order they wrote; the rest last.
	if log[0].author != "Test Bot <bot@example.com>" || log[2].author != "daniele <bot@example.com>" {
		t.Errorf("order (newest first) = %q, %q, %q", log[0].author, log[1].author, log[2].author)
	}
	if len(told) != 3 {
		t.Errorf("listeners told of %d commits, want 3", len(told))
	}
}

// One writer of several notes makes one commit: its subject names the
// first and how many more, its body one line per note. A note created and
// then edited in the window says create.
func TestSync_OneCommitForSeveralNotes(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "# A\n")
	write("p/b.md", "# B\n")
	flushNow(s)

	write("p/a.md", "# A edited\n")
	write("p/b.md", "# B edited\n")
	write("p/new.md", "# New\n")
	s.RecordWrite("update", "p/a.md", "", "daniele")
	s.RecordWrite("create", "p/new.md", "", "daniele")
	s.RecordWrite("update", "p/new.md", "", "daniele")
	s.RecordWrite("append", "p/b.md", "", "daniele")
	flushNow(s)

	log := gitLog(t, dir)
	if len(log) != 2 {
		t.Fatalf("%d commits, want 2", len(log))
	}
	if log[0].subject != "update p/a.md and 2 more" {
		t.Errorf("subject = %q", log[0].subject)
	}
	if want := "update p/a.md\ncreate p/new.md\nappend p/b.md"; log[0].body != want {
		t.Errorf("body = %q, want %q", log[0].body, want)
	}
}

// A commit names the notes of one project: one writer in two projects
// makes a commit for each, or the history of a note would name a note of a
// project its reader may not see.
func TestSync_ACommitPerProject(t *testing.T) {
	s, dir, write := startWrites(t)
	write("pub/a.md", "# A\n")
	write("secret/b.md", "# B\n")
	s.RecordWrite("create", "pub/a.md", "", "daniele")
	s.RecordWrite("create", "secret/b.md", "", "daniele")
	flushNow(s)
	log := gitLog(t, dir)
	if len(log) != 2 {
		t.Fatalf("%d commits, want 2: %+v", len(log), log)
	}
	// The order of the first write: pub first, so it is the older.
	if log[1].subject != "create pub/a.md" || !slices.Equal(log[1].files, []string{"A\tpub/a.md"}) {
		t.Errorf("first commit = %+v", log[1])
	}
	if log[0].subject != "create secret/b.md" || !slices.Equal(log[0].files, []string{"A\tsecret/b.md"}) {
		t.Errorf("second commit = %+v", log[0])
	}
}

// A rename names both paths and a delete its note, each by its writer; a
// project renamed or deleted names the project, and its files go with it.
func TestSync_RenameDeleteAndProjects(t *testing.T) {
	s, dir, write := startWrites(t)
	long := "A text long enough for git to find the move as a rename.\n"
	write("p/a.md", "# A\n"+long)
	write("p/b.md", "# B\n")
	write("old/x.md", "# X\n"+long)
	write("gone/y.md", "# Y\n")
	flushNow(s)

	if err := os.Rename(filepath.Join(dir, "p/a.md"), filepath.Join(dir, "p/z.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "p/b.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "old"), filepath.Join(dir, "new")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}
	s.RecordWrite("rename", "p/a.md", "p/z.md", "daniele")
	s.RecordWrite("delete", "p/b.md", "", "anna")
	s.RecordWrite("rename_project", "old", "new", "anna")
	s.RecordWrite("delete_project", "gone", "", "daniele")
	flushNow(s)

	// Four commits, and the one of the files written first.
	log := gitLog(t, dir)
	if len(log) != 5 {
		t.Fatalf("%d commits, want 5: %+v", len(log), log)
	}
	got := map[string]logEntry{}
	for _, e := range log {
		got[e.subject] = e
	}
	for subject, want := range map[string]struct {
		author string
		files  []string
	}{
		"rename p/a.md -> p/z.md":   {"daniele <bot@example.com>", []string{"R100\tp/a.md\tp/z.md"}},
		"delete p/b.md":             {"anna <bot@example.com>", []string{"D\tp/b.md"}},
		"rename project old -> new": {"anna <bot@example.com>", []string{"R100\told/x.md\tnew/x.md"}},
		"delete project gone":       {"daniele <bot@example.com>", []string{"D\tgone/y.md"}},
	} {
		e, ok := got[subject]
		if !ok || e.author != want.author || !slices.Equal(e.files, want.files) {
			t.Errorf("%q = %+v, want %s with %q", subject, e, want.author, want.files)
		}
	}
}

// What a commit could not take stays for the next one: a commit refused
// (here by a hook) keeps the writers, and the commit after names them. A
// write whose file did not change is forgotten at the next commit, so the
// list does not grow.
func TestSync_WritesKeptUntilCommitted(t *testing.T) {
	s, dir, write := startWrites(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "daniele")
	flushNow(s)
	if log := gitLog(t, dir); len(log) != 0 {
		t.Fatalf("the hook let %d commits through", len(log))
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	s.RecordWrite("update", "p/unchanged.md", "", "anna")
	flushNow(s)
	log := gitLog(t, dir)
	if len(log) != 1 || log[0].author != "daniele <bot@example.com>" || log[0].subject != "create p/a.md" {
		t.Fatalf("after the hook = %+v", log)
	}
	s.mu.Lock()
	left := len(s.writes)
	s.mu.Unlock()
	if left != 0 {
		t.Errorf("%d writes left after the commit, want none", left)
	}
}

// A name is a git author's name: the brackets and line ends that would
// break it go, and an empty one is the configured author.
func TestSync_WriterNames(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "# A\n")
	write("p/b.md", "# B\n")
	s.RecordWrite("create", "p/a.md", "", "evil <x@y>\nname")
	s.RecordWrite("create", "p/b.md", "", "  ")
	flushNow(s)
	authors := []string{}
	for _, e := range gitLog(t, dir) {
		authors = append(authors, e.author)
	}
	slices.Sort(authors)
	if want := []string{"Test Bot <bot@example.com>", "evil x@y name <bot@example.com>"}; !slices.Equal(authors, want) {
		t.Errorf("authors = %q, want %q", authors, want)
	}
}

// A commit with a message of its own (a pipeline's revision, CommitNow)
// keeps it and the configured author, as before.
func TestSync_CommitNowKeepsItsMessage(t *testing.T) {
	s, dir, write := startWrites(t)
	write("docs/a.md", "# A\n")
	s.RecordWrite("create", "docs/a.md", "", "daniele")
	if _, err := s.CommitNow(context.Background(), "revision 2 of A", ""); err != nil {
		t.Fatal(err)
	}
	log := gitLog(t, dir)
	if len(log) != 1 || log[0].author != "Test Bot <bot@example.com>" || log[0].subject != "revision 2 of A" {
		t.Errorf("CommitNow = %+v", log)
	}
}

// A restore commits at once, in the writer's name, and says which version
// came back (IMP-222, D5): the history shows it at its top without waiting
// for the debounce. What others wrote meanwhile gets its own commits.
func TestSync_CommitWritesNow(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "# A\n")
	flushNow(s)
	write("p/a.md", "# A, as it was\n")
	write("p/b.md", "# B\n")
	sha := "0123456789abcdef0123456789abcdef01234567"
	s.RecordWrite("restore", "p/a.md", sha, "daniele")
	s.RecordWrite("create", "p/b.md", "", "anna")
	if err := s.CommitWrites(); err != nil {
		t.Fatal(err)
	}
	log := gitLog(t, dir)
	if len(log) != 3 {
		t.Fatalf("%d commits, want 3: %+v", len(log), log)
	}
	if log[1].author != "daniele <bot@example.com>" || log[1].subject != "restore p/a.md to 0123456" {
		t.Errorf("restore commit = %+v", log[1])
	}
	if log[0].author != "anna <bot@example.com>" || log[0].subject != "create p/b.md" {
		t.Errorf("anna's commit = %+v", log[0])
	}
	// Nothing pending: no commit, no error.
	if err := s.CommitWrites(); err != nil {
		t.Errorf("nothing pending = %v", err)
	}
	if n := len(gitLog(t, dir)); n != 3 {
		t.Errorf("%d commits after an empty CommitWrites, want 3", n)
	}
}

// onTheWay is the set of paths whose lock a writer holds, as
// vault.PathBusy tells them to a commit (BUG-147).
type onTheWay struct {
	mu    sync.Mutex
	paths map[string]bool
}

func (o *onTheWay) set(rel string, on bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.paths == nil {
		o.paths = map[string]bool{}
	}
	o.paths[rel] = on
}

func (o *onTheWay) busy(rel string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.paths[rel]
}

func pendingWriteCount(s *Sync) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

// A write's record comes from the audit log once its file is on disk: a
// commit in between took the file as nobody's, "auto:", and the record,
// left over, gave its verb to the next commit of the note, "create" for an
// edit (BUG-147). A file whose lock a writer holds waits for the commit
// after its record.
func TestSync_AWriteOnItsWayWaitsForItsRecord(t *testing.T) {
	s, dir, write := startWrites(t)
	var way onTheWay
	s.SetBusy(way.busy)
	way.set("p/a.md", true)
	write("p/a.md", "# A\n")
	flushNow(s)
	if log := gitLog(t, dir); len(log) != 0 {
		t.Fatalf("a write on its way was committed: %+v", log)
	}
	s.RecordWrite("create", "p/a.md", "", "alice")
	way.set("p/a.md", false)
	flushNow(s)
	write("p/a.md", "# A edited\n")
	s.RecordWrite("update", "p/a.md", "", "alice")
	flushNow(s)

	log := gitLog(t, dir)
	if len(log) != 2 {
		t.Fatalf("%d commits, want 2: %+v", len(log), log)
	}
	for i, want := range []logEntry{
		{author: "alice <bot@example.com>", subject: "update p/a.md", files: []string{"M\tp/a.md"}},
		{author: "alice <bot@example.com>", subject: "create p/a.md", files: []string{"A\tp/a.md"}},
	} {
		if log[i].author != want.author || log[i].subject != want.subject || !slices.Equal(log[i].files, want.files) {
			t.Errorf("commit %d = %+v, want %+v", i, log[i], want)
		}
	}
	if n := pendingWriteCount(s); n != 0 {
		t.Errorf("%d writes left, want none", n)
	}
}

// A record that comes while the commit reads the files names a file the
// commit may have read before the write or after it: the file waits too,
// and the commit after names its writer.
func TestSync_ARecordDuringTheCommitWaits(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "# A\n")
	var once sync.Once
	// The commit asks for the locks once it has read the files: the record
	// comes then.
	s.SetBusy(func(string) bool {
		once.Do(func() { s.RecordWrite("create", "p/a.md", "", "alice") })
		return false
	})
	flushNow(s)
	flushNow(s)
	log := gitLog(t, dir)
	if len(log) != 1 || log[0].author != "alice <bot@example.com>" || log[0].subject != "create p/a.md" {
		t.Errorf("log = %+v, want one commit, alice's create p/a.md", log)
	}
	if n := pendingWriteCount(s); n != 0 {
		t.Errorf("%d writes left, want none", n)
	}
}

// A file left out lets the others through, each in its writer's name, and
// keeps its writes. The commit after, which the one that left it out asks
// for, comes by itself and names the last writer of the file, as it does
// for two writers in the same window.
func TestSync_AFileLeftOutLetsTheOthersThrough(t *testing.T) {
	s, dir, write := startWritesAfter(t, 300*time.Millisecond)
	var way onTheWay
	s.SetBusy(way.busy)
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	// anna's edit of a.md is on its way: on disk, not recorded yet.
	way.set("p/a.md", true)
	write("p/a.md", "# A, as anna wrote it\n")
	write("p/b.md", "# B\n")
	s.RecordWrite("create", "p/b.md", "", "bob")
	write("p/c.md", "# C\n")
	flushNow(s)

	log := gitLog(t, dir)
	if len(log) != 2 {
		t.Fatalf("%d commits, want 2 (b.md and c.md): %+v", len(log), log)
	}
	if log[1].author != "bob <bot@example.com>" || log[1].subject != "create p/b.md" || !slices.Equal(log[1].files, []string{"A\tp/b.md"}) {
		t.Errorf("bob's commit = %+v", log[1])
	}
	if log[0].author != "Test Bot <bot@example.com>" || !strings.HasPrefix(log[0].subject, "auto: ") || !slices.Equal(log[0].files, []string{"A\tp/c.md"}) {
		t.Errorf("the commit of what nobody wrote = %+v", log[0])
	}

	s.RecordWrite("update", "p/a.md", "", "anna")
	way.set("p/a.md", false)
	for deadline := time.Now().Add(10 * time.Second); len(gitLog(t, dir)) < 3; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no commit came for the file left out")
		}
	}
	log = gitLog(t, dir)
	if len(log) != 3 || log[0].author != "anna <bot@example.com>" || log[0].subject != "update p/a.md" || !slices.Equal(log[0].files, []string{"A\tp/a.md"}) {
		t.Errorf("the commit after = %+v, want anna's update with A p/a.md", log)
	}
	if n := pendingWriteCount(s); n != 0 {
		t.Errorf("%d writes left, want none", n)
	}
}

// A commit split among writers takes each file as git read it. A write
// that comes after, its record later still, goes in the commit after, in
// its writer's name: staged again from disk, the file took that write in
// the name of the writer before (BUG-147).
func TestSync_ACommitTakesTheFilesAsItReadThem(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	write("p/b.md", "# B\n")
	s.RecordWrite("create", "p/b.md", "", "bob")
	var once sync.Once
	var werr error
	// anna's edit of a.md comes once the commit has read the files.
	s.SetBusy(func(string) bool {
		once.Do(func() {
			werr = os.WriteFile(filepath.Join(dir, "p/a.md"), []byte("# A, as anna wrote it\n"), 0o644)
		})
		return false
	})
	flushNow(s)
	if werr != nil {
		t.Fatal(werr)
	}
	s.RecordWrite("update", "p/a.md", "", "anna")
	flushNow(s)

	log := gitLog(t, dir)
	if len(log) != 3 {
		t.Fatalf("%d commits, want 3: %+v", len(log), log)
	}
	for i, want := range []logEntry{
		{author: "anna <bot@example.com>", subject: "update p/a.md", files: []string{"M\tp/a.md"}},
		{author: "bob <bot@example.com>", subject: "create p/b.md", files: []string{"A\tp/b.md"}},
		{author: "alice <bot@example.com>", subject: "create p/a.md", files: []string{"A\tp/a.md"}},
	} {
		if log[i].author != want.author || log[i].subject != want.subject || !slices.Equal(log[i].files, want.files) {
			t.Errorf("commit %d = %+v, want %+v", i, log[i], want)
		}
	}
}

// An IN folder turned on leaves git: the next commit takes its files out
// of the index. Split among writers, that commit staged each group again
// with git add, which refuses a path .gitignore names, so it failed, and
// so did every commit after it (found in BUG-147's lab runs). Staged
// again from the tree git read, it goes through.
func TestSync_ACommitSplitTakesAnINFolderJustIgnored(t *testing.T) {
	s, dir, write := startWrites(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	write("p/IN/scan.pdf", "%PDF-1.7\n")
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	flushNow(s)

	if err := pstore.Set("p", projects.Flags{UseIN: true}); err != nil {
		t.Fatal(err)
	}
	write("p/a.md", "# A, again\n")
	s.RecordWrite("update", "p/a.md", "", "alice")
	write("q/b.md", "# B\n")
	s.RecordWrite("create", "q/b.md", "", "bob")
	flushNow(s)

	if st := s.Status(); !st.Healthy {
		t.Fatalf("the commit failed: %s", st.LastError)
	}
	log := gitLog(t, dir)
	if len(log) < 3 {
		t.Fatalf("%d commits, want 3 more after the first: %+v", len(log), log)
	}
	for _, want := range []logEntry{
		{author: "alice <bot@example.com>", subject: "update p/a.md", files: []string{"M\tp/a.md"}},
		{author: "bob <bot@example.com>", subject: "create q/b.md", files: []string{"A\tq/b.md"}},
		{author: "Test Bot <bot@example.com>", files: []string{"M\t.gitignore", "D\tp/IN/scan.pdf"}},
	} {
		if !slices.ContainsFunc(log[:3], func(e logEntry) bool {
			return e.author == want.author && (want.subject == "" || e.subject == want.subject) && slices.Equal(e.files, want.files)
		}) {
			t.Errorf("no commit %+v in %+v", want, log[:3])
		}
	}
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err != nil || len(out) != 0 {
		t.Errorf("left after the commit: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "p/IN/scan.pdf")); err != nil {
		t.Errorf("the file went from the disk with the index: %v", err)
	}
}

// A project whose git sync just went off leaves git's index at the commit
// after, while one of its notes is being saved (BUG-149). The commit left
// that note's way out of git for the next one, which saw it in the index
// again, as HEAD had it, and tracked it from then on: the project's next
// saves went to the remote.
func TestSync_AProjectJustSkippedLeavesGitWithANoteOnItsWay(t *testing.T) {
	s, dir, write := startWrites(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	var way onTheWay
	s.SetBusy(way.busy)
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	write("q/b.md", "# B\n")
	s.RecordWrite("create", "q/b.md", "", "bob")
	flushNow(s)

	if err := pstore.Set("p", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	way.set("p/a.md", true) // alice saves p/a.md as the commit runs
	write("q/b.md", "# B, again\n")
	s.RecordWrite("update", "q/b.md", "", "bob")
	flushNow(s)
	way.set("p/a.md", false)
	write("p/a.md", "# A, not for the remote\n")
	s.RecordWrite("update", "p/a.md", "", "alice")
	flushNow(s)

	if st := s.Status(); !st.Healthy {
		t.Fatalf("the commit failed: %s", st.LastError)
	}
	if out, err := exec.Command("git", "-C", dir, "ls-files", "--", "p").Output(); err != nil || len(out) != 0 {
		t.Errorf("p in git's index: %q %v", out, err)
	}
	if out, _ := exec.Command("git", "-C", dir, "log", "-p", "--format=%an %s", "--", "p").Output(); strings.Contains(string(out), "not for the remote") {
		t.Errorf("a commit holds p's save after its git sync went off:\n%s", out)
	}
}

// What git sync takes out of git is no one's write: a project's notes leave
// it in the commit without a writer, not in the commit of the last one who
// saved them, whose subject would say "update" for a file git deletes
// (BUG-149).
func TestSync_WhatGitSyncTakesOutIsNoWritersCommit(t *testing.T) {
	s, dir, write := startWrites(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	flushNow(s)

	write("p/a.md", "# A, again\n")
	s.RecordWrite("update", "p/a.md", "", "alice")
	if err := pstore.Set("p", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	write("q/b.md", "# B\n")
	s.RecordWrite("create", "q/b.md", "", "bob")
	flushNow(s)

	log := gitLog(t, dir)
	if len(log) < 3 {
		t.Fatalf("%d commits, want 2 more after the first: %+v", len(log), log)
	}
	for _, want := range []logEntry{
		{author: "bob <bot@example.com>", subject: "create q/b.md", files: []string{"A\tq/b.md"}},
		{author: "Test Bot <bot@example.com>", files: []string{"M\t.gitignore", "D\tp/a.md"}},
	} {
		if !slices.ContainsFunc(log[:2], func(e logEntry) bool {
			return e.author == want.author && (want.subject == "" || e.subject == want.subject) && slices.Equal(e.files, want.files)
		}) {
			t.Errorf("no commit %+v in %+v", want, log[:2])
		}
	}
}

// git may read a file taken out of git and a new one with its bytes as a
// rename. The new file's write is on its way, so it waits; the file taken
// out does not, and stays out of git (BUG-149).
func TestSync_AFileTakenOutIsNoRenameOfANewOne(t *testing.T) {
	s, dir, write := startWrites(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	var way onTheWay
	s.SetBusy(way.busy)
	write("p/a.md", "# A, the same bytes\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	flushNow(s)

	if err := pstore.Set("p", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	write("q/c.md", "# A, the same bytes\n")
	way.set("q/c.md", true) // bob saves it as the commit runs
	flushNow(s)

	if out, err := exec.Command("git", "-C", dir, "ls-files", "--", "p").Output(); err != nil || len(out) != 0 {
		t.Errorf("p in git's index: %q %v", out, err)
	}
	if out, err := exec.Command("git", "-C", dir, "ls-files", "--", "q").Output(); err != nil || len(out) != 0 {
		t.Errorf("q/c.md committed while its write was on its way: %q %v", out, err)
	}
	if log := gitLog(t, dir); len(log) == 0 || !slices.Equal(log[0].files, []string{"M\t.gitignore", "D\tp/a.md"}) {
		t.Errorf("the last commit is not p leaving git: %+v", log)
	}
}

// What git sync changes itself, the managed block of .gitignore and the
// folders it takes out of git or tracks again, goes in a commit of its own,
// in the configured author's name, whose message says what (BUG-148). It
// went in the auto commit with what no write names, "auto: <date>".
func TestSync_GitSyncsOwnChangesSayWhatTheyAre(t *testing.T) {
	s, dir, write := startWrites(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	write("p/a.md", "# A\n")
	s.RecordWrite("create", "p/a.md", "", "alice")
	flushNow(s)

	has := func(log []logEntry, want logEntry) bool {
		return slices.ContainsFunc(log, func(e logEntry) bool {
			return e.author == want.author && e.subject == want.subject && e.body == want.body && slices.Equal(e.files, want.files)
		})
	}
	// p's git sync goes off, as bob writes in q.
	if err := pstore.Set("p", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	write("q/b.md", "# B\n")
	s.RecordWrite("create", "q/b.md", "", "bob")
	flushNow(s)
	log := gitLog(t, dir)
	if len(log) != 3 {
		t.Fatalf("%d commits, want 2 more after the first: %+v", len(log), log)
	}
	for _, want := range []logEntry{
		{author: "bob <bot@example.com>", subject: "create q/b.md", files: []string{"A\tq/b.md"}},
		{author: "Test Bot <bot@example.com>", subject: "git sync: update .gitignore and 1 more",
			body: "update .gitignore\nstop tracking p", files: []string{"M\t.gitignore", "D\tp/a.md"}},
	} {
		if !has(log[:2], want) {
			t.Errorf("no commit %+v in %+v", want, log[:2])
		}
	}

	// Back on: git tracks p's files again, and a file a write names goes
	// with its writer.
	write("p/a.md", "# A, while off\n")
	write("p/c.md", "# C, while off\n")
	if err := pstore.Set("p", projects.Flags{}); err != nil {
		t.Fatal(err)
	}
	write("p/c.md", "# C\n")
	s.RecordWrite("update", "p/c.md", "", "carol")
	flushNow(s)
	log = gitLog(t, dir)
	if len(log) != 5 {
		t.Fatalf("%d commits, want 2 more: %+v", len(log), log)
	}
	for _, want := range []logEntry{
		{author: "carol <bot@example.com>", subject: "update p/c.md", files: []string{"A\tp/c.md"}},
		{author: "Test Bot <bot@example.com>", subject: "git sync: update .gitignore and 1 more",
			body: "update .gitignore\ntrack p again", files: []string{"M\t.gitignore", "A\tp/a.md"}},
	} {
		if !has(log[:2], want) {
			t.Errorf("no commit %+v in %+v", want, log[:2])
		}
	}
	for _, e := range log {
		if strings.HasPrefix(e.subject, "auto: ") {
			t.Errorf("an auto commit: %+v", e)
		}
	}

	// What no write names and git sync did not change keeps the auto message.
	write("q/outside.md", "# Outside\n")
	flushNow(s)
	if log = gitLog(t, dir); !strings.HasPrefix(log[0].subject, "auto: ") || !slices.Equal(log[0].files, []string{"A\tq/outside.md"}) {
		t.Errorf("an outside change = %+v, want the auto message", log[0])
	}
}

// A restore from the trash and a folder deleted name the files under their
// path, attachments included, as a project's writes do (BUG-148): the
// record named the trash id, or the folder alone, and the files went in the
// auto commit.
func TestSync_TrashAndFoldersCoverTheirFiles(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/f/a.md", "# A\n")
	write("p/f/attachments/x.png", "png")
	write("p/fx.md", "# Not in p/f\n")
	s.RecordWrite(RestoreFromTrash, "p/f", "1793650000000000000__p%2Ff", "alice")
	write("q/n.md", "# N\n")
	s.RecordWrite(RestoreFromTrash, "q", "1793650000000000001__q", "carol")
	flushNow(s)
	log := gitLog(t, dir)
	for _, want := range []logEntry{
		{author: "alice <bot@example.com>", subject: "restore p/f from the trash", files: []string{"A\tp/f/a.md", "A\tp/f/attachments/x.png"}},
		{author: "carol <bot@example.com>", subject: "restore project q from the trash", files: []string{"A\tq/n.md"}},
		{author: "Test Bot <bot@example.com>", files: []string{"A\tp/fx.md"}},
	} {
		if !slices.ContainsFunc(log, func(e logEntry) bool {
			return e.author == want.author && (want.subject == "" || e.subject == want.subject) && slices.Equal(e.files, want.files)
		}) {
			t.Errorf("no commit %+v in %+v", want, log)
		}
	}

	if err := os.RemoveAll(filepath.Join(dir, "p", "f")); err != nil {
		t.Fatal(err)
	}
	s.RecordWrite("delete", "p/f", "1793650000000000002__p%2Ff", "bob")
	flushNow(s)
	log = gitLog(t, dir)
	if log[0].author != "bob <bot@example.com>" || log[0].subject != "delete p/f" || !slices.Equal(log[0].files, []string{"D\tp/f/a.md", "D\tp/f/attachments/x.png"}) {
		t.Errorf("the folder's delete = %+v, want bob's delete p/f with its files", log[0])
	}
}

// An attachment uploaded or deleted is committed in its writer's name, with
// a subject that says what was done to it (BUG-148).
func TestSync_AttachmentsSayWhatWasDone(t *testing.T) {
	s, dir, write := startWrites(t)
	write("p/attachments/x.png", "png")
	s.RecordWrite("upload_attachment", "p/attachments/x.png", "", "alice")
	flushNow(s)
	log := gitLog(t, dir)
	if len(log) != 1 || log[0].author != "alice <bot@example.com>" || log[0].subject != "upload p/attachments/x.png" {
		t.Errorf("upload = %+v, want alice's upload p/attachments/x.png", log)
	}
	if err := os.Remove(filepath.Join(dir, "p", "attachments", "x.png")); err != nil {
		t.Fatal(err)
	}
	s.RecordWrite("delete_attachment", "p/attachments/x.png", "1793650000000000000__p%2Fattachments%2Fx.png", "bob")
	flushNow(s)
	log = gitLog(t, dir)
	if log[0].author != "bob <bot@example.com>" || log[0].subject != "delete attachment p/attachments/x.png" || !slices.Equal(log[0].files, []string{"D\tp/attachments/x.png"}) {
		t.Errorf("delete = %+v, want bob's delete attachment p/attachments/x.png", log[0])
	}
}

// A write recorded asks for the debounced commit that takes it (BUG-148):
// only the watcher asked, and for notes only, so an attachment, or a folder
// deleted through the web UI, waited for the next write of a note, off the
// remote meanwhile.
func TestSync_ARecordedWriteAsksForTheCommit(t *testing.T) {
	s, dir, write := startWritesAfter(t, 50*time.Millisecond)
	write("p/attachments/x.png", "png")
	s.RecordWrite("upload_attachment", "p/attachments/x.png", "", "alice")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(gitLog(t, dir)) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if log := gitLog(t, dir); len(log) != 1 || log[0].subject != "upload p/attachments/x.png" {
		t.Errorf("history = %+v, want alice's upload, with no other write to start the commit", log)
	}
}

// A write recorded never pushes back the commit on its way: a debounce that
// starts again at every record, besides the watcher's events, left the
// commit of a note waiting while writes kept coming (an e2e of the history
// of a note, run 9 of 10, on 28887cf).
func TestSync_ARecordDoesNotPushBackTheCommit(t *testing.T) {
	s, _, write := startWrites(t)
	write("p/a.md", "# A\n")
	s.TriggerCommit()
	s.mu.Lock()
	first := s.timer
	s.mu.Unlock()
	s.RecordWrite("upload_attachment", "p/attachments/x.png", "", "alice")
	s.mu.Lock()
	again := s.timer
	s.mu.Unlock()
	if first == nil || again != first {
		t.Errorf("the record started the debounce again (timer %p, then %p)", first, again)
	}
}

// A commit with a message of its own (CommitNow) leaves a write on its way
// out too: the record would give its verb to the next commit of the note.
func TestSync_CommitNowLeavesAWriteOnItsWayOut(t *testing.T) {
	s, dir, write := startWrites(t)
	var way onTheWay
	s.SetBusy(way.busy)
	write("docs/a.md", "# A\n")
	s.RecordWrite("create", "docs/a.md", "", "daniele")
	way.set("docs/b.md", true)
	write("docs/b.md", "# B\n")
	if _, err := s.CommitNow(context.Background(), "revision 2 of A", ""); err != nil {
		t.Fatal(err)
	}
	log := gitLog(t, dir)
	if len(log) != 1 || log[0].subject != "revision 2 of A" || !slices.Equal(log[0].files, []string{"A\tdocs/a.md"}) {
		t.Errorf("CommitNow = %+v, want revision 2 of A with docs/a.md alone", log)
	}
}

// With git sync off there is nothing to commit to.
func TestSync_CommitWritesOff(t *testing.T) {
	if err := New(t.TempDir(), config.GitConfig{}).CommitWrites(); !errors.Is(err, ErrDisabled) {
		t.Errorf("off = %v, want ErrDisabled", err)
	}
}
