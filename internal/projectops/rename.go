// Package projectops holds the operations on a whole project that span the
// vault, the access store and the MCP tokens, so every entry point (REST,
// MCP, account archiving) does them the same way.
package projectops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

// ErrInvalid wraps an operation refused by its checks, before any change: a
// bad name, a missing project, a taken target, tokens scoped to the name.
var ErrInvalid = errors.New("refused")

// ErrIncomplete wraps a failure that came after the project folder moved
// (renamed, deleted): the operation stands, and the error says what did not
// follow (the index, the access entry or the MCP token scopes).
var ErrIncomplete = errors.New("the project folder changed, but not everything followed")

// projectMu serialises the operations that decide who a project name
// belongs to: Create, Rename, Delete and RestoreProject. Two of them on the
// same name would both pass their checks: a create landing between a
// rename's access move and its folder move gave the creator's grant to the
// source project on rollback (IMP-124).
var projectMu sync.Mutex

// Rename renames project from to to everywhere it is known: its flags and
// member and team grants (projects.Store), its folder and index entries
// (vault.RenameProject), and the scope of the MCP tokens naming it, which
// follow the project instead of losing it or reaching a later project that
// takes the old name (IMP-123). The MCP rename used to move the folder only,
// and a private project lost its visibility and grants (BUG-078).
//
// Every check comes before any change: valid names, an existing source, a
// free target, and no MCP token already scoped to the target name, which
// the renamed project would otherwise fall to. The access entry moves before
// the folder, and moves back if the folder does not move. ps and tokens may
// be nil. Returns how many tokens changed.
func Rename(v *vault.Vault, idx *index.Index, ps *projects.Store, tokens *auth.Store, from, to string) (int, error) {
	projectMu.Lock()
	defer projectMu.Unlock()
	from, err := v.CheckProject(from)
	if err != nil {
		return 0, fmt.Errorf("%w: source name: %v", ErrInvalid, err)
	}
	to, err = v.CheckProject(to)
	if err != nil {
		return 0, fmt.Errorf("%w: target name: %v", ErrInvalid, err)
	}
	if from == to {
		return 0, nil
	}
	if idx == nil {
		return 0, errors.New("index not configured")
	}
	if !folderExists(v, from) {
		return 0, fmt.Errorf("%w: project %q does not exist", ErrInvalid, from)
	}
	if folderExists(v, to) {
		return 0, fmt.Errorf("%w: target %q already exists", ErrInvalid, to)
	}
	if err := refuseScopedName(tokens, to); err != nil {
		return 0, err
	}
	var since int64
	if ps != nil {
		since = ps.Get(from).Since
		if err := ps.Rename(from, to); err != nil {
			return 0, err
		}
	}
	if err := v.RenameProject(idx, from, to); err != nil {
		if folderExists(v, from) {
			if ps != nil {
				// Moving the entry back stamps from as just named; it was
				// not, so its Since goes back too.
				if ps.Rename(to, from) == nil {
					f := ps.Get(from)
					f.Since = since
					_ = ps.Set(from, f)
				}
			}
			return 0, err
		}
		// The folder moved but the index did not fully follow: the rename
		// stands, so the token scopes follow it too.
		n, _ := renameScopes(tokens, from, to)
		return n, fmt.Errorf("%w: index: %v", ErrIncomplete, err)
	}
	n, err := renameScopes(tokens, from, to)
	if err != nil {
		return n, fmt.Errorf("%w: MCP token scopes: %v", ErrIncomplete, err)
	}
	return n, nil
}

func renameScopes(tokens *auth.Store, from, to string) (int, error) {
	if tokens == nil {
		return 0, nil
	}
	return tokens.RenameProject(from, to)
}

// scopedTokens counts the unexpired MCP tokens whose scope names project.
func scopedTokens(tokens *auth.Store, project string) int {
	if tokens == nil {
		return 0
	}
	n := 0
	for _, t := range tokens.List() {
		if !t.Expired() && slices.Contains(t.ProjectList(), project) {
			n++
		}
	}
	return n
}

// folderExists reports whether the project folder name (already checked by
// Vault.CheckProject) is on disk, whatever else fails.
func folderExists(v *vault.Vault, name string) bool {
	st, err := os.Stat(filepath.Join(v.Root, name))
	return err == nil && st.IsDir()
}

// Exists reports whether the vault has a top-level project name.
func Exists(v *vault.Vault, name string) bool {
	projs, err := v.Projects()
	if err != nil {
		return false
	}
	for _, p := range projs {
		if p.Name == name {
			return true
		}
	}
	return false
}
