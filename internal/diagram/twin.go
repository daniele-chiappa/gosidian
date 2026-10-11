package diagram

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
)

// A diagram may keep a twin for apps that draw Mermaid and not this format
// (Obsidian): a ```mermaid block right after the diagram's, nothing but
// blank lines between, whose first line is TwinMark and the Hash of the
// YAML it was made from. The web UI's editor writes it on request and
// again at every save (plancia-diagrams' writeDiagramBlock); the server
// only checks that it still matches its diagram.

// TwinMark starts the first line of a twin, before the hash.
const TwinMark = "%% diagram-twin"

var twinRe = regexp.MustCompile(`^%% diagram-twin ([0-9a-f]{8})\b`)

// Hash is the hash of a diagram's YAML a twin's mark holds: FNV-1a, 32
// bits, over the YAML with its line ends made \n and its trailing ASCII
// white space left out, as 8 hex digits. plancia-diagrams' diagramHash
// makes the same one.
func Hash(src string) string {
	s := strings.ReplaceAll(src, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimRight(s, " \t\n\r\f\v")
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// TwinState is how a diagram's twin stands: "current" when it was made
// from the diagram as it is, "stale" when the diagram changed since, ""
// when the diagram has none.
type TwinState string

const (
	TwinCurrent TwinState = "current"
	TwinStale   TwinState = "stale"
)

// Twin is a twin of a note: its line, and the diagram's line (0 for a twin
// with no ```diagram block right above it).
type Twin struct {
	Line        int
	DiagramLine int
	State       TwinState
}

// Twins returns the twins of a note's body, in order: each marked
// ```mermaid block, with the state against the diagram right above it.
func Twins(body []byte) []Twin {
	mermaid := parser.FindFenced(body, "mermaid")
	if len(mermaid) == 0 {
		return nil
	}
	diagrams := parser.FindFenced(body, Lang)
	lineOf := func(off int) int { return strings.Count(string(body[:off]), "\n") + 1 }
	var out []Twin
	for _, m := range mermaid {
		mark := twinRe.FindStringSubmatch(m.Spec)
		if mark == nil {
			continue
		}
		t := Twin{Line: lineOf(m.Start)}
		for _, d := range diagrams {
			if d.End <= m.Start && strings.TrimSpace(string(body[d.End:m.Start])) == "" {
				t.DiagramLine = lineOf(d.Start)
				t.State = TwinStale
				if Hash(d.Spec) == mark[1] {
					t.State = TwinCurrent
				}
			}
		}
		out = append(out, t)
	}
	return out
}
