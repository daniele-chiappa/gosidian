package gitsync

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/clock"
)

// binaryExts are the attachment formats already compressed (attach.AllowedExt
// less the text ones): a delta between two of them saves next to nothing,
// and looking for one made every repack of a vault full of images cost
// seconds of CPU (IMP-209). On a copy of a 1.1 GB repository, 95% PNG, a
// repack went from 21.5 to 6.4 s of CPU, the pack 0.1% larger. svg, csv,
// json, txt and the notes keep their deltas.
var binaryExts = []string{"png", "jpg", "jpeg", "gif", "webp", "pdf", "zip", "docx", "xlsx"}

// DefaultMaintenance is the time of day of git's maintenance when [git]
// maintenance is unset.
const DefaultMaintenance = "03:30"

// maintenance is [git] maintenance: auto leaves it to git, which repacks
// after a commit, in the middle of the writes; off turns it off; a time
// runs it every day then, in the server's time zone.
type maintenance struct {
	auto, off    bool
	hour, minute int
}

// parseMaintenance reads a [git] maintenance value: auto, off or HH:MM
// (empty: DefaultMaintenance).
func parseMaintenance(v string) (maintenance, error) {
	v = strings.TrimSpace(v)
	switch v {
	case "":
		v = DefaultMaintenance
	case "auto":
		return maintenance{auto: true}, nil
	case "off":
		return maintenance{off: true}, nil
	}
	t, err := time.Parse("15:04", v)
	if err != nil {
		return maintenance{}, fmt.Errorf("git maintenance %q: a time of day HH:MM, auto or off", v)
	}
	return maintenance{hour: t.Hour(), minute: t.Minute()}, nil
}

// scheduled says whether gosidian runs the maintenance itself, at a time.
func (m maintenance) scheduled() bool { return !m.auto && !m.off }

// next is the first time of day of m after now, in now's location.
func (m maintenance) next(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), m.hour, m.minute, 0, 0, now.Location())
	if !t.After(now) {
		t = time.Date(now.Year(), now.Month(), now.Day()+1, m.hour, m.minute, 0, 0, now.Location())
	}
	return t
}

// The settings of the repository that turn git's own maintenance off: no
// gc --auto, and no maintenance run --auto after a commit.
var autoMaintenanceOff = [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}}

// ensureMaintenance writes the repository's settings for m. With auto it
// takes back only the values it wrote, so a setting of the operator's own
// stays.
func (s *Sync) ensureMaintenance(m maintenance) error {
	for _, kv := range autoMaintenanceOff {
		if !m.auto {
			if err := s.run("git", "config", "--local", kv[0], kv[1]); err != nil {
				return err
			}
			continue
		}
		cur, err := s.capture("git", "config", "--local", "--get", kv[0])
		if err != nil || strings.TrimSpace(cur) != kv[1] {
			continue // unset (exit 1), or the operator's
		}
		if err := s.run("git", "config", "--local", "--unset", kv[0]); err != nil {
			return err
		}
	}
	return nil
}

// refreshAttributes writes the managed block of the repository's
// info/attributes: -delta on the compressed formats (binaryExts), in any
// case. The file is the repository's own, not the vault's: it is not
// committed and does not reach the remote. Lines outside the markers stay.
func (s *Sync) refreshAttributes() error {
	out, err := s.capture("git", "rev-parse", "--git-path", "info/attributes")
	if err != nil {
		return err
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.vaultDir, path)
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := []string{
		gitignoreManagedBegin,
		"# Managed by gosidian — do not edit between these markers.",
		"# Formats already compressed: no delta search when git packs them.",
	}
	for _, ext := range binaryExts {
		lines = append(lines, "*."+anyCase(ext)+" -delta")
	}
	lines = append(lines, gitignoreManagedEnd)
	combined := strings.Join(lines, "\n") + "\n"
	if user := strings.TrimSpace(stripManagedBlock(string(old))); user != "" {
		combined += "\n" + user + "\n"
	}
	if combined == string(old) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(combined), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// anyCase is a glob matching ext in any case: png → [pP][nN][gG].
func anyCase(ext string) string {
	var b strings.Builder
	for _, r := range ext {
		lo, up := strings.ToLower(string(r)), strings.ToUpper(string(r))
		if lo == up {
			b.WriteRune(r)
			continue
		}
		b.WriteString("[" + lo + up + "]")
	}
	return b.String()
}

// maintain runs git's maintenance every day at m's time until ctx ends.
func (s *Sync) maintain(ctx context.Context, m maintenance) {
	for {
		t := time.NewTimer(time.Until(m.next(clock.Now())))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		s.runMaintenance()
	}
}

// runMaintenance packs the repository (git gc) while no commit runs.
func (s *Sync) runMaintenance() {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	start := time.Now()
	if err := s.run("git", "gc", "--quiet"); err != nil {
		log.Printf("gitsync: maintenance: %v", err)
		return
	}
	log.Printf("gitsync: maintenance done in %s", time.Since(start).Round(time.Millisecond))
}
