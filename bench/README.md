# Benchmark

A reproducible benchmark of how well an agent finds things in a gosidian
vault. Results and method: [docs/benchmark.md](../docs/benchmark.md).

## Layout

| Path | What |
|---|---|
| `vault/` | The benchmark vault: a fictional ticketing project (`tidewater`), an analytics project (`atlas`) and shared skills (`global`), written once and frozen. Mostly English, a few notes in Italian. |
| `questions.jsonl` | 50 questions with the expected answer, `check` strings that must appear in the `evidence` notes, and `r_gold`: the notes that count as a hit when measuring retrieval. |
| `queries.jsonl` | The search each question gets at level R: a `query` and up to four `any_of` phrasings. Written by an agent that saw only the question text, never the vault. |
| `retrieval/` | Level R runner and the consistency tests (run by `go test ./...`). |
| `scale/` | Builds a larger copy of the vault: noise notes (plans, meetings, support articles, research) added to `tidewater`, deterministic for a seed, never touching what the questions depend on. |
| `results/` | Raw results, one file per run, kept as a history. |

## Levels

- **R — retrieval**, no model: indexes the vault with the server's own code
  and reports where `memory_search` ranks the gold notes (R@1, R@5, MRR),
  in three modes: `bm25` (text only), `ranked` (title and structural
  factors), `ranked+any_of` (with the phrasings). Deterministic, seconds.
- **A — agents**, headless Claude Code sessions answering the questions in
  two configurations: `fs` (the vault as working directory, Read, Grep and
  Glob only) and `gosidian` (no file tools, a gosidian MCP server serving
  the same vault, `memory_bootstrap` first). Answers are scored with the
  `accept` patterns of each question; negative questions are flagged for
  a manual check. Sessions use model quota: run a few ids first.

## Running level R

```bash
go run ./bench/retrieval          # summary tables
go run ./bench/retrieval -v       # plus the rank of every question
go run ./bench/retrieval -json bench/results/retrieval-$(date +%F).json
```

## Running level A

```bash
# a gosidian server on a copy of the benchmark vault, with a read-only token
cp -r bench/vault /tmp/gos-bench-vault && chmod -R a+rwX /tmp/gos-bench-vault
docker run -d --name gos-bench -p 127.0.0.1:8083:8080 -e GOSIDIAN_STATE_DIR=/data \
  -v /tmp/gos-bench-vault:/vault -v gos-bench-state:/data ghcr.io/daniele-chiappa/gosidian:latest
export GOSIDIAN_BENCH_TOKEN=$(docker exec gos-bench /gosidian token create \
  --vault /vault --name bench --scopes read | grep -oE 'gosidian_[A-Za-z0-9_-]+')

go run ./bench/agents -config both -ids L2,L9 -runs 1     # a first look
go run ./bench/agents -config all -runs 3                 # fs, gosidian, gosidian-core
go run ./bench/agents -summary bench/results/agents-<date>.jsonl
```

`gosidian-core` needs a second token created with `--tool-profile core`
in `GOSIDIAN_BENCH_TOKEN_CORE`; `gosidian-deferred` reuses the full token
and gives the client its ToolSearch tool, so schemas can load on demand;
`fs-oriented` is `fs` plus the orientation a gosidian project gives
(hot.md, README, tags, wikilinks), the reading side of a local mirror.

The two mirror configurations measure the local read-only mirror of
IMP-102 with the real pieces: the harness syncs the mirror with the
gosidian binary (`-gosidian-bin`) before the first session and asks the
Claude Code hook (`-hook`, default `contrib/claude-code/gosidian-hook.sh`)
for its SessionStart context, then copies that mirror into every session's
working directory.

- `mirror` — file tools only, every project of `-mirror-projects` synced
  into `.gosidian/mirror/`, orientation from the hook's mirror section:
  the counterpart of `fs-oriented` with `_index.md` and `MIRROR.md`.
- `mirror-mcp` — the deployed setup: only `-project` mirrored, the MCP
  server as well (schemas on demand through ToolSearch), the stub's
  "memory_bootstrap first" and the hook's whole context.

The benchmark server must allow the mirror on the mirrored projects: turn
on `mirror` for `tidewater`, `atlas` and `global` on the Projects page, or
`PUT /api/v1/projects/<project>` with `{"allow_local_mirror": true}` as
the owner. Then:

```bash
go run ./bench/agents -config mirror,mirror-mcp -gosidian-bin ./gosidian
```

- `hooks-mcp` — `mirror-mcp` with the mirror off: same tools, stub and
  hook context, no local copy. Against `mirror-mcp` it isolates what the
  mirror changes.

Sessions from 2026-09-26 on also record their tool calls (`tools`), and
`-summary` adds a table of them per configuration.

### Several questions per session

One question per session puts the whole start of a session — tool
schemas, `memory_bootstrap` — on a single answer. `-per-session N` asks N
questions in one session instead, as a working session looks things up
several times after one bootstrap: question *i* goes to session *i* mod
(questions / N), so every session mixes categories, and the reply is cut
at its `### <id>` headers so each answer is scored on its own. The
sessions are recorded as `B01`, `B02`… with their questions in `batch`
and each answer in `parts`; `-summary` reports them in a separate table
with the cost per session and per question.

```bash
go run ./bench/agents -config hooks-mcp,mirror-mcp,mirror -per-session 5 -timeout 15m
```

Project flags are measured by turning them on in the benchmark server
(Projects page or `PUT /api/v1/projects/tidewater`) and running with a
label, e.g. `-config gosidian,gosidian-core -label lean` for
`lean_read_bootstrap`: the results then read `gosidian-lean`,
`gosidian-core-lean`.

Each session is isolated with `claude --restricted --strict-mcp-config`:
the operator's settings, hooks, CLAUDE.md and MCP servers never reach it.
Results are appended to `bench/results/agents-<date>.jsonl`.

## Runs on another vault

The harness is not tied to the benchmark vault: `-vault`, `-questions`,
`-project` (bootstrapped first by the gosidian configurations) and
`-projects` (how the prompts list the projects) point it at any vault —
for example a private copy with questions about your own notes. Keep such
questions and results out of this repository.

## Scale runs

```bash
go run ./bench/scale -out /tmp/bench-vault-large -notes 420     # tidewater: 67 → 489 notes
go run ./bench/retrieval -vault /tmp/bench-vault-large
go run ./bench/agents -config fs-oriented,gosidian -vault /tmp/bench-vault-large   # server on the same copy
```

## Rules

- The vault, the questions and their gold are frozen before any run. A
  change to the vault that breaks a gold note fails `go test ./bench/...`.
- New questions get their `queries.jsonl` line from an agent that sees only
  the question, never the notes; its output is copied verbatim.
- Report unfavourable results too.

## Gold revisions

A gold note is added only when it states the answer and was missed when
the gold was written; every revision is listed here.

- 2026-09-25 — T8: added `tidewater/plans/2026-03-22-jwt-auth.md`, which
  says "ADR-009 supersedes the Redis sessions of ADR-002".
- 2026-09-25 — N4 (level A): the manual review of the negatives found
  correct answers the pattern did not accept ("the vault does not contain
  any reference to BUG-031"); the accept pattern now covers those
  phrasings. `-summary` re-scores every stored answer with the current
  patterns, so a revision applies to all runs alike.
- 2026-09-26 — D1 (level A): a correct answer wrote "seat-maps" with a
  hyphen; the pattern `seat maps?` now accepts `seat[- ]maps?`. Of every
  stored run, only that answer (mirror-mcp) changes.
