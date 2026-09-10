# Agent anchors

Agent anchors close the gap between **agent roles defined in the
vault** and **the local files a CLI harness needs to spawn them**.

## The problem

Vault notes tagged `type:agent` (conventionally under
`<project>/agents/`) are the canonical definition of a specialised
role: its scope, mandatory context, trigger phrases. But a harness
like Claude Code discovers subagents from local files (e.g.
`.claude/agents/*.md`) in the working directory. Copying the role
into a local file works once — then the two copies drift.

## The model

An **anchor** is a thin, generated local file that *references* the
canonical vault note instead of copying it: at spawn time the
subagent pulls its role from the vault (`memory_get` on the canonical
path), so the vault stays the single source of truth. Each anchor
carries a marker:

```
<!-- gosidian:anchor v=<AnchorVersion> canonical=<vault path> profile=<profile> meta=<hash> -->
```

`meta` is a fingerprint of the anchor *shell* (not of the role body,
which is never copied): when the server-side template changes, the
fingerprint changes and the anchor gets rewritten on the next
bootstrap.

## Enabling (off by default on every axis)

Three switches must align — with any of them off, bootstrap behaves
exactly as before and no anchor is ever surfaced:

1. **Master switch**: `GOSIDIAN_ANCHORS_ENABLED=true`
   (`agent_anchors.enabled` in `config.toml`).
2. **Per-project opt-in**: the `use_anchors` flag on the project
   (project flags live in `<state-dir>/projects.json`,
   alongside `use_globals` / `public` / `hidden_from_mcp`).
3. **A profile that supports native subagents**: pass
   `profile="claude"` (the default) to `memory_bootstrap`; profiles
   without a native subagent mechanism yield no anchors.

## The reconcile flow

The server is **cwd-blind**: it cannot see the agent's working
directory. `memory_bootstrap` therefore returns the *desired* anchor
set and a reconcile directive; the calling agent (which does have
filesystem access) applies the diff:

```jsonc
"anchors": {
  "profile": "claude",
  "target_dir": ".claude/agents",
  "items": [ { "path": "...", "canonical": "...", "meta_version": "...", "content": "..." } ],
  "reconcile": "…how to apply the set against target_dir…"
}
```

Per item: write the file if missing; rewrite it if its marker's
`meta` differs from `meta_version`; leave it alone if equal. Remove
files carrying a `gosidian:anchor` marker whose `canonical` is no
longer in the set (orphans). **Never touch files without the
marker** — those are hand-written ("foreign") subagents.

On repeat bootstraps, pass `known_anchor_metas` (a map of
`canonical → meta_version` from the previous round): items whose meta
still matches come back as `{path, canonical, meta_version,
unchanged: true}` with no `content`, mirroring the `known_etags`
mechanism. An `unchanged` item means "leave the file as is"; if it
went missing from disk, re-bootstrap without the param to get the
content back.

Anchor files are generated artifacts: gitignore them
(`.claude/agents/` entries with the marker), like any other build
output.

## Harness overrides (`harness:` frontmatter block)

A `type:agent` note controls its anchor shell through an optional
`harness:` block in the frontmatter:

```yaml
harness:
  name: custom-name        # anchor name (default: the note slug)
  description: routing …   # routing hint (default: the note description)
  model: sonnet            # optional model line
  tools: [Read, Bash]      # explicit toolset (default: a least-privilege preset)
  materialize: false       # keep the role vault-only (no local anchor)
```

Two special values:

- `tools: all` renders the anchor **without** a `tools:` line, which
  the CLI interprets as "inherit the full default toolset" — no more
  enumerating (and drifting) 15+ tool names per role.
- `materialize: false` opts the note out of materialisation: it stays
  a canonical, listable role (e.g. an orchestrator that must not be
  spawnable as a subagent) but never appears in the anchors set. An
  already-materialised anchor becomes an orphan and is removed by the
  normal reconcile flow.

## Adopting a hand-written subagent

When the reconcile pass finds a foreign file worth promoting to a
shared, versioned role, `memory_promote_agent` adopts it: the
system-prompt body becomes a canonical `type:agent` vault note (the
`name`/`tools` harness fields are captured into a `harness:`
frontmatter block) and the tool returns a thin anchor to replace the
original file. Deliberately human-gated — promotion is a curation
decision, not an automatic sweep.

When the canonical note **already exists** (a role created before
anchors), pass `adopt_into_existing: true`: the existing body is
never touched (it stays the source of truth), a `harness:` block is
inserted only if missing, and the foreign body comes back in
`foreign_body_for_review` with an instruction to fold any unique
content into the canonical note via `memory_edit` before replacing
the foreign file with the returned anchor. Without the flag, the
exists-error explains exactly this.

## Why not just copy the file?

Same reason the operational directives are served by
`memory_bootstrap` instead of being pasted into every `CLAUDE.md`:
copies drift, references don't. The anchor shell changes rarely (its
`meta` fingerprint tracks that); the role body lives in exactly one
place, versioned with the rest of the vault and shared by every
working directory that bootstraps the project.
