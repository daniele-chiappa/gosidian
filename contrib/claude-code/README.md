# Claude Code hooks for gosidian

A safety net for the two things an agent forgets most: reading the vault's
`hot.md` at the start of a session and leaving a trace at the end. The hooks
talk to gosidian over HTTP with an MCP bearer token, so they work even when
the agent never opens an MCP session — and they never block Claude Code.

| Event | What the hook does |
|---|---|
| `SessionStart` (startup, resume, clear, compact) | Injects `<project>/hot.md` into the context — whole when it fits `GOSIDIAN_HOOK_FOCUS_BYTES` (6000), and then the `memory_bootstrap` reminder carries its etag in `known_etags` so the bootstrap does not repeat it; otherwise its **Current focus** section — plus this session's checkpoint note after a resume or a compaction. The bootstrap stays authoritative. |
| `PreCompact` (manual, auto) | Appends a **checkpoint digest** to `<project>/sessions/<date>-<session>.md`, so the compacted context finds the thread again at the next `SessionStart`. |
| `SessionEnd` | Appends the **session digest** to the same note. With `GOSIDIAN_HOOK_LOG_ENTRY=1`, also a one-line pointer in `<project>/log.md`. |
| `Stop` | Nothing by default. With `GOSIDIAN_HOOK_STOP_LOG=1`, appends the last assistant message at every turn. |
| `PostToolUse` (gosidian write tools) | With `GOSIDIAN_MIRROR=1`, refreshes the local mirror after the agent writes through MCP. |

Digests are built **without any LLM** from the session transcript: first
prompt, turn counts, tools used, files touched, last assistant message. They
are raw material in a low-importance note, not curated memory — the agent
still owns `hot.md`, `log.md` and the memory notes. The session notes are
tagged `topic:auto-capture` and `importance: 1` so `memory_stale` and the
grooming digest can sweep them.

## Install

1. Copy the script into your checkout and make it executable:

   ```bash
   mkdir -p .claude/hooks
   cp contrib/claude-code/gosidian-hook.sh .claude/hooks/
   chmod +x .claude/hooks/gosidian-hook.sh
   ```

2. Configure the connection. Copy `gosidian.env.example` to
   `.claude/gosidian.env`, fill in `GOSIDIAN_URL`, `GOSIDIAN_TOKEN` and
   `GOSIDIAN_PROJECT`, and **add `.claude/gosidian.env` to `.gitignore`**
   (it holds a token). Alternatively export the three variables in the
   environment, or put the file at `~/.config/gosidian/hook.env`. The token
   needs write scope: a self-service token in *inherit* mode
   (Settings → My MCP tokens) is the right fit — it follows your account's
   access as grants change.

3. Register the hooks. Merge `settings.json.example` into
   `.claude/settings.json` (shared with the team) or
   `.claude/settings.local.json` (yours only). The timeouts matter:
   `SessionEnd` hooks get a 1.5 s budget unless a per-hook `timeout` raises
   it.

4. Check: start a new Claude Code session in the checkout. The first
   context carries `hot.md` (or its focus excerpt); `/clear` or exiting produces
   `<project>/sessions/<date>-<id>.md` in the vault (Projects → the project,
   or `memory_recent`).

Requirements: bash, `curl`, `jq`. Server: gosidian ≥ 2.33 (`POST /mcp/append`).

## Local read-only mirror (optional)

With `GOSIDIAN_MIRROR=1` the hooks keep a read-only copy of the project in
`.gosidian/mirror/<project>/` ([guide](../../docs/mcp/mirror.md)) and tell
the agent to read and search there. Reads from the copy cost about what
MCP reads cost; the saving is for an agent that has no gosidian MCP server
and so skips the bootstrap and the tool schemas
([benchmark](../../docs/benchmark.md#several-questions-per-session-2026-09-26)):

- `SessionStart` starts `gosidian mirror sync` in the background (the
  session does not wait) and adds to the context where the mirror is, when
  it was last synced and how to use it: start from `hot.md`, `README.md`
  and `_index.md`, prefer the more recent of two notes that disagree,
  never edit the files, write through MCP.
- `PostToolUse` on the gosidian write tools syncs again, so the agent's own
  writes show up in the copy (only changed notes are fetched).

Requirements: the `gosidian` binary on the machine (a release binary, or
`GOSIDIAN_BIN=/path/to/gosidian`), a project admin who turned on
**`mirror`** for the project, and `.gosidian/` in `.gitignore`. Sync output
goes to `.gosidian/mirror/.sync.log`.

## What goes over the wire

- `GET  <URL>/download?path=<project>/hot.md` and the session note (read).
- `POST <URL>/append?path=<project>/sessions/...` (and `log.md` if enabled)
  with a markdown body — the same pipeline as `memory_append`: per-note
  lock, size and rate limits, audit (`append`), live events.

Privacy: the digest contains the first prompt (truncated), file paths,
tool names and an excerpt of the last assistant message. Everything lands in
the vault project the token is scoped to. Keep `GOSIDIAN_HOOK_STOP_LOG` off
unless you want every turn recorded.

## Testing the script

`test.sh` runs the hook against a fake server with fixture input and
transcript, no vault needed:

```bash
contrib/claude-code/test.sh
```

## Other agents

The endpoints are plain HTTP, so the same idea ports to any tool with
lifecycle hooks (Cursor, Gemini CLI, Codex): read `hot.md` at start, append
a digest at the end. Contributions welcome under `contrib/`.
