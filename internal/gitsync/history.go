package gitsync

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// Commit summarizes one entry from `git log` for a single file.
type Commit struct {
	SHA      string
	ShortSHA string
	Author   string
	Date     time.Time
	Subject  string
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
		"-n", strconv.Itoa(limit),
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
