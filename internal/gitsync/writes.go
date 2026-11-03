package gitsync

import (
	"bytes"
	"cmp"
	"errors"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Who wrote what since the last commit (IMP-222, F-01): the audit log tells
// through RecordWrite, and the debounced commit makes a commit per writer,
// in the writer's name with the configured email, whose subject says what
// was done to which note. The history of a note said the configured author
// and "auto: <date>" for every save. What git sync changes itself has a
// commit of its own in the configured author's name, which says what
// (BUG-148). What no write names (a pull, an editor outside gosidian) keeps
// the configured author and the auto message.

// RestoreFromTrash is the action of a write that brought path, a note, a
// folder or a project, back from the trash entry to (BUG-148).
const RestoreFromTrash = "restore_trash"

// write is one write of a note, or of a project's notes, as the audit log
// names it: by did action on path, to the new path or name of a rename.
type write struct {
	seq                  uint64
	action, path, to, by string
}

// RecordWrite notes a write until the commit that takes its file, and asks
// for that commit, debounced, unless one is on its way: the watcher asks
// for notes only, so an attachment, or a folder deleted or restored, waited
// for the next write of a note, off the remote meanwhile (BUG-148). Safe
// from any goroutine.
func (s *Sync) RecordWrite(action, path, to, by string) {
	if !s.cfg.Enabled {
		return
	}
	s.mu.Lock()
	s.writeSeq++
	s.writes = append(s.writes, write{seq: s.writeSeq, action: action, path: path, to: to, by: authorName(by)})
	s.mu.Unlock()
	s.RequestCommit()
}

// pendingWrites gives the writes noted so far, and the last one's number.
func (s *Sync) pendingWrites() ([]write, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.writes), s.writeSeq
}

// writesSince gives the writes noted after from.
func (s *Sync) writesSince(from uint64) []write {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []write
	for _, w := range s.writes {
		if w.seq > from {
			out = append(out, w)
		}
	}
	return out
}

// forgetWrites drops the writes up to upTo once a commit took what they
// wrote; a write whose file did not change goes with them, so the list
// does not grow. The writes noted meanwhile, and those of the files the
// commit left out, stay for the next commit.
func (s *Sync) forgetWrites(upTo uint64, left []Change) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = slices.DeleteFunc(s.writes, func(w write) bool {
		return w.seq <= upTo && !slices.ContainsFunc(left, w.names)
	})
}

// splitOutOfGit sets apart, from the writes' changes, what git sync took
// out of git's index itself (BUG-149): the folders .gitignore names
// (untrackIgnored) and the IN files too big to track (unstageBigIN). That
// is no one's write: it goes in the commit now, whoever holds its file, and
// in no writer's name. Left out, the index would take it back from HEAD,
// and git would track it from then on. A rename git reads from a file taken
// out to a new one is two changes, the file going and the new one. The
// caller holds workMu.
func (s *Sync) splitOutOfGit(changes []Change) (ours, theirs []Change) {
	for _, c := range changes {
		switch {
		case s.outOfGit(c.Path):
			ours = append(ours, c)
		case c.From != "" && s.outOfGit(c.From):
			ours = append(ours, Change{Path: c.From, Status: "deleted"})
			theirs = append(theirs, Change{Path: c.Path, Status: "added"})
		default:
			theirs = append(theirs, c)
		}
	}
	return ours, theirs
}

// outOfGit says whether git sync keeps rel out of git.
func (s *Sync) outOfGit(rel string) bool {
	return s.bigIN[rel] || under(s.untracked, rel) != ""
}

// under gives the folder of dirs that holds rel, or is rel; "" for none.
func under(dirs map[string]bool, rel string) string {
	for dir := range dirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return dir
		}
	}
	return ""
}

// ownChanges sets apart, from the changes no write names, what git sync
// changed itself (BUG-148): .gitignore, when refreshGitignore wrote it, and
// the files of a folder git tracks again. The caller holds workMu.
func (s *Sync) ownChanges(changes []Change, writes []write) (own, rest []Change) {
	for _, c := range changes {
		named := slices.ContainsFunc(writes, func(w write) bool { return w.names(c) })
		if !named && ((c.Path == ".gitignore" && s.ignoreChanged) || under(s.retracked, c.Path) != "") {
			own = append(own, c)
		} else {
			rest = append(rest, c)
		}
	}
	return own, rest
}

// syncLines says what git sync did to files, its own changes: .gitignore
// first, then the folders and files it took out of git, then the folders
// it tracks again. The caller holds workMu.
func (s *Sync) syncLines(files []Change) []string {
	var ignore bool
	stop, track := map[string]bool{}, map[string]bool{}
	for _, c := range files {
		switch {
		case c.Path == ".gitignore":
			ignore = true
		case s.bigIN[c.Path]:
			stop[c.Path+" (over "+strconv.Itoa(INMaxTrackedBytes>>20)+" MiB)"] = true
		case under(s.untracked, c.Path) != "":
			stop[under(s.untracked, c.Path)] = true
		case under(s.retracked, c.Path) != "":
			track[under(s.retracked, c.Path)] = true
		}
	}
	var lines []string
	if ignore {
		lines = append(lines, "update .gitignore")
	}
	for _, d := range slices.Sorted(maps.Keys(stop)) {
		lines = append(lines, "stop tracking "+d)
	}
	for _, d := range slices.Sorted(maps.Keys(track)) {
		lines = append(lines, "track "+d+" again")
	}
	return lines
}

// ownCommitted forgets git sync's own changes once a commit took them, or
// found none. The caller holds workMu.
func (s *Sync) ownCommitted() {
	s.ignoreChanged = false
	s.retracked = nil
}

// leaveOut splits the staged changes into those the commit takes and those
// it leaves for the next (BUG-147). A write's record comes from the audit
// log once its file is on disk, so a file may hold the bytes of a write
// not recorded yet: one whose lock a writer holds now, or one a write
// recorded since from names, as git may have read it before that write or
// after. The locks are asked first: a write that ends between the two
// checks is recorded by the second.
func (s *Sync) leaveOut(changes []Change, from uint64) (take, left []Change) {
	s.mu.Lock()
	busy := s.busy
	s.mu.Unlock()
	var free []Change
	for _, c := range changes {
		if busy != nil && (busy(c.Path) || (c.From != "" && busy(c.From))) {
			left = append(left, c)
		} else {
			free = append(free, c)
		}
	}
	since := s.writesSince(from)
	for _, c := range free {
		if slices.ContainsFunc(since, func(w write) bool { return w.names(c) }) {
			left = append(left, c)
		} else {
			take = append(take, c)
		}
	}
	return take, left
}

// authorName makes a git author's name of a writer: brackets and line ends
// would break the author line.
func authorName(by string) string {
	by = strings.NewReplacer("<", "", ">", "").Replace(by)
	return strings.Join(strings.Fields(by), " ")
}

// covers says whether w wrote the file at path: the note itself, the new
// path of a rename, a file of the project it renamed or deleted, or a file
// of the folder or project it deleted or brought back from the trash
// (BUG-148). No file sits under a note, so a note's delete covers it alone.
func (w write) covers(path string) bool {
	switch w.action {
	case "delete_project", "rename_project":
		return strings.HasPrefix(path, w.path+"/") || (w.to != "" && strings.HasPrefix(path, w.to+"/"))
	case "restore": // to is a commit
		return path == w.path
	case "delete", RestoreFromTrash: // to is a trash entry
		return path == w.path || strings.HasPrefix(path, w.path+"/")
	case "upload_attachment", "delete_attachment":
		return path == w.path
	}
	return path == w.path || (w.to != "" && path == w.to)
}

// names says whether w wrote c: its path, or the path it came from.
func (w write) names(c Change) bool {
	return w.covers(c.Path) || (c.From != "" && w.covers(c.From))
}

// line says w in a commit message: "update p/a.md", "rename a -> b",
// "delete project p", "restore p/a.md to 1a2b3c4", "restore p/f from the
// trash", "upload p/attachments/1a2b.png".
func (w write) line() string {
	switch w.action {
	case "rename":
		return "rename " + w.path + " -> " + w.to
	case "restore":
		return "restore " + w.path + " to " + w.to[:min(len(w.to), 7)]
	case RestoreFromTrash:
		if !strings.Contains(w.path, "/") {
			return "restore project " + w.path + " from the trash"
		}
		return "restore " + w.path + " from the trash"
	case "upload_attachment":
		return "upload " + w.path
	case "delete_attachment":
		return "delete attachment " + w.path
	case "delete_project":
		return "delete project " + w.path
	case "rename_project":
		return "rename project " + w.path + " -> " + w.to
	}
	return w.action + " " + w.path
}

// group is the files of one commit and the writes that name them; by is
// "" for the files no write names. sync says what git sync did, for the
// group of its own changes.
type group struct {
	by, project string
	files       []Change
	writes      []write
	sync        []string
}

// projectOf is the project of a vault path: its first folder, "" for a
// file at the root.
func projectOf(path string) string {
	project, _, ok := strings.Cut(path, "/")
	if !ok {
		return ""
	}
	return project
}

// groupByWriter splits the staged changes among their writers and the
// projects of the files: a file goes to the last write that covers it, or
// the path it came from, and the files no write covers to a last group
// without a writer. A commit names the notes of one project, so the history
// of a note never names a note of a project its reader may not see. The
// groups come in the order of their first write.
func groupByWriter(changes []Change, writes []write) []group {
	byWriter := map[string]*group{}
	var rest group
	for _, c := range changes {
		var w *write
		for i := len(writes) - 1; i >= 0; i-- {
			if writes[i].names(c) {
				w = &writes[i]
				break
			}
		}
		if w == nil {
			rest.files = append(rest.files, c)
			continue
		}
		key := w.by + "\x00" + projectOf(c.Path)
		g := byWriter[key]
		if g == nil {
			g = &group{by: w.by, project: projectOf(c.Path)}
			byWriter[key] = g
		}
		g.files = append(g.files, c)
	}
	var groups []group
	for _, g := range byWriter {
		for _, w := range writes {
			if w.by != g.by {
				continue
			}
			if slices.ContainsFunc(g.files, w.names) {
				g.writes = append(g.writes, w)
			}
		}
		groups = append(groups, *g)
	}
	slices.SortFunc(groups, func(a, b group) int {
		return cmp.Or(cmp.Compare(a.writes[0].seq, b.writes[0].seq), cmp.Compare(a.project, b.project))
	})
	if len(rest.files) > 0 {
		groups = append(groups, rest)
	}
	return groups
}

// message says what g did: one line per note, in the order of their first
// write, the subject the first line and how many more. A note created and
// then edited before the commit says create. The group of git sync's own
// changes says what it did, "git sync: stop tracking p". "" for the group
// without a writer, which takes the auto message.
func (g group) message() string {
	if len(g.sync) > 0 {
		return "git sync: " + say(g.sync)
	}
	var keys []string
	lines := map[string]write{}
	for _, w := range g.writes {
		prev, seen := lines[w.path]
		switch {
		case !seen:
			keys = append(keys, w.path)
		case prev.action == "create" && (w.action == "update" || w.action == "append"):
			continue
		}
		lines[w.path] = w
	}
	if len(keys) == 0 {
		return ""
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = lines[k].line()
	}
	return say(out)
}

// say makes a commit message of lines: the first one and how many more as
// the subject, and all of them in the body.
func say(lines []string) string {
	if len(lines) == 1 {
		return lines[0]
	}
	return lines[0] + " and " + strconv.Itoa(len(lines)-1) + " more\n\n" + strings.Join(lines, "\n")
}

// paths are the files git stages for g: both ends of a rename.
func (g group) paths() []string {
	var out []string
	for _, c := range g.files {
		if c.From != "" {
			out = append(out, c.From)
		}
		out = append(out, c.Path)
	}
	return out
}

// stage stages exactly paths, read as literal paths, as tree holds them: a
// path tree does not hold is staged as deleted. The "--" keeps a file
// named like the tree's id from being read as the tree.
func (s *Sync) stage(tree string, paths []string) error {
	c := exec.Command("git", "--literal-pathspecs", "reset", "-q", "--pathspec-from-file=-", "--pathspec-file-nul", tree, "--")
	c.Dir = s.vaultDir
	c.Stdin = strings.NewReader(strings.Join(paths, "\x00"))
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
