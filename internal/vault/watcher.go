package vault

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gosidian/gosidian/internal/index"
)

// Watch starts a recursive fsnotify watcher on the vault and reindexes files
// as they change. If onChange is non-nil it is invoked after every successful
// reindex (create/update/delete) with the vault-relative path that changed,
// a note or a folder gone. Blocks until ctx is cancelled.
func (v *Vault) Watch(ctx context.Context, idx *index.Index, onChange func(rel string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	if err := v.addRecursive(w, v.Root); err != nil {
		return err
	}

	handle := func(abs string) {
		rel, err := filepath.Rel(v.Root, abs)
		if err != nil {
			return
		}
		relSlash := filepath.ToSlash(rel)
		if _, err := v.Rel(relSlash); err != nil {
			return
		}
		if !v.IsNoteFile(abs) {
			// A folder moved or removed from outside sends one event, for
			// itself: its notes stayed in the index until a restart
			// (BUG-115, S5-15).
			if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
				if gone, err := idx.NotesByPrefix(relSlash); err == nil && len(gone) > 0 {
					for _, n := range gone {
						_ = idx.Delete(n.Path)
					}
					if onChange != nil {
						onChange(relSlash)
					}
				}
			}
			return
		}
		n, err := loadNote(v.Root, relSlash)
		if err != nil {
			// file likely deleted
			_ = idx.Delete(relSlash)
			if onChange != nil {
				onChange(relSlash)
			}
			return
		}
		if err := idx.Upsert(toIndexNote(n)); err != nil {
			log.Printf("watcher upsert %s: %v", relSlash, err)
			return
		}
		if onChange != nil {
			onChange(relSlash)
		}
	}

	// One pending timer per path, dropped when it fires, so the map holds
	// only the paths about to be handled. The timers fire on their own
	// goroutines: mu guards the map.
	var mu sync.Mutex
	debounce := make(map[string]*time.Timer)
	schedule := func(name string, wait time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		if t, ok := debounce[name]; ok {
			t.Stop()
		}
		var t *time.Timer
		t = time.AfterFunc(wait, func() {
			mu.Lock()
			if debounce[name] == t {
				delete(debounce, name)
			}
			mu.Unlock()
			handle(name)
		})
		debounce[name] = t
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors:
			log.Printf("watcher error: %v", err)
		case ev := <-w.Events:
			// What the vault skips is no vault content: hidden entries
			// (every save goes through a hidden temporary file, writeWhole),
			// node_modules, the state dir. A node_modules made by an npm
			// install had thousands of READMEs indexed (BUG-115, S5-9).
			if v.skipDir(v.Root, ev.Name) {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if st, err := statDir(ev.Name); err == nil && st {
					// Add the new dir to the watcher, then walk it once and
					// schedule re-index for any .md files that already
					// landed inside before the watch was active. fsnotify
					// otherwise loses those CREATE events on a subdir +
					// file race.
					_ = v.addRecursive(w, ev.Name)
					_ = filepath.Walk(ev.Name, func(p string, info os.FileInfo, err error) error {
						if err != nil {
							return nil
						}
						if info.IsDir() {
							if v.skipDir(v.Root, p) {
								return filepath.SkipDir
							}
							return nil
						}
						if !v.IsNoteFile(info.Name()) {
							return nil
						}
						schedule(p, 50*time.Millisecond)
						return nil
					})
				}
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			schedule(ev.Name, 100*time.Millisecond)
		}
	}
}

// addRecursive watches root and the folders below it, leaving out those
// the vault skips: hidden ones, node_modules and the state dir, whose index
// writes would otherwise wake the watcher at every change. root is the
// vault or a folder created in it.
func (v *Vault) addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if v.skipDir(v.Root, path) {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

func statDir(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return st.IsDir(), nil
}
