package projectops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
)

// ErrExists wraps a create or restore refused because a project already
// has the name.
var ErrExists = errors.New("project already exists")

// Create makes project name with a fresh access entry: visibility vis (the
// store default when empty) and, when adminUserID is set, that account as
// admin. An entry the name still holds from a project that is gone is
// replaced, not inherited, and a name MCP tokens are scoped to is refused:
// the new project would fall into their scope (IMP-124). The entry is saved
// before the folder appears, so a private project is never visible under
// the default visibility, and it records when the project began
// (Flags.Since): notes trashed from an earlier project with the name stay
// the owner's. ps and tokens may be nil. Returns the clean name.
func Create(v *vault.Vault, ps *projects.Store, tokens *auth.Store, name, vis, adminUserID string) (string, error) {
	projectMu.Lock()
	defer projectMu.Unlock()
	clean, err := v.CheckProject(name)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, clean)); err == nil {
		return "", fmt.Errorf("%w: %q", ErrExists, clean)
	}
	if err := refuseScopedName(tokens, clean); err != nil {
		return "", err
	}
	if ps != nil {
		if vis == "" {
			vis = ps.DefaultVisibility()
		}
		a := projects.Access{Flags: projects.Flags{Visibility: vis, Since: time.Now().UnixNano()}}
		if adminUserID != "" {
			a.Members = []projects.ProjectMember{{UserID: adminUserID, Level: projects.LevelAdmin}}
		}
		if err := ps.SetAccess(clean, a); err != nil {
			return "", fmt.Errorf("project access: %w", err)
		}
	}
	if _, err := v.CreateProject(clean); err != nil {
		// A folder that appeared meanwhile (a note written into it) keeps
		// the fresh entry rather than none, which would mean the default,
		// but not the admin grant: the caller did not make that folder.
		if folderExists(v, clean) {
			if ps != nil && adminUserID != "" {
				_ = ps.RemoveMember(clean, adminUserID)
			}
			return "", fmt.Errorf("%w: %q", ErrExists, clean)
		}
		if ps != nil {
			_ = ps.Delete(clean)
		}
		return "", err
	}
	return clean, nil
}

// FolderExists reports whether the vault has a top-level folder name: a
// stat, where Exists lists every project.
func FolderExists(v *vault.Vault, name string) bool { return folderExists(v, name) }

// DeleteResult says what Delete did.
type DeleteResult struct {
	Name           string
	TrashID        string   // empty when there is no trash and the folder was removed
	Removed        []string // note paths taken out of the index
	TokensRevoked  int
	TokensNarrowed int
}

// Delete removes project name: into the trash when bin is set, with its
// access entry stored alongside for RestoreProject, otherwise from disk.
// Then its index entries, its access entry and its place in the MCP token
// scopes go: a token scoped to it alone is revoked, one scoped to several
// loses it (auth.Store.RemoveProject), so none reaches a later project
// with the name (IMP-124). Once the folder has moved, a later failure
// returns ErrIncomplete: the delete stands, and Create still refuses a
// name tokens are scoped to and replaces a leftover entry. idx, ps, tokens
// and bin may be nil.
func Delete(v *vault.Vault, idx *index.Index, ps *projects.Store, tokens *auth.Store, bin *trash.Bin, name string) (DeleteResult, error) {
	projectMu.Lock()
	defer projectMu.Unlock()
	clean, err := v.CheckExistingProject(name)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if !folderExists(v, clean) {
		return DeleteResult{}, fmt.Errorf("%w: project %q does not exist", ErrInvalid, clean)
	}
	res := DeleteResult{Name: clean}
	if bin != nil {
		var meta []byte
		if ps != nil {
			if meta, err = json.Marshal(ps.Access(clean)); err != nil {
				return DeleteResult{}, err
			}
		}
		res.TrashID, res.Removed, err = bin.DiscardProject(clean, meta)
	} else {
		res.Removed, err = v.DeleteProject(clean)
	}
	if err != nil {
		return DeleteResult{}, err
	}
	var failed []string
	if idx != nil {
		// Every note, even after a failure: an indexed note of a project
		// whose access entry is gone is searched under the default.
		var idxErr error
		for _, p := range res.Removed {
			if err := idx.Delete(p); err != nil && idxErr == nil {
				idxErr = err
			}
		}
		if idxErr != nil {
			failed = append(failed, "index: "+idxErr.Error())
		}
	}
	if ps != nil {
		if err := ps.Delete(clean); err != nil {
			failed = append(failed, "access entry: "+err.Error())
		}
	}
	if tokens != nil {
		if res.TokensRevoked, res.TokensNarrowed, err = tokens.RemoveProject(clean); err != nil {
			failed = append(failed, "MCP token scopes: "+err.Error())
		}
	}
	if len(failed) > 0 {
		return res, fmt.Errorf("%w: %s", ErrIncomplete, strings.Join(failed, "; "))
	}
	return res, nil
}

// TrashedAccess returns the access entry stored with a trashed project, and
// false when there is none: a note, or a project trashed before IMP-124.
// Callers decide with it who may see, restore or purge the entry.
func TrashedAccess(bin *trash.Bin, id string) (projects.Access, bool, error) {
	meta, err := bin.ProjectMeta(id)
	if err != nil || meta == nil {
		return projects.Access{}, false, err
	}
	var a projects.Access
	if err := json.Unmarshal(meta, &a); err != nil {
		return projects.Access{}, false, fmt.Errorf("trashed project access: %w", err)
	}
	return a, true, nil
}

// RestoreResult says what RestoreProject did.
type RestoreResult struct {
	Name     string
	Restored []string // note paths back in the vault and the index
	// AccessRestored is false when the entry carried no access: the project
	// came back private, with the restoring account as admin.
	AccessRestored bool
}

// RestoreProject brings trashed project id back with the access it had when
// it was deleted. One trashed without it comes back private, with
// restorerID as admin (when set): never under the default visibility,
// which exposed a private project to every account. As for Create, a name
// MCP tokens are scoped to is refused, and the access is in place before
// the folder returns. The restored notes are indexed. idx, ps and tokens
// may be nil.
func RestoreProject(v *vault.Vault, idx *index.Index, ps *projects.Store, tokens *auth.Store, bin *trash.Bin, id, restorerID string) (RestoreResult, error) {
	projectMu.Lock()
	defer projectMu.Unlock()
	clean, err := v.CheckExistingProject(trash.Origin(id))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := os.Stat(filepath.Join(v.Root, clean)); err == nil {
		return RestoreResult{}, fmt.Errorf("%w: %q", ErrExists, clean)
	}
	if err := refuseScopedName(tokens, clean); err != nil {
		return RestoreResult{}, err
	}
	res := RestoreResult{Name: clean}
	// An unreadable saved access counts as none: the project comes back
	// private rather than stuck (only the owner reaches such an entry).
	a, ok, err := TrashedAccess(bin, id)
	res.AccessRestored = ok && err == nil
	if !res.AccessRestored {
		a = projects.Access{Flags: projects.Flags{Visibility: projects.VisibilityPrivate, Since: time.Now().UnixNano()}}
		if restorerID != "" {
			a.Members = []projects.ProjectMember{{UserID: restorerID, Level: projects.LevelAdmin}}
		}
	}
	if a.Flags.Visibility == "" {
		a.Flags.Visibility = projects.VisibilityPrivate
	}
	if laterNotesUnder(bin, id, clean) {
		// While this project was in the trash another one took the name
		// and trashed notes under it: they are not this project's, so the
		// project counts as named now. Its own earlier trashed notes then
		// become the owner's too, which errs on the closed side.
		a.Flags.Since = time.Now().UnixNano()
	}
	if ps != nil {
		if err := ps.SetAccess(clean, a); err != nil {
			return RestoreResult{}, fmt.Errorf("project access: %w", err)
		}
	}
	res.Restored, _, err = bin.Restore(id)
	if err != nil {
		if ps != nil {
			_ = ps.Delete(clean)
		}
		return RestoreResult{}, err
	}
	if idx != nil {
		for _, p := range res.Restored {
			note, err := v.Load(p)
			if err != nil {
				continue
			}
			_ = idx.Upsert(index.NoteDoc{Path: note.Path, Title: note.Title, Body: string(note.Content), ModTime: note.ModTime.Unix(), Size: note.Size})
		}
	}
	return res, nil
}

// laterNotesUnder reports whether the trash holds notes (or folders)
// discarded under project name after the project entry id was: only another
// project with the name can have trashed them. An unreadable trash counts
// as yes.
func laterNotesUnder(bin *trash.Bin, id, name string) bool {
	entries, err := bin.List()
	if err != nil {
		return true
	}
	var deletedAt time.Time
	for _, e := range entries {
		if e.ID == id {
			deletedAt = e.DiscardedAt
		}
	}
	for _, e := range entries {
		if !e.IsProject() && strings.HasPrefix(e.OriginPath, name+"/") && e.DiscardedAt.After(deletedAt) {
			return true
		}
	}
	return false
}

// refuseScopedName refuses a name unexpired MCP tokens are scoped to: a
// project created, restored or renamed onto it would fall into their scope.
func refuseScopedName(tokens *auth.Store, name string) error {
	if n := scopedTokens(tokens, name); n > 0 {
		return fmt.Errorf("%w: %d MCP tokens are scoped to a project named %q and would reach this one: revoke them or pick another name", ErrInvalid, n, name)
	}
	return nil
}
