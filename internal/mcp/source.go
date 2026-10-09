package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
)

// bridgePath resolves a bridge_filename to its path in the bridge dir. Only
// the last element of the name counts, as before, so an agent passing the
// whole path of the staged file still works; "." and ".." are refused
// (BUG-102): filepath.Base keeps them, and a package import of "." read the
// whole bridge dir and then removed it, ".." its parent.
func (s *Server) bridgePath(name string) (string, error) {
	if s.bridgeDir == "" {
		return "", errors.New("bridge_filename given but no bridge dir is configured (set GOSIDIAN_MCP_BRIDGE_DIR)")
	}
	base := filepath.Base(name)
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("bridge_filename %q does not name a file or folder in the bridge dir", name)
	}
	return filepath.Join(s.bridgeDir, base), nil
}

// checkSource checks a source_path: absolute, inside an allowed root once
// symbolic links are resolved, and, where it resolves inside the vault, a
// path the token may read (BUG-103). The allowed roots always hold the
// vault, so without that a token scoped to one project imported another,
// private or hidden from MCP, or the trash, into its own and read it there.
// A vault path must be one the vault addresses (no hidden folder, no state
// dir), in the token's scope, outside the projects hidden from MCP, and not
// hold the state dir; a source holding the whole vault is refused. A bridge
// dir or an upload root the operator put inside the vault is a staging
// place, not vault content: what is in it passes, but for the state dir.
// Returns the resolved path, the one to read: reading p again would follow
// a link swapped in the meantime.
func (s *Server) checkSource(ctx context.Context, p string) (string, error) {
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		return "", errors.New("source_path must be absolute")
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("source_path %q does not exist on the server. %s", p, attach.RemoteSetupHint)
		}
		return "", fmt.Errorf("source_path: %w", err)
	}
	vaultRoot := resolved(s.vault.Root)
	inRoot, staged := false, false
	for _, root := range s.effectiveUploadRoots() {
		r := resolved(root)
		if _, ok := relWithin(r, real); !ok {
			continue
		}
		inRoot = true
		if rel, in := relWithin(vaultRoot, r); in && rel != "." {
			staged = true
		}
	}
	if !inRoot {
		return "", fmt.Errorf("source_path %q is not inside the vault, the bridge dir or an allowed upload root (GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS). %s", p, attach.RemoteSetupHint)
	}
	if _, ok := relWithin(real, vaultRoot); ok {
		return "", fmt.Errorf("source_path %q holds the whole vault", p)
	}
	rel, ok := relWithin(vaultRoot, real)
	if !ok {
		return real, nil
	}
	refused := fmt.Errorf("source_path %q is a vault path this token may not read", p)
	if s.vault.InStateDir(rel) || s.vault.HoldsStateDir(rel) {
		return "", refused
	}
	if staged {
		return real, nil
	}
	tok := s.tokenFromContext(ctx)
	vrel, err := s.vault.Rel(rel)
	if err != nil || tok == nil || !tok.AllowsPath(vrel) || s.pathInHiddenProject(vrel) {
		return "", refused
	}
	return real, nil
}

// checkSourceFile is checkSource for a single file. It is the one check
// of a file's source_path: the ingestion of a note checked it first
// against the roots without resolving the links too (IMP-161).
func (s *Server) checkSourceFile(ctx context.Context, p string) (string, error) {
	real, err := s.checkSource(ctx, p)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(real); err == nil && fi.IsDir() {
		return "", errors.New("source_path is a directory, not a file")
	}
	return real, nil
}

// sourceRoots is effectiveUploadRoots with each root also resolved, for
// the checks that compare a path checkSource resolved with the roots.
func (s *Server) sourceRoots() []string {
	roots := s.effectiveUploadRoots()
	out := append([]string(nil), roots...)
	for _, r := range roots {
		if rr := resolved(r); rr != filepath.Clean(r) {
			out = append(out, rr)
		}
	}
	return out
}

// resolved returns p with its symbolic links resolved, or cleaned when it
// cannot be resolved.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// relWithin returns the slash path of p relative to root when p is root or
// lies under it ("." for root itself).
func relWithin(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
