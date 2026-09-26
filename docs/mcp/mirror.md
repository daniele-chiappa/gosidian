# Local read-only mirror

An agent that reads files is cheaper than one that asks MCP for them: in
the [benchmark](../benchmark.md), an agent reading a copy of the notes —
told where to start (`hot.md`, `README.md`) and to retry with synonyms —
answered as well as the gosidian MCP agent at about half the cost per
session on a real vault. A **mirror** gives an agent that copy: one project,
read-only, next to its working directory, kept in sync with the server.
Writes still go through MCP, with their locks, audit and access checks.

What it saves depends on MCP. Reads from the mirror cost about what
reads through MCP cost; gosidian's extra cost is fixed per session — the
bootstrap and the tool schemas. An agent that reads only the mirror and
has no gosidian MCP server avoids it: in the benchmark, $0.085 against
$0.138 for five questions in one session. An agent that also has the MCP
server and starts with `memory_bootstrap`, as the CLAUDE.md stub asks,
pays it anyway, and the mirror changes little in cost
([benchmark](../benchmark.md#several-questions-per-session-2026-09-26)).

A mirror copies notes onto another machine, so it is **off by default**
and enabled per project by a project admin.

## Enable it on a project

Projects → the project's row → **`mirror`** (flag `allow_local_mirror`).
From then on, a token that can read the project may list it through
`GET /mcp/manifest` ([reference](upload.md#http-manifest-endpoint-local-mirrors)).
Every listing is audited (`mirror_sync`). Turning the flag off stops new
syncs; it does not erase copies that already exist.

## Sync

```bash
export GOSIDIAN_URL=https://vault.example.com/mcp   # your MCP base URL
export GOSIDIAN_TOKEN=…                             # read scope; a self-service token in inherit mode fits
gosidian mirror sync --project work                 # → .gosidian/mirror/work/
```

The values can also come from `.claude/gosidian.env`, the file the
[Claude Code hooks](../../contrib/claude-code/README.md) use
(`GOSIDIAN_URL`, `GOSIDIAN_TOKEN`, `GOSIDIAN_PROJECT`).

- The copy lives in **`.gosidian/mirror/<project>/`** under the working
  directory (`--dir` to change the root), with the vault's own paths, so
  `[[work/plans/x]]` wikilinks resolve from the mirror root. **Add
  `.gosidian/` to `.gitignore`**: the command warns when it is not ignored.
- Files are **read-only** and carry the server's modification time. The
  first sync downloads every note; later syncs download only the notes
  whose ETag changed and delete the ones that disappeared.
- **`MIRROR.md`** at the mirror root says what the folder is and how to
  write; **`<project>/_index.md`** lists the notes from the most recent,
  with title, path and tags — a copy of files cannot otherwise tell which
  of two notes is newer.
- Two syncs of the same project never run at once (a lock file; a sync
  already running makes the second exit quietly).
- The token is never written to the mirror.

```bash
gosidian mirror status                 # mirrored projects, notes, last sync
gosidian mirror purge --project work   # delete one project's copy
gosidian mirror purge                  # delete the whole mirror
```

## Access and limits

- The mirror holds what the token can read in that project, computed on
  the server at each sync: visibility, grants and teams of the owner
  account for account tokens; hidden files and projects hidden from MCP
  are never listed.
- It is a **snapshot**: between syncs, changes made by others are not
  there. Sync at the start of a session and again when freshness matters.
- It has no ranking: `grep` finds words, not the most relevant note. For
  "what is the current state" questions, `_index.md` and the frontmatter
  dates tell which note is newer.
- A revoked token cannot delete a copy already on disk.
