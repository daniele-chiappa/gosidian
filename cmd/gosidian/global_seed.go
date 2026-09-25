package main

import (
	"log"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

// seedGlobalProjects ensures the shared global projects exist, are flagged
// correctly (the public project gets Public=true), and carry a starter README,
// before the initial vault scan. Idempotent: existing folders, flags and
// READMEs are preserved. See plan 20260608-global-project-shared-skills.
func seedGlobalProjects(v *vault.Vault, pstore *projects.Store, cfg config.GlobalConfig) {
	seed := func(name string, public bool) {
		if name == "" {
			return
		}
		// "already exists" is fine — CreateProject is idempotent in effect.
		_, _ = v.CreateProject(name)
		// The public global project must be readable by every account; the
		// private one is grants-only. Only fill an unset visibility so an
		// owner's explicit choice survives restarts.
		want := projects.VisibilityPrivate
		if public {
			want = projects.VisibilityPublic
		}
		if f := pstore.Get(name); f.Visibility == "" {
			f.Visibility = want
			f.Public = false
			if err := pstore.Set(name, f); err != nil {
				log.Printf("global: set visibility on %q: %v", name, err)
			}
		}
		readme := name + "/README.md"
		if _, err := v.Load(readme); err != nil {
			if err := v.Save(readme, []byte(globalReadme(name, public))); err != nil {
				log.Printf("global: seed README for %q: %v", name, err)
			}
		}
	}
	seed(cfg.PublicProject, true)
	seed(cfg.PrivateProject, false)
}

// globalReadme returns the starter index note for a freshly seeded global
// project.
func globalReadme(name string, public bool) string {
	vis := "private (owner-only)"
	if public {
		vis = "public (shared with everyone; guests read-only)"
	}
	return "---\n" +
		"title: " + name + " — shared global project\n" +
		"description: Reusable skills, agents and init templates other projects reference via the use_globals flag.\n" +
		"tags: [" + name + ", type:index, topic:meta]\n" +
		"type: index\n" +
		"---\n\n" +
		"# " + name + "\n\n" +
		"Shared **global** project (" + vis + "). Holds reusable `type:skill` / `type:agent`\n" +
		"notes and `templates/<destination>/` init presets that other projects pull in by\n" +
		"setting their `use_globals` flag. Local entries with the same title override the\n" +
		"global ones (local-overrides-global).\n\n" +
		"- `skills/` — reusable skills\n" +
		"- `agents/` — reusable agent roles\n" +
		"- `templates/<destination>/` — scaffold presets by project destination\n"
}
