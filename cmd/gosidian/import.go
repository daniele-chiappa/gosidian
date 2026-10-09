package main

import (
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// runImportCmd copies an existing Obsidian (or any markdown) vault into the
// gosidian vault target. It does NOT touch the source. Files under .obsidian
// or other hidden top-level dirs are skipped to avoid pulling Obsidian's
// settings/cache.
//
//	gosidian import-vault --from <obsidian-vault> --to <gosidian-vault>
func runImportCmd(args []string) {
	fset := flag.NewFlagSet("import-vault", flag.ExitOnError)
	from := fset.String("from", "", "source vault directory")
	to := fset.String("to", "", "destination gosidian vault directory")
	overwrite := fset.Bool("overwrite", false, "replace destination files when they already exist")
	_ = fset.Parse(args)

	if *from == "" || *to == "" {
		log.Fatal("--from and --to are required")
	}
	src, err := filepath.Abs(*from)
	if err != nil {
		log.Fatalf("source: %v", err)
	}
	dst, err := filepath.Abs(*to)
	if err != nil {
		log.Fatalf("destination: %v", err)
	}
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		log.Fatalf("source must be a directory: %v", err)
	}
	if err := separateDirs(src, dst); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		log.Fatalf("create destination: %v", err)
	}

	copied, skipped := importDir(src, dst, *overwrite)
	fmt.Printf("Imported %d files (skipped %d).\n", copied, skipped)
	fmt.Println("Now run `gosidian --vault " + dst + "` to start serving the new vault.")
}

// separateDirs refuses a destination that is the source, or lies inside it,
// or holds it, once symbolic links are resolved (BUG-115, S5-13): the same
// folder with --overwrite rewrote the source with itself, and a destination
// inside the source was copied into itself again and again.
func separateDirs(src, dst string) error {
	resolve := func(p string) string {
		// The destination may not exist yet: resolve what does.
		rest := ""
		for {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return filepath.Join(r, rest)
			}
			parent := filepath.Dir(p)
			if parent == p {
				return filepath.Join(p, rest)
			}
			rest = filepath.Join(filepath.Base(p), rest)
			p = parent
		}
	}
	a, b := resolve(src), resolve(dst)
	within := func(child, parent string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if within(a, b) || within(b, a) {
		return fmt.Errorf("--from %s and --to %s must be separate folders, neither inside the other", src, dst)
	}
	return nil
}

func importDir(src, dst string, overwrite bool) (copied, skipped int) {
	_ = filepath.WalkDir(src, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		// Skip hidden top-level directories (.obsidian, .git, .gosidian, …).
		first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if strings.HasPrefix(first, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// A link is not followed: copying what it points to put files from
		// outside the source into the vault (S5-13).
		if d.Type()&iofs.ModeSymlink != 0 {
			fmt.Fprintf(os.Stderr, "skip %s: a symbolic link\n", rel)
			skipped++
			return nil
		}
		if _, err := os.Stat(target); err == nil && !overwrite {
			skipped++
			return nil
		}
		if err := copyAttachment(path, target); err != nil {
			fmt.Fprintf(os.Stderr, "copy %s: %v\n", rel, err)
			return nil
		}
		copied++
		return nil
	})
	return copied, skipped
}

func copyAttachment(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
