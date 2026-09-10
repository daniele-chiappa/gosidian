# Global projects — shared skills, agents & templates

As a vault grows to many projects, the same procedures get rewritten
over and over: "review code before commit", "cut a release", "record an
architectural decision". The **global projects** feature gives you a
shared home for that reusable material — write a skill, agent, or
scaffold template once, and let any project opt into it.

**Disabled by default.** With `[global] enabled = false` (the default)
nothing changes: there are no global projects and `memory_bootstrap`
returns each project's own notes exactly as before.

## The two global projects

When enabled, gosidian seeds two projects at startup:

| Project | Visibility | Holds |
|---|---|---|
| `global` | **public** (RBAC) — shared with every token, guests included | skills/agents/templates safe to expose publicly |
| `global-private` | **private** — owner-only | skills that reference internal infra, secrets, or host specifics |

Names are configurable (`global.public_project` / `global.private_project`);
the defaults are `global` and `global-private`.

## Enabling it

Operator master switch (off by default), in `<state-dir>/config.toml`:

```toml
[global]
enabled = true                      # master switch
public_project = "global"           # shared with everyone
private_project = "global-private"  # owner-only
```

Both fields have a `GOSIDIAN_GLOBAL_*` env override — see
[Configuration](../configuration.md). Restart gosidian after editing;
the two projects are seeded on the next boot.

## Opting a project in

Sharing is **per project** and opt-in — a project only sees global
material once it sets the `use_globals` flag in the projects store
(`<state-dir>/projects.json`):

```json
{ "my-project": { "use_globals": true } }
```

> There is no web/CLI toggle for `use_globals` yet — it is set by
> editing the projects store directly. A management-surface toggle is
> on the roadmap.

## How the merge works

For an opted-in project, `memory_bootstrap` augments the project's own
skills and agents with the global ones. The rules:

- **Local overrides global.** If a project has its own skill with the
  same title as a global one, the local version wins and the global is
  hidden — so a project can specialise a shared skill without forking
  the whole set.
- **Source is labelled.** Every entry in `available_skills` /
  `available_agents` carries a `source` field: `local`, `global`, or
  `global-private`. You always know where a procedure came from.
- **Public is shared with everyone; private is gated.** `global`
  entries are merged for any token. `global-private` entries are merged
  only for tokens that can read that project (the owner / an
  admin-scoped token) — a scoped guest token never sees them.

```jsonc
// memory_bootstrap("my-project").available_skills
[
  { "title": "Deploy my-project", "source": "local" },          // project-specific
  { "title": "Record an ADR",     "source": "global" },         // shared, public
  { "title": "Rotate host certs", "source": "global-private" }  // owner only
]
```

## Templates live in the global project too

When global is enabled, the bootstrap **scaffold templates**
(`karpathy-wiki`, `minimal`, `team`) are seeded as editable notes under
`global/templates/<name>/` instead of the machine-owned
`.gosidian/templates/`. That means you can edit, extend, or add presets
as ordinary vault notes, and `memory_project_scaffold` will pick them
up. Template-definition files under `global/templates/` are **excluded**
from the skill/agent merge — they are scaffold sources (with
`{{PLACEHOLDER}}` content), not runnable skills.

## Promoting private → public

A skill often starts in `global-private` (it touched something internal)
and later turns out to be safe to share. `memory_global_check(project)`
is the read-only, human-gated helper: it reports which `global-private`
notes a project actually references (via wiki-links) and which other
projects also use them, so you can decide what is worth generalising.
Promote a note with `memory_move_note` from `global-private/` to
`global/` once you have sanitised it.

## See also

- [Multi-project layout](multi-project.md) — top-level folders as
  projects, scoped tokens, cross-project links
- [Configuration](../configuration.md) — the `[global]` env/TOML reference
- [Conventions](conventions.md) — tag vocabulary, skill/agent shape
