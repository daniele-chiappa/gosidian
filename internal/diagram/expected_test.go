package diagram

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// expected.json beside the examples says what this reader makes of each of
// them: the problems with their lines, and the text of the valid ones.
// plancia-diagrams' reader is checked against the same file, so the two
// readers give the same messages on the same lines, not only the problem
// the first line of an example names. UPDATE_EXPECTED=1 rewrites it.
type expectedProblem struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type expectedExample struct {
	Problems []expectedProblem `json:"problems"`
	Text     string            `json:"text,omitempty"`
}

func TestExamplesExpected(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "diagrams", "examples")
	got := map[string]expectedExample{}
	for _, dir := range []string{"valid", "invalid"} {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*.yaml"))
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			d, problems := Parse(string(src))
			ex := expectedExample{Problems: []expectedProblem{}, Text: Text(d)}
			for _, p := range problems {
				ex.Problems = append(ex.Problems, expectedProblem{p.Line, p.Msg})
			}
			// Some checks run over a map, in no fixed order.
			sort.Slice(ex.Problems, func(i, j int) bool {
				a, b := ex.Problems[i], ex.Problems[j]
				return a.Line < b.Line || a.Line == b.Line && a.Message < b.Message
			})
			got[dir+"/"+filepath.Base(f)] = ex
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // "a -> b" as written
	enc.SetIndent("", "  ")
	if err := enc.Encode(got); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	path := filepath.Join(root, "expected.json")
	if os.Getenv("UPDATE_EXPECTED") != "" {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(want, data) {
		t.Errorf("%s is not what the reader says: UPDATE_EXPECTED=1 go test ./internal/diagram rewrites it (%v)", path, err)
	}
}
