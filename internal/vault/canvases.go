package vault

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Obsidian canvases (IMP-144): a .canvas file is the JSON of cards laid out
// on a plane (JSON Canvas 1.0). Like a base it is no note: gosidian reads
// it, shows it read-only (package canvas) and never writes it, so Save,
// Delete and Move keep refusing it (ADR-021). It stays out of the index: a
// note does not know a canvas holds it.

// MaxCanvasBytes caps the .canvas file read.
const MaxCanvasBytes = 4 << 20

// ErrNotCanvas is returned by LoadCanvas for a path that is not a .canvas
// file, or one over MaxCanvasBytes.
var ErrNotCanvas = fmt.Errorf("not an Obsidian canvas")

// IsCanvasFile reports whether name (a path or basename) is an Obsidian
// canvas.
func IsCanvasFile(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".canvas")
}

// LoadCanvas reads the .canvas file at rel: Content holds its JSON as
// written.
func (v *Vault) LoadCanvas(rel string) (*Note, error) {
	return v.loadReadOnly(rel, IsCanvasFile, MaxCanvasBytes, ErrNotCanvas)
}

// ListCanvases returns the paths of the .canvas files of the vault, or of
// the project when one is given, as ListBases does.
func (v *Vault) ListCanvases(project string) ([]string, error) {
	return v.listFiles(project, IsCanvasFile)
}
