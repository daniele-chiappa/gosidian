package gitsync

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNoVersion says a sha is no commit of the history of the note.
var ErrNoVersion = errors.New("no such version of the note")

// Commit summarizes one entry from `git log` for a single file.
type Commit struct {
	SHA      string
	ShortSHA string
	Author   string
	Date     time.Time
	Subject  string
	// Path is the note's path in this commit: a rename gave it another
	// name before (IMP-222). From is the name before, in the commit that
	// renamed it. Status is added, modified, renamed or deleted; "" for a
	// merge, which changes no file of its own.
	Path, From, Status string
}

// History runs `git log --follow` on the given vault-relative path and
// returns up to limit entries, newest first. Suitable for the per-note
// history page.
func (s *Sync) History(relPath string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.history(relPath, limit)
}

// history is History with limit 0 for every commit, and more options of
// git log.
func (s *Sync) history(relPath string, limit int, more ...string) ([]Commit, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	args := []string{
		"--literal-pathspecs",
		"log",
		"--follow",
		"--no-color",
		"-z",
		"--name-status",
		"--format=%x1e%H%x09%an <%ae>%x09%aI%x09%s",
	}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	args = append(args, more...)
	out, err := s.capture("git", append(args, "--", relPath)...)
	if err != nil {
		return nil, err
	}
	// Each commit is \x1e, its line up to a NUL, then its change of the
	// file as `--name-status -z` writes it. Newest first: a rename's
	// older commits have the name it came from.
	var commits []Commit
	path := relPath
	for _, rec := range strings.Split(out, "\x1e") {
		line, changes, ok := strings.Cut(rec, "\x00")
		if !ok {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 || len(parts[0]) < 7 {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, parts[2])
		c := Commit{
			SHA:      parts[0],
			ShortSHA: parts[0][:7],
			Author:   parts[1],
			Date:     ts,
			Subject:  parts[3],
			Path:     path,
		}
		if ch := parseNameStatus(strings.TrimLeft(changes, "\n")); len(ch) > 0 {
			c.Path, c.From, c.Status = ch[0].Path, ch[0].From, ch[0].Status
			if c.From != "" {
				path = c.From
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// Version is a note as one commit of its history holds it.
type Version struct {
	Commit
	// Content is the note's text then; nil when the commit deleted it, or
	// when it is larger than the max of Version (Size says how large).
	Content []byte
	Size    int64
}

// Version reads the note at relPath as the commit sha of its history held
// it, under the name it had then: sha is a full hash or at least 7 of its
// first hex digits. A sha that is no commit of that history is
// ErrNoVersion. A text over max bytes is left out.
func (s *Sync) Version(relPath, sha string, max int64) (Version, error) {
	if !s.cfg.Enabled {
		return Version{}, ErrDisabled
	}
	sha = strings.ToLower(sha)
	if len(sha) < 7 || len(sha) > 64 || strings.Trim(sha, "0123456789abcdef") != "" {
		return Version{}, ErrNoVersion
	}
	// The commit's date bounds the walk, the history from now back to it
	// with a day more for a clock that was off: on 4,820 commits, 5 ms for
	// a version of yesterday where the whole history of a note took 155.
	// An unknown or ambiguous sha has no date.
	date, err := s.capture("git", "show", "-s", "--format=%ct", sha+"^{commit}", "--")
	if err != nil {
		return Version{}, ErrNoVersion
	}
	ct, err := strconv.ParseInt(strings.TrimSpace(date), 10, 64)
	if err != nil {
		return Version{}, ErrNoVersion
	}
	commits, err := s.history(relPath, 0, "--since=@"+strconv.FormatInt(ct-24*60*60, 10))
	if err != nil {
		return Version{}, err
	}
	for _, c := range commits {
		if strings.HasPrefix(c.SHA, sha) {
			return s.readVersion(c, max)
		}
	}
	return Version{}, ErrNoVersion
}

// readVersion reads the note as commit c holds it.
func (s *Sync) readVersion(c Commit, max int64) (Version, error) {
	v := Version{Commit: c}
	if c.Status == "deleted" {
		return v, nil
	}
	object := c.SHA + ":" + c.Path
	size, err := s.capture("git", "cat-file", "-s", object)
	if err != nil {
		return Version{}, err
	}
	if v.Size, err = strconv.ParseInt(strings.TrimSpace(size), 10, 64); err != nil {
		return Version{}, err
	}
	if v.Size > max {
		return v, nil
	}
	content, err := s.capture("git", "cat-file", "blob", object)
	if err != nil {
		return Version{}, err
	}
	v.Content = []byte(content)
	return v, nil
}

// Diff is the unified diff from old to new, as git makes it, its hunks
// only: "" when the two are the same. Both are written to a temporary
// directory for `git diff --no-index`.
func Diff(old, new []byte) (string, error) {
	dir, err := os.MkdirTemp("", "gosidian-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "a"), old, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), new, 0o600); err != nil {
		return "", err
	}
	c := exec.Command("git", "diff", "--no-index", "--no-color", "--no-ext-diff", "--text", "-U3", "--", "a", "b")
	c.Dir = dir
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err = c.Run()
	// Exit status 1 is a difference found, not a failure.
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	out := stdout.String()
	// The header names the temporary files: the hunks start at the first
	// line with @@, which no line of the text can start with (each has its
	// mark, a space, + or -).
	if out == "" {
		return "", nil
	}
	if strings.HasPrefix(out, "@@") {
		return out, nil
	}
	if i := strings.Index(out, "\n@@"); i >= 0 {
		return out[i+1:], nil
	}
	return "", nil
}
