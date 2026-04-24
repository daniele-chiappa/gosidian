package vault

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/fsnotify/fsnotify"
)

// Watch starts a recursive fsnotify watcher on the vault and reindexes files
// as they change. If onChange is non-nil it is invoked after every successful
// reindex (create/update/delete). Blocks until ctx is cancelled.
func (v *Vault) Watch(ctx context.Context, idx *index.Index, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	if err := addRecursive(w, v.Root); err != nil {
		return err
	}

	debounce := make(map[string]*time.Timer)

	handle := func(abs string) {
		if !strings.EqualFold(filepath.Ext(abs), ".md") {
			return
		}
		rel, err := filepath.Rel(v.Root, abs)
		if err != nil {
			return
		}
		relSlash := filepath.ToSlash(rel)
		if _, err := v.Rel(relSlash); err != nil {
			return
		}
		n, err := loadNote(v.Root, relSlash)
		if err != nil {
			// file likely deleted
			_ = idx.Delete(relSlash)
			if onChange != nil {
				onChange()
			}
			return
		}
		if err := idx.Upsert(toIndexNote(n)); err != nil {
			log.Printf("watcher upsert %s: %v", relSlash, err)
			return
		}
		if onChange != nil {
			onChange()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-w.Errors:
			log.Printf("watcher error: %v", err)
		case ev := <-w.Events:
			if ev.Op&fsnotify.Create != 0 {
				if st, err := statDir(ev.Name); err == nil && st {
					// Add the new dir to the watcher, then walk it once and
					// schedule re-index for any .md files that already
					// landed inside before the watch was active. fsnotify
					// otherwise loses those CREATE events on a subdir +
					// file race.
					_ = addRecursive(w, ev.Name)
					_ = filepath.Walk(ev.Name, func(p string, info os.FileInfo, err error) error {
						if err != nil || info.IsDir() {
							return nil
						}
						if !strings.EqualFold(filepath.Ext(info.Name()), ".md") {
							return nil
						}
						name := p
						if t, ok := debounce[name]; ok {
							t.Stop()
						}
						debounce[name] = time.AfterFunc(50*time.Millisecond, func() {
							handle(name)
						})
						return nil
					})
				}
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			name := ev.Name
			if t, ok := debounce[name]; ok {
				t.Stop()
			}
			debounce[name] = time.AfterFunc(100*time.Millisecond, func() {
				handle(name)
			})
		}
	}
}

func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		name := info.Name()
		if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
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
