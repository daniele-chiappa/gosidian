package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// GlobalPlaceholder is replaced, in a skill gosidian ships, with the name of
// the public global project that receives it: its project tag.
const GlobalPlaceholder = "{{GLOBAL}}"

// SeedSkillsInto copies every skill note (.md) of the embedded FS under
// embedRoot into targetDir when a note of that name is absent there, with
// GlobalPlaceholder replaced by project (IMP-210). A note already there is
// never touched, so an owner's edits survive the upgrades. Returns the file
// names seeded on this call.
func SeedSkillsInto(targetDir string, efs fs.FS, embedRoot, project string) ([]string, error) {
	entries, err := fs.ReadDir(efs, embedRoot)
	if err != nil {
		return nil, fmt.Errorf("read embed root %q: %w", embedRoot, err)
	}
	var seeded []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		target := filepath.Join(targetDir, e.Name())
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return seeded, fmt.Errorf("stat %s: %w", target, err)
		}
		body, err := fs.ReadFile(efs, path.Join(embedRoot, e.Name()))
		if err != nil {
			return seeded, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return seeded, fmt.Errorf("mkdir skills dir: %w", err)
		}
		out := strings.ReplaceAll(string(body), GlobalPlaceholder, project)
		if err := os.WriteFile(target, []byte(out), 0o644); err != nil {
			return seeded, fmt.Errorf("write %s: %w", target, err)
		}
		seeded = append(seeded, e.Name())
	}
	return seeded, nil
}
