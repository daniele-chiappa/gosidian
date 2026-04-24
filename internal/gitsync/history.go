package gitsync

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Commit summarizes one entry from `git log` for a single file.
type Commit struct {
	SHA     string
	ShortSHA string
	Author  string
	Date    time.Time
	Subject string
}

// History runs `git log --follow` on the given vault-relative path and
// returns up to limit entries, newest first. Suitable for the per-note
// history page.
func (s *Sync) History(relPath string, limit int) ([]Commit, error) {
	if !s.cfg.Enabled {
		return nil, errors.New("git sync disabled")
	}
	if limit <= 0 {
		limit = 50
	}
	args := []string{
		"log",
		"--follow",
		"--no-color",
		"--format=%H%x09%an <%ae>%x09%aI%x09%s",
		"-n", iToA(limit),
		"--",
		relPath,
	}
	out, err := s.capture("git", args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, parts[2])
		commits = append(commits, Commit{
			SHA:      parts[0],
			ShortSHA: parts[0][:7],
			Author:   parts[1],
			Date:     ts,
			Subject:  parts[3],
		})
	}
	return commits, nil
}

// Show returns the unified diff of a single commit for a single file.
// If sha is empty it returns the latest content for the file via `git show
// HEAD:<path>`. Suitable for the diff pane on the history page.
func (s *Sync) Show(relPath, sha string) (string, error) {
	if !s.cfg.Enabled {
		return "", errors.New("git sync disabled")
	}
	if sha == "" {
		return "", errors.New("sha required")
	}
	out, err := s.capture("git", "show", "--no-color", sha, "--", relPath)
	return out, err
}

// Restore writes the historical version of a file back to the working
// tree (without committing it). The caller can then commit through the
// normal debounce path. Returns the bytes that were written.
func (s *Sync) Restore(relPath, sha string) ([]byte, error) {
	if !s.cfg.Enabled {
		return nil, errors.New("git sync disabled")
	}
	if sha == "" {
		return nil, errors.New("sha required")
	}
	out, err := s.captureRaw("git", "show", sha+":"+relPath)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// captureRaw is like capture but returns the raw bytes (used by Restore so
// binary content isn't re-encoded as a Go string).
func (s *Sync) captureRaw(cmd string, args ...string) ([]byte, error) {
	c := exec.Command(cmd, args...)
	c.Dir = s.vaultDir
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, err
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
}

func iToA(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
