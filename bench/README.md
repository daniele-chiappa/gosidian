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
| `base/` | Level B: any image of the server, measured from outside — level R through `memory_search`, what an agent receives, latency, start, memory, CPU, sizes, code. `run.sh` builds and runs it. |
| `backlog/` | The backlog benchmark: a project (`harbor`) whose backlog of 180 entries is written as one note with a section per entry and as a database note with one note per entry, the same facts in both, and 22 status questions with the answers computed from the entries. Deterministic; `run.sh` runs level A on one image and one shape. |
| `scale/` | Builds a larger copy of the vault: noise notes (plans, meetings, support articles, research) added to `tidewater`, deterministic for a seed, never touching what the questions depend on. |
| `results/` | Raw results, one file per run, kept as a history. `rag-*.json`: the RAG benchmarks, one file per benchmark or configuration. |
| `rag/` | `render.py` writes the tables of [docs/rag-benchmarks.md](../docs/rag-benchmarks.md) from `results/rag-*.json` (Python, standard library). |
| `rag/vidore/` | The public RAG reference, end to end on [ViDoRe V3](https://arxiv.org/abs/2601.08620): download, notes, the two indexes, the search on every question, the answers and their judges, the JSON of the results. How to run it: [rag/vidore/README.md](rag/vidore/README.md). |

## RAG benchmarks

The benchmarks of the RAG pipelines (embeddings, chunks, reranker, image
descriptions, answers, document conversion) ran in the maintainers' lab
on private corpora, so only their numbers are here: one JSON per
benchmark or configuration in `results/rag-*.json`, with versions,
hardware, settings and date. The page that reads them is
[docs/rag-benchmarks.md](../docs/rag-benchmarks.md); after changing a
JSON, rewrite its tables with

```bash
python3 bench/rag/render.py           # rewrite the tables in place
python3 bench/rag/render.py --check   # exit 1 if a table is out of date
```

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
- **B — the base**, no model: an image of the server, any version, started
  in a throwaway container (2 CPUs, 2 GB) on a copy of the vault and
  measured from outside, as an agent uses it. Level R through
  `memory_search` (the tool's two ways: one query, and `any_of`); the
  tools and bootstrap an agent receives, in bytes; the latency of the
  everyday tools; the start, cold and warm, on this vault and on a large
  one (`bench/scale`, 5,000 more notes); memory and CPU from the
  container's cgroup; image, binary, index and the web UI's first load;
  the code of the version, from git. Because it knows nothing of the
  server's code, the same command measures past versions from their
  images. Minutes.

## Running level B

```bash
bench/base/run.sh ghcr.io/daniele-chiappa/gosidian:v2.119.0     # three runs, the median
bench/base/run.sh gosidian:dev dev 1                           # a local image, one run
go run ./bench/base compare bench/results/base-*.json          # one column per file
```

`run.sh` builds `bench/base` and `bench/scale` in a Go container (the host
needs docker and git), makes the large vault once in
`~/.cache/gosidian-bench-base`, and writes
`bench/results/base-<date>-<version>.json`. Measure on a quiet machine: the
results record the load before and after. The vault is copied just before
each start, so its files are in the host's page cache: a cold start
measures building the index, not reading the disk.

### The guard

```bash
bench/base/guard.sh gitea.example.com/org/gosidian:main-1a23ee5 /tmp/guard.json
go run ./bench/base check bench/results/base-2026-10-29-main-1a23ee5-a.json
go run ./bench/base reference -why "what changed and why" bench/results/base-<date>-*.json
```

`bench/base/reference.json` holds the base's numbers as the roadmap's M1.2
decided them: for each, its path in a results file, its reference, the
room it has the wrong way before it counts as worse (`pct` of the
reference, at least `min`), and its objective when it has one. Numbers
with no noise (the search's results, the bytes an agent receives, the web
UI's) have no room: any fall of a result, any byte more counts.

`check` prints the table and fails when a number is past its limit or
missing, or the bench met errors; its `WORSE` and `MISSING` lines are what
the nightly run's message quotes. `guard.sh` waits for the load to go
under 4 (at most 15 minutes), measures three runs and checks them, and
measures again before it fails: the nightly run's stage `base` runs it on
the night's image, and a release runs it on the image of the commit it
tags. A number past its limit stops the tag. Every number in a results
file is the median of its runs; the latency percentiles are each run's,
and their median.

The reference moves only with a commit that says why: an improvement past
the noise (`check` prints `BETTER`), or a cost accepted, such as a new
tool's bytes. `reference` sets it to the medians of the results given and
keeps each number's room and objective; with `-only` it moves just the
metrics named, and the file keeps the move (when, why, from what).

## Running the backlog benchmark

A backlog is where database notes should matter most: "what is still
open?" over a long note means reading all of it, over rows it is one
`memory_query`. `bench/backlog` writes the same backlog both ways, outside
`bench/vault`, so the numbers of that vault stay comparable:

- `sections` — `harbor/docs/improvements.md`, one `## IMP-001 — title`
  section per entry with its status, priority, area, owner and dates as a
  list: a backlog before database notes (v2.42.0 and earlier).
- `database` — the same path as a database note (`type: database`) over
  `harbor/docs/improvements/`, one note per entry with those fields in the
  frontmatter, as [Moving an existing
  backlog](../docs/vault/databases.md#moving-an-existing-backlog) does it.

The 22 questions in `backlog/questions.jsonl` ask for one entry, how many,
which ones, which is the most, and two with no answer. A list question
also has `reject` patterns: the entries that miss one condition only.
An answer that names one is not failed, since a correct answer often names
them to say why it left them out: `-summary` lists it with the negatives,
to be read by hand. The file is the
generator's output (`go run ./bench/backlog -questions
bench/backlog/questions.jsonl`), and a test keeps it so.

```bash
bench/backlog/run.sh gosidian:v2.119.0 database                 # one run, the 22 questions
BENCH_BACKLOG_PORT=8085 bench/backlog/run.sh gosidian:v2.42.0 sections v2.42.0-sections 2
go run ./bench/agents -summary bench/results/agents-<date>-backlog.jsonl -questions bench/backlog/questions.jsonl
```

`run.sh IMAGE FORM [LABEL] [RUNS] [FLAGS...]` builds the generator and the
harness in a Go container, starts the image on the vault of that shape (2
CPUs, 2 GB, the port on the loopback, 8083 unless `BENCH_BACKLOG_PORT`
says otherwise) with a read token, and runs the `gosidian` configuration,
labelled with the image's tag and the shape. Runs on different ports can
go in parallel. The sessions use model quota.

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
  the counterpart of `fs-oriented` with `.gosidian-index.md` and `MIRROR.md`.
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

### When to run level A

Level A uses model quota, so the nightly run does not include it: the
nightly run measures level B. The backlog's 132 sessions of 2026-10-27
cost about $10 at API prices and used up a subscription's session limit.
Run level A:

- when a change touches what an agent receives or how it finds things:
  the MCP tools and their descriptions, the bootstrap, the directives and
  the stub, search, `memory_query`, views and database notes
  (`internal/mcp`, `internal/initprompt`, `internal/index`,
  `internal/views`, `internal/dbschema`), on the change's image before
  its tag;
- otherwise every two weeks.

One run of the `gosidian` configuration is enough to compare with the
stored runs: the backlog as a database (22 sessions) and the benchmark
vault (50). The other shapes and versions again only when the model or
Claude Code changes.

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
- 2026-10-27 — the backlog's `reject` patterns (level A): during the first
  run, before any table, every answer they had failed was correct and
  named the near misses to say why it left them out ("IMP-097 is done, so
  I left it out"). A match no longer fails the answer; it puts the answer
  on the list to be read by hand.
