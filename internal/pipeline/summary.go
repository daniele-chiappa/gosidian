package pipeline

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// What a run leaves in the vault besides its results (ADR-035): one line
// a day in a note beside the pipeline, `<name>-runs.md`, with how many
// runs ended, how many failed and the last one. The detail stays in the
// jobs database, shown by the web UI.

// RunsNote is the path of a pipeline's runs note.
func RunsNote(pipelinePath string) string {
	return strings.TrimSuffix(pipelinePath, ".md") + "-runs.md"
}

var dayLineRe = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2}): (\d+) runs?, (\d+) failed`)

func (e *Engine) summarize(run jobs.Run) error {
	day := run.Finished
	if day.IsZero() {
		day = e.now()
	}
	day = day.In(e.cfg.Location)
	date := day.Format("2006-01-02")
	rel := RunsNote(run.Ref)
	var lines []string
	runs, failed := 0, 0
	note, err := e.host.ReadNote(run.Project, rel)
	switch {
	case err == nil:
		for _, l := range strings.Split(note.Body, "\n") {
			if m := dayLineRe.FindStringSubmatch(l); m != nil {
				if m[1] == date {
					runs, _ = strconv.Atoi(m[2])
					failed, _ = strconv.Atoi(m[3])
					continue
				}
				lines = append(lines, l)
			}
		}
	case errors.Is(err, ErrNoNote):
	default:
		return err
	}
	runs++
	if run.Status == jobs.Failed {
		failed++
	}
	last := fmt.Sprintf("last at %s, %s", day.Format("15:04"), run.Status)
	if run.Error != "" {
		last += ": " + oneLine(run.Error, 120)
	}
	plural := "s"
	if runs == 1 {
		plural = ""
	}
	today := fmt.Sprintf("- %s: %d run%s, %d failed (%s)", date, runs, plural, failed, last)
	// Newest day first.
	lines = append([]string{today}, lines...)
	name := strings.TrimSuffix(run.Ref[strings.LastIndex(run.Ref, "/")+1:], ".md")
	body := "# Runs of " + name + "\n\nOne line a day, written by the pipeline when a run ends. The runs themselves are in the web UI (Runs).\n\n" + strings.Join(lines, "\n") + "\n"
	fm := map[string]any{
		"title":    "Runs of " + name,
		"type":     "pipeline-runs",
		"pipeline": "[[" + strings.TrimSuffix(run.Ref, ".md") + "]]",
		"updated":  day.Format(time.DateOnly),
	}
	content, err := withFrontmatter(fm, body)
	if err != nil {
		return err
	}
	_, err = e.host.WriteNote(run.Project, rel, content, WriteUpsert, Actor(run.Ref))
	return err
}

// ErrNoNote is what Host.ReadNote returns for a note that does not exist.
var ErrNoNote = errors.New("no such note")

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}
