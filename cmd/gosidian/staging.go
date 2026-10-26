package main

import (
	"log"

	"github.com/gosidian/gosidian/internal/vault"
)

// stagingInVault returns the vault-relative paths of the bridge dir and of
// the upload roots that lie inside the vault, the folders git sync leaves
// out (BUG-138), and logs each one: an upload root set on a visible folder,
// a project, leaves the backup too, since the server takes what is in it for
// staged files, not for vault content. A root outside the vault, or the
// vault itself, gives nothing.
func stagingInVault(v *vault.Vault, bridgeDir string, uploadRoots []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(dir, what string) {
		rel, ok := v.RelInside(dir)
		if !ok || seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, rel)
		log.Printf("gitsync: leaving out %s, the %s inside the vault: files staged there are not vault content (BUG-138)", rel, what)
	}
	add(bridgeDir, "bridge dir")
	for _, r := range uploadRoots {
		add(r, "upload root")
	}
	return out
}
