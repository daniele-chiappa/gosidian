# Benchmark

How well does an agent find what it needs in a gosidian vault? Other memory
servers publish recall figures measured on conversational memory
(LongMemEval); gosidian is a wiki of notes, so we measure on a vault of
notes instead. Everything needed to rerun it is in [`bench/`](../bench/README.md).

This page measures the agents' memory and, at level B, the speed and
weight of the base from one version to the next. The answers a bot gives on a
project's documents (the RAG pipelines and plugins) have their own page:
[RAG benchmarks](rag-benchmarks.md).

## Setup

- **Vault** — 87 notes of a fictional team: `tidewater`, an event
  ticketing platform (architecture, conventions, 12 ADRs, 20 plans, skills,
  bugs, incidents, meetings, log), `atlas`, an analytics project whose ADR
  and bug numbers overlap with tidewater's, and shared `global` skills. A
  few notes are in Italian, as happens in a mixed team.
- **Questions** — 50, in seven categories: lexical (10), typed lookups such
  as "status of BUG-017" (8), multi-hop (7), temporal (5), **paraphrase**
  (12: the words of the question do not appear in the note), cross-lingual
  (4) and negative (4).
- **Leakage control** — the queries used at level R were written by an agent
  that saw only the question text, never the vault, and are used verbatim.
  Questions and gold notes were frozen before the first run.

## Level R — retrieval (2026-09-25)

`memory_search` over the benchmark vault, top 10, 45 questions (negatives
and list questions belong to level A only). *R@k*: share of questions with
a gold note in the first *k* hits; *MRR*: mean reciprocal rank of the first
gold note.

| Mode | R@1 | R@5 | MRR | Misses |
|---|---|---|---|---|
| `bm25` — text only | 0.44 | 0.69 | 0.55 | 12 |
| `ranked` — title and structural factors | 0.47 | 0.71 | 0.56 | 12 |
| `ranked` + `any_of` phrasings | 0.71 | 0.93 | 0.80 | 3 |

Before query stemming (v2.34.0) the same three rows read R@5 0.62, 0.67
and 0.91 (R@1 0.44, 0.44, 0.73).

R@5 by category:

| Category | n | bm25 | ranked | ranked + any_of |
|---|---|---|---|---|
| lexical | 10 | 0.70 | 0.70 | 1.00 |
| typed | 7 | 0.86 | 1.00 | 1.00 |
| multi-hop | 7 | 1.00 | 1.00 | 1.00 |
| temporal | 5 | 1.00 | 1.00 | 1.00 |
| paraphrase | 12 | 0.50 | 0.50 | 0.75 |
| cross-lingual | 4 | 0.00 | 0.00 | 1.00 |

What it says:

- **The agent's own vocabulary is the biggest lever.** Letting the agent
  pass a few phrasings in one call (`any_of`) lifts R@5 by about 0.2 and
  solves every cross-lingual question. The server stays lexical; the model
  already knows that "fattura cartacea" means "printed invoice".
- **Query stemming fixes morphology, at a small cost.** Each query word
  also matches the indexed words with the same Porter stem ("retry" finds
  "retries" and "retried", not "retrieval"; "naming" finds "named"), on
  top of what the word alone matches, so no search loses a result. It
  lifts single-query R@5 by 0.04; with `any_of` the extra forms add some
  noise to the fusion (R@1 0.73 → 0.71). Two rejected variants: stemming
  the index (the FTS5 `porter` tokenizer) lost matches on the maintainers'
  real vault — "deploy" stopped matching notes that only say
  "deployment" — and matching the bare stem as a prefix let "retry" reach
  "retrieval".
- **Structure helps less than text.** Title and structural factors move
  typed and multi-hop lookups to the top (R@5 1.00) and add a little to
  R@1 and MRR.
- **Three misses remain, all true paraphrases** with no word in common
  with the note — "password reset" against "account-recovery", "nightly
  job" against "runs at 02:00", "card fee" against "service charge". Only
  a semantic signal would find them.

Raw data: [`bench/results/`](../bench/results/).

## Level A — agents

Headless Claude Code sessions (Sonnet, `claude -p`, isolated with
`--restricted --strict-mcp-config`) answer the questions in two
configurations: **fs** — the vault as working directory with Read, Grep
and Glob only; **gosidian** — no file tools, the same vault served by
gosidian over MCP, `memory_bootstrap` first as a project's CLAUDE.md stub
asks. Answers are scored against the expected facts.

### A backlog as sections and as a database (2026-10-27) — 22 questions, two runs each

Database notes exist so that "what is still open?" is one `memory_query`
instead of reading a long note. `bench/backlog` writes one backlog of 180
entries both ways, with the same facts: **sections**, one note with a
`## IMP-001 — title` section per entry and its status, priority, area,
owner and dates as a list (43 KB); **database**, the same path as a
database note over one note per entry, those fields in the frontmatter.
The 22 questions ask for one entry (lookup), how many (count), which ones
(list, two to six entries), which is the most (aggregate), and two with no
answer (negative). v2.42.0 is the last version before database notes; on
the sections it is the backlog as it was then. The `gosidian`
configuration, Sonnet, Claude Code 2.1.311; the three combinations ran
side by side on one machine.

| | v2.42.0, sections | v2.119.0, sections | v2.119.0, database |
|---|---|---|---|
| Correct | 44/44 | 44/44 | 44/44 |
| Turns, mean | 4.8 | 5.2 | 3.7 |
| Time, mean | 14.0 s | 15.9 s | 12.7 s |
| Cached input · cache writes, mean | 120k · 30.7k | 181k · 33.6k | 120k · 11.8k |
| API-price estimate, per session | $0.140 | $0.159 | $0.064 |
| Sessions that used `memory_query` · read the backlog note | 15 · 39 | 24 · 38 | 38 · 6 |

Per session, by category (API-price estimate · turns):

| Category | v2.42.0, sections | v2.119.0, sections | v2.119.0, database |
|---|---|---|---|
| lookup | $0.043 · 4.3 | $0.056 · 3.7 | $0.062 · 4.3 |
| count | $0.155 · 4.4 | $0.175 · 4.8 | $0.061 · 3.0 |
| list | $0.155 · 4.7 | $0.178 · 5.6 | $0.063 · 3.2 |
| aggregate | $0.155 · 4.5 | $0.177 · 5.2 | $0.067 · 3.8 |
| negative | $0.161 · 7.0 | $0.157 · 6.8 | $0.072 · 6.0 |

What it says:

- **Every answer is right in all three**, also on the sections: the model
  reads the 43 KB note whole and counts 50 open entries out of 180
  without a slip. At this size the database does not make answers more
  correct. A backlog with long bodies would not fit in one read; this
  benchmark does not measure that.
- **The database costs less than half**: $0.064 a session against $0.140
  and $0.159, a turn and a half less, and a third of the cache writes,
  because the rows come back from one `memory_query` instead of a 43 KB
  read. On counts, lists and aggregates it is 2.5 times cheaper.
- **Looking up one entry costs more with the database** ($0.062 against
  $0.043): a search and a row instead of one section.
- **v2.119.0 on the sections costs 13% more than v2.42.0**: a larger tool
  list (181k cached input against 120k), and an agent that tries
  `memory_query` first in 24 sessions of 44, gets nothing from a backlog
  without frontmatter, and reads the note anyway. The version's gain
  comes from the database notes, used.
- The 16 answers to read by hand (the negatives, and the answers that
  name a near miss of a list) are all correct: the near misses were named
  to say why they were left out.
- Times are of three runs side by side, so take them as a rough guide;
  turns and tokens do not depend on it.

Raw results: `bench/results/agents-2026-10-27-backlog.jsonl`.

### By configuration (2026-09-26) — 50 questions, one run each

gosidian's default serves the full memory; every setting that saves tokens
by leaving context out is an opt-in, per project (ADR-027 in the
maintainers' vault: memory first, tokens second). Each configuration is
measured on its own:

| Configuration | Correct | Turns | Time | Cached input | Cache writes | API-price estimate per session | What it gives up |
|---|---|---|---|---|---|---|---|
| fs — files only, no gosidian (2026-09-25, 150 sessions) | 97% | 5.0 | 8.9 s | 31k | 2.4k | $0.022 | ranking, orientation, access control, shared memory |
| fs-oriented — files plus gosidian's orientation (2026-09-26, 100 sessions) | 100% | 5.0 | 10.7 s | 31k | 2.4k | $0.022 | ranking, access control, shared memory, freshness (it reads a copy) |
| mirror — the local mirror as shipped, files only (2026-09-26, 50 sessions) | 100% | 4.9 | 10.8 s | 31k | 2.8k | $0.024 | as fs-oriented; the copy is kept in sync and the server keeps access control |
| mirror-mcp — the mirror plus the MCP server, as deployed (2026-09-26, 50 sessions) | 100% | 6.0 | 13.2 s | 58k | 9.6k | $0.058 | — |
| gosidian, default | 100% | 4.6 | 8.6 s | 93k | 10.0k | $0.063 | — |
| gosidian, `core` tool profile | 100% | 4.4 | 9.4 s | 54k | 9.3k | $0.052 | admin and advanced tools (lint, compact, scaffold, uploads…) |
| gosidian, `lean_read_bootstrap` | 100% | 4.4 | 9.0 s | 85k | 6.1k | $0.045 | for read-only tokens: the writing rules and note-format notes of the directives |
| gosidian, `core` + `lean_read_bootstrap` | 100% | 4.8 | 9.2 s | 55k | 6.5k | $0.042 | both of the above |

- The `lean_read_bootstrap` flag cuts the bootstrap from about 3,850 to
  2,100 tokens for tokens that cannot write, and the cost per session by
  about 28% (33% with the `core` profile).
- **Accuracy is saturated on this vault**: every gosidian configuration
  answers all 50 questions. The benchmark therefore cannot show what the
  lean settings lose — context about how the memory is organized, which
  matters more on a large, real vault than on 87 notes. That is why they
  are opt-in.
- **Files plus orientation match gosidian on this vault, at a third of
  the cost.** `fs-oriented` gives the files-only agent what a gosidian
  project tells its agents — start from `hot.md` and `README.md`, follow
  tags and wikilinks, try synonyms and the other language before giving
  up — and answers all 100 sessions correctly (the files-only control of
  the same day: 98%, the miss being "forgotten password emails" twice).
  On 87 notes, reading is not where gosidian's value lies: shared
  memory, access control, audit and the web UI are. Whether it holds on
  a large vault, where `grep` returns hundreds of files, is measured
  below; this result is the premise of the local read-only mirror
  (`gosidian mirror`, opt-in per project), also measured below.
- Since 2026-09-26 search snippets are twice as long (24 tokens): the
  only wrong gosidian answer of the full run below — taken from a snippet
  that cut the fact in half — does not recur.

### Scale (2026-09-26) — the same questions on a project seven times larger

`bench/scale` adds 420 noise notes to `tidewater` (plans, meetings,
support articles, research; some in Italian) that never touch what the
questions depend on: the project grows from 67 to 489 notes, from 57 KB
to about 490 KB of text. One run of each configuration:

| Configuration | Vault | Correct | Turns | Cached input | Cache writes | API-price estimate per session |
|---|---|---|---|---|---|---|
| fs-oriented | 87 notes | 100% | 5.0 | 31k | 2.4k | $0.022 |
| fs-oriented | 507 notes | 98% | 5.3 | 35k | 3.3k | $0.027 |
| gosidian, default | 87 notes | 100% | 4.6 | 93k | 10.0k | $0.063 |
| gosidian, default | 507 notes | 100% | 4.7 | 94k | 10.4k | $0.065 |

Reading files gets dearer as the project grows (+23%), gosidian barely
moves (+3%): the gap narrows from about 2.9× to 2.4×. The larger vault
also cost the files-only agent one answer — lost among unrelated notes
about retries. Level R is unchanged on the larger vault (R@5 0.69 /
0.71 / 0.93). A caveat: the generated noise shares little vocabulary with
the questions ("Paylane" never appears in it), so `grep` output grew less
than it would on a real project, where a common term can match hundreds
of notes; the +23% is a lower bound.

### A real vault (2026-09-26) — private, aggregate figures only

The same two configurations on a copy of the maintainers' own vault: 678
notes in 17 projects, mostly in Italian, 13 questions in Italian about one
project (lexical, typed, multi-hop, temporal, paraphrase, cross-lingual,
negative), two runs each. Questions, answers and raw data stay private;
the copy was deleted after the run. Answers checked by hand.

| Configuration | Correct | Turns | Time | API-price estimate per session |
|---|---|---|---|---|
| fs-oriented | 96% (25/26) | 5.9 | 14.6 s | $0.067 |
| gosidian, default | 92% (24/26) | 5.5 | 12.8 s | $0.130 |

- **The gap keeps narrowing as the vault gets real**: 2.9× on the
  benchmark vault, 2.4× on the scaled one, 1.9× here. Both
  configurations cost more than on the benchmark: the real project's
  `hot.md` and bootstrap are large, and so are some notes (an ADR log of
  85 KB, an activity log of 120 KB) the files-only agent has to read.
- **Accuracy is comparable.** gosidian picked the wrong one of two
  similar skills twice; the files-only agent once quoted a figure from an
  older note that a newer one had superseded — gosidian's ranking, which
  favours recent notes, returned the current one. A copy of the files
  does not know which note is newer unless it is told.

### Local mirror (2026-09-26) — the shipped pieces, one run each

The harness syncs the benchmark vault with the real `gosidian mirror sync`
(read-only files, `_index.md` — `.gosidian-index.md` since v2.72.1 —,
`MIRROR.md`) and takes the orientation
from the real Claude Code hook's SessionStart context. Two setups:
`mirror`, file tools only on every project's mirror; `mirror-mcp`, as a
user would run it — the current project mirrored, the MCP server
configured too (schemas loaded on demand through ToolSearch), the stub's
"memory_bootstrap first" and the hook's whole context. Sessions now
record their tool calls.

| Configuration | Correct | Turns | Cached input | Cache writes | API-price estimate per session |
|---|---|---|---|---|---|
| fs-oriented (reference) | 100% | 5.0 | 31k | 2.4k | $0.022 |
| mirror | 100% | 4.9 | 31k | 2.8k | $0.024 |
| mirror-mcp, all sessions | 100% | 6.0 | 58k | 9.6k | $0.058 |
| — the 17 that read the mirror without `memory_bootstrap` | | 4.4 | 36k | 3.8k | $0.029 |
| — the 33 that called `memory_bootstrap` first | | 6.8 | 70k | 12.7k | $0.074 |
| gosidian, default (reference) | 100% | 4.6 | 93k | 10.0k | $0.063 |

- **The mirror reproduces files plus orientation**: same accuracy, the
  cost within 10% (the hook's orientation is a little longer). The
  recency index (`_index.md`) has nothing to show here: `fs-oriented`
  already answers every temporal question of this vault.
- **As deployed, the saving depends on the bootstrap, not on the
  reads.** Two sessions in three still start with `memory_bootstrap`, as
  the stub asks and the hook reminds; those cost as much as gosidian with
  deferred tool loading, although 20 of the 33 then read only the mirror
  (3.1 file-tool calls per session against 0.5 calls to other MCP tools
  overall). Sessions that go straight to the mirror cost what files only
  cost. On average the deployed setup is 8% below gosidian's default.
- One question per session puts the whole bootstrap — about 3,850
  tokens here, 60% of it the directives an agent needs to write — on a
  single answer. Sessions with several questions are measured next.

### Several questions per session (2026-09-26)

A working session bootstraps once and looks things up several times.
`-per-session 5` asks the 50 questions in 10 sessions of five, each
mixing categories, every answer scored on its own. Three setups with the
hook's context: `hooks-mcp` (MCP, mirror off), `mirror-mcp` (MCP and the
mirror) and `mirror` (the mirror alone, no MCP).

| Configuration | Correct | Turns | Tool calls: files / MCP | Cached input | Cache writes | Per session | Per question | Per question, one per session |
|---|---|---|---|---|---|---|---|---|
| hooks-mcp | 96% (48/50) | 19.0 | 0.3 / 16.5 | 169k | 20.1k | $0.141 | $0.028 | — |
| mirror-mcp | 100% | 15.2 | 10.6 / 2.4 | 141k | 20.6k | $0.138 | $0.028 | $0.058 |
| mirror | 100% | 13.5 | 12.5 / 0 | 67k | 11.9k | $0.085 | $0.017 | $0.024 |

- **Five questions per session halve the cost per question with MCP**
  ($0.058 → $0.028): most of gosidian's cost is paid once per session.
- **The mirror does not make reads cheaper.** With the MCP server there,
  reading the mirror (10.6 file-tool calls per session) costs what
  reading through MCP costs (16.5 calls): $0.138 against $0.141. `Grep`
  and `Read` bring about as much text into the conversation as
  `memory_search` and `memory_get`.
- **The difference is fixed per session**: $0.053 between `mirror-mcp`
  and `mirror`, i.e. the bootstrap and the tool schemas loaded through
  ToolSearch, both carried in every later turn. Only an agent that does
  not use MCP at all saves it. On two of the maintainers' projects the
  bootstrap is 5,000 and 7,300 tokens, 43% and 60% of it content a mirror
  also holds (`hot.md`, README, skills, recent notes, plans).
- `hooks-mcp` missed two answers the mirror sessions got — a paraphrase
  ("free tickets for journalists" → comps) and half of a retry window;
  two questions are too few to call it a difference.

### Full run (2026-09-25) — 50 questions, three runs each

450 sessions with 12-token snippets. gosidian-core is the same setup with
a token restricted to the 20-tool `core` profile. Negative answers were
checked by hand; the answer patterns were widened where a correct phrasing
had been rejected (see the gold revisions in `bench/README.md`).

| Configuration | Correct | Turns (avg) | Time (avg) | Cached input | Cache writes | Output | API-price estimate per session |
|---|---|---|---|---|---|---|---|
| fs | 97% (146/150) | 5.0 | 8.9 s | 31k tokens | 2.4k | 614 | $0.022 |
| gosidian | 99% (149/150) | 5.1 | 8.5 s | 105k tokens | 9.5k | 485 | $0.064 |
| gosidian-core | 99% (149/150) | 5.0 | 8.6 s | 62k tokens | 9.5k | 503 | $0.055 |

Correct answers by category:

| Category | fs | gosidian | gosidian-core |
|---|---|---|---|
| lexical | 100% | 97% | 97% |
| typed | 100% | 100% | 100% |
| multi-hop | 100% | 100% | 100% |
| temporal | 100% | 100% | 100% |
| paraphrase | 89% | 100% | 100% |
| cross-lingual | 100% | 100% | 100% |
| negative | 100% | 100% | 100% |

What it says:

- **Both approaches answer almost everything on a vault this size.** The
  difference is in the paraphrases: the filesystem agent searches for the
  words of the question, finds nothing and concludes the vault has no
  answer ("forgotten password emails" 0/3, "shipping code on Fridays"
  2/3); the gosidian agent searches again with other words and finds the
  note.
- **gosidian costs about three times as much per session**, at the same
  number of turns and the same speed. The cost is mostly cache writes:
  the bootstrap payload (about 3,800 tokens, most of it the working
  directives) and the search results enter every conversation. The tool
  schemas matter less: the `core` profile reads 40% fewer tokens but
  costs only 14% less, because cached reads are cheap.
- **Deferred tool loading does not change the bill.** With Claude Code's
  ToolSearch available (10 questions, one run), the client loads the
  gosidian schemas on demand: cached reads halve (126k → 61k on those
  questions), but the fetched schemas enter the conversation as writes
  (10.2k → 13.5k) and each session takes almost one more turn. Estimated
  cost per session: $0.074 against $0.072.

Pilot of 10 questions (same day): 9/10 correct in both fs and gosidian
configurations; the raw data of every run is in `bench/results/`.

## Level B — the base across versions (2026-10-27)

The base — the vault, the index and search, the MCP server, the web UI —
measured from outside by `bench/base`: each version's image in a container
with 2 CPUs and 2 GB, on a copy of the benchmark vault, three runs each,
on one machine in the same hour. v2.34.0 is the version of the first
benchmark (2026-09-25), v2.42.0 the last before database notes
(2026-10-02), v2.118.0 the first release of the weekly public train,
v2.119.0 the current one. Latency percentiles are over all the calls of
the three runs; the other figures are the median of the runs. Sizes are
in KB and MB of 1,000, memory in MiB.

| | v2.34.0 | v2.42.0 | v2.118.0 | v2.119.0 |
|---|---|---|---|---|
| Date, runs, load | 2026-10-27, 3, 1.3→2.6 | 2026-10-27, 3, 2.6→1.6 | 2026-10-27, 3, 1.6→1.3 | 2026-10-27, 3, 1.3→1.4 |
| **Results** |  |  |  |  |
| Search, one query: R@1 · R@5 · MRR · misses | 0.44 · 0.67 · 0.53 · 14 | 0.47 · 0.71 · 0.56 · 12 | 0.47 · 0.71 · 0.56 · 12 | 0.47 · 0.71 · 0.56 · 12 |
| Search with any_of: R@1 · R@5 · MRR · misses | 0.73 · 0.91 · 0.81 · 4 | 0.73 · 0.93 · 0.82 · 3 | 0.73 · 0.93 · 0.82 · 3 | 0.73 · 0.93 · 0.82 · 3 |
| **What an agent receives** |  |  |  |  |
| Tools, full · core | 57 · 20 | 58 · 21 | 71 · 27 | 71 · 27 |
| tools/list, full · core | 61.6 KB · 23.2 KB | 68.3 KB · 27.0 KB | 93.5 KB · 38.3 KB | 93.5 KB · 38.3 KB |
| Bootstrap of the project | 15.5 KB | 17.4 KB | 25.2 KB | 25.2 KB |
| A search's result, mean | 0.9 KB | 1.3 KB | 1.3 KB | 1.3 KB |
| **Speed** |  |  |  |  |
| Start, cold · warm | 1746 ms · 1588 ms | 272 ms · 211 ms | 451 ms · 210 ms | 498 ms · 215 ms |
| bootstrap, p50 · p95 | 1.68 ms · 2.35 ms | 1.75 ms · 2.50 ms | 2.96 ms · 3.65 ms | 2.82 ms · 3.43 ms |
| search, p50 · p95 | 0.74 ms · 1.11 ms | 1.11 ms · 1.68 ms | 1.12 ms · 1.73 ms | 1.08 ms · 1.65 ms |
| get, p50 · p95 | 0.26 ms · 0.36 ms | 0.26 ms · 0.35 ms | 0.44 ms · 0.62 ms | 0.42 ms · 0.60 ms |
| list_notes, p50 · p95 | 0.65 ms · 0.86 ms | 0.65 ms · 0.91 ms | 0.65 ms · 0.85 ms | 0.65 ms · 0.90 ms |
| query, p50 · p95 | — | 1.36 ms · 1.64 ms | 1.26 ms · 1.58 ms | 1.25 ms · 1.62 ms |
| create, p50 · p95 | 3.78 ms · 18.00 ms | 0.65 ms · 0.88 ms | 4.01 ms · 7.09 ms | 4.04 ms · 7.91 ms |
| edit, p50 · p95 | 4.07 ms · 17.94 ms | 0.75 ms · 3.41 ms | 4.06 ms · 5.17 ms | 4.02 ms · 4.98 ms |
| append, p50 · p95 | 5.83 ms · 15.02 ms | 0.70 ms · 0.86 ms | 4.00 ms · 4.96 ms | 4.01 ms · 6.42 ms |
| Large vault: notes | 5087 | 5087 | 5087 | 5087 |
| Large vault: start, cold · warm | 18733 ms · 18302 ms | 5092 ms · 294 ms | 5766 ms · 312 ms | 5723 ms · 296 ms |
| Large vault: search, p50 · p95 | 1.15 ms · 3.58 ms | 1.76 ms · 4.95 ms | 1.58 ms · 4.63 ms | 1.63 ms · 4.75 ms |
| **Resources** |  |  |  |  |
| Memory at rest: own · page cache | 13.0 MiB · 4.4 MiB | 13.8 MiB · 4.5 MiB | 16.4 MiB · 4.9 MiB | 16.9 MiB · 4.9 MiB |
| Memory after the calls: own · peak | 14.5 MiB · 27.8 MiB | 15.1 MiB · 28.1 MiB | 20.3 MiB · 31.3 MiB | 20.0 MiB · 32.9 MiB |
| Large vault: memory at rest, own | 17.3 MiB | 18.9 MiB | 21.4 MiB | 21.4 MiB |
| CPU at rest, % of a core | 0.23 | 0.23 | 0.28 | 0.27 |
| CPU per call | 882 µs | 966 µs | 1225 µs | 1204 µs |
| Image · binary | 89.0 MB · 41.4 MB | 89.8 MB · 41.9 MB | 126.0 MB · 70.7 MB | 126.0 MB · 70.7 MB |
| State after the start · index | 4509.7 KB · 262.1 KB | 4583.4 KB · 356.4 KB | 5025.8 KB · 397.3 KB | 5025.8 KB · 397.3 KB |
| Large vault: index | 11.1 MB | 15.5 MB | 17.3 MB | 17.3 MB |
| Web UI, first load: files · sent · gzipped | 6 · 401.0 KB · 123.2 KB (not compressed by the server) | 6 · 401.5 KB · 123.3 KB (not compressed by the server) | 14 · 590.1 KB · 166.9 KB (not compressed by the server) | 14 · 590.1 KB · 166.9 KB (not compressed by the server) |
| **Code** |  |  |  |  |
| Go lines: base · branches · tests | 33,001 · 0 · 22,685 | 38,086 · 0 · 27,867 | 68,114 · 19,265 · 59,733 | 68,115 · 19,411 · 59,882 |
| Web UI lines | 9,898 | 10,701 | 23,027 | 23,027 |
| Dependencies: Go direct + indirect · npm + dev | 12 + 26 · 21 + 23 | 12 + 26 · 21 + 23 | 17 + 23 · 23 + 23 | 17 + 23 · 23 + 23 |

What it says:

- **Search results have not regressed.** v2.34.0 gives the level R
  figures recorded before query stemming (R@5 0.67 and 0.91), the later
  versions those after it (0.71 and 0.93): measured from outside, the
  search agrees with the in-process level R. Nothing has moved since
  2026-10-02.
- **What an agent receives grew by half.** The tool list went from
  61.6 KB to 93.5 KB (57 to 71 tools; the `core` profile from 23.2 to
  38.3 KB), the bootstrap of the project from 15.5 to 25.2 KB: about
  41 KB more at the start of every session. Cached reads keep it cheap in
  money (level A above) but not in context.
- **Writes are slower by choice.** Since v2.103.0 a write is synced to the
  disk before it returns (`vault.durability = batch`, the default): 4.0 ms
  against 0.7 ms on v2.42.0. With `none` the same writes take 1.0–1.2 ms
  (`base-2026-10-27-v2.119.0-durability-none.json`); the rest of the gap
  is a longer write path.
- **Reads cost a little more**: `memory_get` 0.26 → 0.42 ms,
  `memory_bootstrap` 1.75 → 2.82 ms (a larger reply), server CPU per call
  966 → 1,204 µs.
- **The server weighs more**: binary 41.9 → 70.7 MB, image 89.8 → 126 MB,
  its own memory at rest 13.8 → 16.9 MiB, a cold start 0.27 → 0.50 s on
  87 notes and 5.1 → 5.7 s on 5,087. Warm starts have not changed (0.2 s
  and 0.3 s); v2.34.0 rescanned the whole vault at every start.
- **The web UI's first load grew from 401 KB to 590 KB** (14 files instead
  of 6), and every version sends it uncompressed: gzipped it is 167 KB.
- **The code**: the base from 38,086 to 68,115 lines of Go in 25 days,
  plus 19,411 in the branches' own packages (pipelines, plugins, the IN
  folder, chat bots, agent teams); the tests from 27,867 to 59,882; the
  web UI from 10,701 to 23,027 lines.

Raw data: `bench/results/base-*.json`.

### The branches off (2026-10-27)

A branch — the pipelines, the plugins, the IN folder, the chat bots, the
agent teams — is off by default and must cost nothing while off. Measured
on v2.119.0 with the defaults, with the pipelines on (and nothing to run),
and on the change that hides the branches' tools while their branch is
off:

| | v2.119.0 | v2.119.0, pipelines on | branches' tools hidden |
|---|---|---|---|
| Tools, full · core | 71 · 27 | 71 · 27 | 67 · 27 |
| tools/list, full · core | 93.5 KB · 38.3 KB | 93.5 KB · 38.3 KB | 85.8 KB · 38.3 KB |
| Memory at rest, own | 16.9 MiB | 16.7 MiB | 16.9 MiB |
| CPU at rest · per call | 0.27% · 1204 µs | 0.29% · 1326 µs | 0.27% · 1223 µs |
| Start, cold · warm | 498 ms · 215 ms | 474 ms · 215 ms | 480 ms · 211 ms |

- **The schemas were the cost**: `memory_in_upload` (3.0 KB),
  `memory_pipeline_run` (1.9), `memory_create_team` (1.3) and
  `memory_pipelines` (1.3) were listed with their branch off, 8% of the
  tool list. They are now listed only while their branch is on.
- **The rest is below the noise.** The branches' packages keep 0.32 MB of
  heap after their init and take 0.75 ms of the 25 ms of package init
  (`GODEBUG=inittrace=1`); the IN folder keeps `in.db` open and wakes
  every 20 s, with nothing to scan. Run to run, latency and CPU per call
  move by 10–15% on this machine: the pipelines-on run had a load of up to
  3.7.

### Code highlighting (2026-10-27)

The server highlights the code blocks of a preview with chroma. Measured
on `main` and on the change that drops chroma's styles, nine runs of each
image in three alternating rounds (medians; memory at rest moves by about
±0.7 MiB run to run), and with `BenchmarkRenderer_CodeBlocks`, which
renders `internal/parser/testdata/code_blocks.md` (41 blocks, four of them
in a language chroma does not know; `benchstat`, n=10):

| | `main` | chroma's styles dropped | and the lexers remembered |
|---|---|---|---|
| Memory own, at rest · after the calls | 16.9 · 19.9 MiB | 16.3 · 19.4 MiB | |
| Start, cold · warm | 475 ms · 207 ms | 452 ms · 210 ms | |
| Binary | 70.7 MB | 70.5 MB | |
| Rendering the 41 blocks | 26.7 ms · 4.1 MiB · 48.8k allocs | 25.4 ms · 3.4 MiB · 31.7k allocs | 3.7 ms · 3.5 MiB · 31.8k allocs |

- **The styles were waste.** `goldmark-highlighting` imported chroma's
  styles, which parse all their themes at every start (5–8 ms and 1.9 MB
  of allocations, `GODEBUG=inittrace=1`), to pick colours that classes
  leave out: with classes the HTML is the same under any style. The
  formatter also turned the whole style into CSS for every block. The
  start's 23 ms are not all theirs: a later run of the final image, against
  the branches' change, started in 476 ms against 480, within the noise.
- **An unknown language was slow.** chroma looks a language up by name and
  alias, then as a file extension against the file names of its 279
  lexers: about 5 ms for `mermaid` or `view`, at every render, 80% of the
  benchmark's time. The renderer now keeps the answer for up to 256 short
  names.
- **The lexers stay.** chroma registers all of them at start (5–7 ms,
  2.8 MB of allocations; in a program of their own, 1.8 MiB of memory and
  4.6 MB of binary), and has no registry that loads them on demand. They
  are what highlights the code.

### A reader's scope (2026-10-28)

A preview with a ```` ```view ```` block cost 27 ms on the lab, whatever
the view found: a CPU profile put two thirds of it in listing the vault's
projects, to keep a reader who is not the owner to those it may see, and
the rest in the view's query. Measured on the lab (29,265 notes, 26,000 of
them in one project; the member `claude`, median of 30 requests over HTTP)
and with `BenchmarkProjects` and `BenchmarkQuery_ScopedFolderNoMatch` on
30,000 synthetic notes in 30 projects (n=6):

| | before | after |
|---|---|---|
| Lab: a preview with a view that finds nothing · with two | 27.4 · 55.3 ms | 1.9 · 3.0 ms |
| Lab: a preview with a view of `gosidian/plans` | 29.1 ms | 3.0 ms |
| Lab: a search · the query of a folder | 22.5 · 28.8 ms | 5.1 · 2.5 ms |
| Listing the projects | 11.2 ms | 0.55 ms |
| The query of a view that finds nothing: the owner · a member who sees 29 projects | 1.9 · 37 ms | 0.03 · 0.26 ms |
| The latest notes of 29 projects · of 3 | 37.6 · 5.7 ms | 2.8 · 0.3 ms |
| The notes of a project in path order: of 25,500 · of 155 | 25 · 3.0 ms | 27.5 · 0.2 ms |

- **The projects were listed by reading every note.** The query cut every
  path at its first slash and sorted the lot, which on the lab spilled to
  disk. It now walks the unique index on the paths, one lookup a project.
- **No path `LIKE` used an index.** A scope is a `path LIKE 'p/%'` for each
  project the reader sees, a view's `from:` is one more, and every query
  of a project is one. `LIKE` ignores case, and an index serves it only in
  NOCASE order: the unique index on the paths is in binary order. The new
  `notes_path_nocase` (2.9 MB on the lab's notes, 0.5% of its index;
  created in 61 ms) lets SQLite search the range, while the `LIKE` still
  decides. On a copy of the lab's index, 134 results of these queries are
  the same with the index and without it.
- **Ties had no order.** The recent and stale lists ordered by time alone,
  and an import writes thousands of notes at the same time: their order
  was whatever the plan read first. The path now orders a tie.
- **One query is slower.** The notes of a project in path order (the graph
  of a project, `NotesByPrefix`) now search the range and sort it, where
  they read the unique index in order: 10% slower on the project of
  25,500 notes, fifteen times faster on a small one.
- **The base does not move.** Level B, six runs of v2.120.0 and of the
  change in alternating rounds (`bench/results/base-2026-10-28-*.json`):
  search results, tools and bootstrap are the same, memory and the latency
  of the everyday tools within the noise. The large vault's index is 1.9%
  larger (17.3 to 17.6 MB) and its cold start 2.5% slower (5.67 to 5.82 s,
  runs from 5.59 to 5.85 s against 5.72 to 6.44): every write of its 5,087
  notes now also writes the index. The p95 of its search came out 11%
  higher (4.25 to 4.73 ms), with ranges that overlap; in one process, the
  same 45 queries run 1,800 times with the index and without it, in
  alternating rounds, take the same time (p50 1.03 ms, p95 3.5 ms either
  way): the owner's search tests no path `LIKE`.

### The guard (2026-10-29)

From the 29th of October level B guards the base: every night on the
night's image, and before a release on the image of the commit it tags
(`bench/base/guard.sh`, see `bench/README.md`). Each number has a
reference, a room the wrong way and, for some, an objective
(`bench/base/reference.json`). The rooms come from the noise of twelve
single runs of the 28th, the same code measured again and again:

| | Noise (max − min, % of the median) | Worse past |
|---|---|---|
| Search results; bytes of the tools, the bootstrap, the web UI | none | any fall, any byte more |
| Start, 87 notes, cold and warm | 8–9% | +15% |
| Start of 5,087 notes, cold (the whole index) · warm | 15% · 9% | +20% · +15% |
| Reads, p50 (`get`, `list_notes`, `search`, `query`, `bootstrap`) | 12–21% | +25%, at least +0.1 ms |
| Reads, p95 | 16–30% | +40%, at least +0.3 ms |
| Search of 5,087 notes, p50 · p95 | 18% · 28% | +25% · +40% |
| Writes, p50 | 4–40%: some runs 2.9 ms, most 4.0 | +25%, at least +1 ms |
| Writes, p95 | 36–62% | not guarded |
| Memory at rest and peak · CPU per call | 4–5% · 12% | +10% · +20% |
| Image · binary | none | +2% |

The rooms are about twice the noise of a single run. The guard measures
three runs and takes the median of each number, the latency percentiles
included: from the 29th each run has its own and the results hold their
median, where before they were over all the calls of the runs. Other
projects share the machine, and on its first try the guard met a run
twice as slow in everything, its CPU per call too: over all the calls,
that one run pushed reads at the p95 past their room. A number past its
limit also counts only when a second measure finds it again. The two
nightly runs before the guard measured with a load of 6.5, left by the
stages before them: the guard first waits for the load to go under 4.

The objectives are where time and tokens are felt: the whole index of a
large vault (5.7 s, ≤ 3.5 s), the search of 5,087 notes at the p95
(4.3 ms, ≤ 3 ms), the web UI's first load (590 KB sent, ≤ 200 KB, which
compressing it reaches), the bootstrap (25.2 KB, ≤ 20 KB), the full tool
list (85.8 KB, ≤ 80 KB), the search with one query (R@5 0.71, ≥ 0.76, the
two lexical questions it misses) and with variants (R@1 0.73, ≥ 0.80;
3 misses, ≤ 2). Under 5 ms a call's time is lost in the model's seconds:
those numbers are kept, not chased.

### The web UI gzipped (2026-10-29)

No version of the server compressed the web UI: its first load (the page,
its script and stylesheets, the modules it preloads) was 590 KB sent. Its
text assets now go gzipped to a client that takes them, each compressed
once at its first request and kept: no work at the start, no larger
binary. Level B: before, the guard's reference (the medians of twelve runs
of the 28th); after, three runs of the change:

| | before | after |
|---|---|---|
| Web UI, first load: files · sent | 14 · 590.1 KB | 14 · 161.6 KB |
| Memory at rest, own · peak | 17.1 · 32.9 MiB | 16.2 · 32.1 MiB |

The other numbers stay within their rooms. The bench counted the bytes
of the first load decompressed until then, which was the same while the
server sent nothing gzipped; it now counts the bytes that came.

### The boot scan (2026-10-29)

A cold start indexes the whole vault before the server answers: on the
large vault of level B, 5,087 notes, that took 5.7 s, against an
objective of 3.5. A CPU profile of one such start
(`GOSIDIAN_PPROF_ADDR`) said where the time went:

- **A transaction a note.** 5,087 commits, each writing its pages to the
  WAL and flushing an FTS segment that later merges undid: 38% of the
  scan's CPU. The scan now writes 256 notes to a transaction
  (`Index.UpsertMany`).
- **One goroutine.** Reading the files and parsing them (the frontmatter,
  links, tags, fields, the FTS text) needs no database, but waited for
  the writes: after the batches, 40% of the scan. `index.Extract` now does
  that part, and the scan reads and extracts the next 256 notes on every
  core while it writes the ones before, in the order of the paths. The
  rows, ids included, are those of indexing the notes one at a time
  (`TestVault_ScanStoresWhatUpsertsDo`).
- **The memory it leaves.** Parsing on every core allocates faster than
  Go's scavenger returns what is freed: the server at rest kept 1.5–5.6
  MiB of heap it no longer used. It hands them back after the scan
  (`debug.FreeOSMemory`).

In one process, `BenchmarkScanInto` on the same vault with 2 CPUs, ten
runs each (`benchstat`):

| | Scan of the large vault |
|---|---|
| main | 5.94 s |
| 256 notes to a transaction | 2.46 s (−58.7%) |
| the next batch read while one is written | 1.71 s (−30.3%) |
| a restart on an unchanged vault (`BenchmarkScanIntoWarm`) | 107.7 → 74.5 ms (−30.9%) |

Level B: before, the guard's reference; after, three runs of the change
(`bench/results/base-2026-10-29-boot-scan.json`):

| | before | after |
|---|---|---|
| Large vault, start: cold · warm | 5,746 · 295 ms | 2,036 · 271 ms |
| Large vault, memory at rest | 20.8 MiB | 21.2 MiB |
| Start, 87 notes: cold · warm | 456 · 206 ms | 415 · 209 ms |

The large vault's cold start moves its reference (runs from 1,991 to
2,057 ms); the other numbers stay within their rooms. The peak memory
came out lower in both measures of the change (32.9 MiB in the
reference, 29.9 and 29.2 MiB here) and keeps its reference until the
reason is clear. The frontmatter is still read as YAML five times a
note, about a fifth of the extraction, which now runs beside the writes.

### The search on the large vault (2026-10-30)

On the large vault of level B a search's 95th percentile was 4.3 ms,
against an objective of 3. Level B's 150 searches a run go over the 45
queries of level R in turn, and three of them make the p95: `cdn`, found
in 1,019 notes, `comp tickets` and `redis streams` (3.3 to 3.9 ms each in
one process, `BenchmarkSearch`), against a median under 1.3 ms. A CPU
profile of the 45 said where a search spends its time: the inflections of
its words 30%, the prefix expansion inside FTS5 23%, bm25 14%, SQLite
preparing statements 12%, the snippets 9%, the structural factors 7%.

- **The stemmer wrote.** Each word, and each word of the vocabulary under
  its prefix, went through an in-memory FTS5 table: a transaction of one
  insert a word, a commit, a read and a delete, every statement prepared
  again. Now one prepared insert (`json_each`), the read in the same
  transaction, and a rollback.
- **A search's own reads were prepared at every call**: the vocabulary,
  and the backlinks and tags of the candidates, whose text changed with
  their number. They now come from the index's statement cache, the
  candidates as a JSON list.
- **The same stems again and again.** A word's stem never changes, and the
  vocabulary under a prefix is the same from one search to the next: the
  stemmer remembers up to 4,096.
- **The full-text index in pieces.** A bulk load leaves FTS5 with many
  segments, and a search looks each term up in every one. A scan that
  rewrote more than half the notes (a first start, a new `ContentVersion`)
  now merges them, FTS5's `optimize`: 49 ms on this vault. On the
  level B queries, 135 searches out of 135 return the same hits merged and
  not.
- **Fifty candidates for eleven hits.** The pool was three times the
  limit but at least 50, so memory_search's eleven made 50 snippets. It is
  now at least 33 for a search without variants: the structure only
  breaks near-ties, and no candidate past three times the hits reached
  them in level B's queries or in `TestSearchWith_Hits`. A search with
  variants ranks each phrasing as deep as before, since the depth changes
  the fused order. (Undone the same day: on a real vault the structure
  does more than break ties, see "The pool back at 50" below.)

`BenchmarkSearch/mixed`, the 45 queries in turn on the large vault, in one
process, with 2 CPUs, ten runs each:

| | p50 | p95 |
|---|---|---|
| main | 1.012 ms | 3.571 ms |
| the stemmer, the prepared reads, the stems remembered | 0.724 ms | 3.026 ms |
| and the index merged | 0.428 ms | 2.524 ms |
| and a pool of 33 | 0.440 ms | 2.233 ms |

Level B, three measures before the pool changed
(`bench/results/base-2026-10-30-search-{a,b,c}.json`; the load of the
machine rose to 9 and 13 during the first two) and one after
(`base-2026-10-30-search-pool.json`):

| | before | after, pool of 50 | and pool of 33 |
|---|---|---|---|
| Search, 87 notes: p50 · p95 | 1.11 · 1.67 ms | 0.69–0.76 · 1.13–1.21 ms | 0.71 · 1.07 ms |
| Search, 5,087 notes: p50 · p95 | 1.571 · 4.338 ms | 0.92–0.98 · 3.37–3.88 ms | 0.95 · 3.26 ms |

The results of level R are the same, every number. The medians move
their references (0.738 and 0.938 ms); the large vault's p95, 3.26 ms,
stays short of its objective.

What was tried and left out:

- **A snippet only for the hits kept.** The snippets are of the 50
  candidates of the pool, not of every note found: SQLite makes them for
  the rows it returns (`LIMIT 5` costs nothing, `LIMIT 2000` 12 ms on
  `cdn`). Making them for the eleven hits alone needs a second query, and
  a second query sets the match up again: 0.2–0.9 ms, more than it saves
  on a query with few notes (`INC-2`, whose `2*` expands to every number).
  With `rowid IN (…)` FTS5 runs the match again for each hit: the 45
  queries 58% slower.
- **The same pool for variants.** At 33 for every phrasing too, the p95
  of level B was 3.13 ms, but one search with variants of the 45 found
  other hits: the fusion keeps its depth.

The rest is FTS5's own work: bm25 over every note found, and the
expansion of each word as a prefix. On a vault of 29,000 notes, most of
them long converted documents, the median search is 39 ms and the p95
250 ms in one process, and merging the index does not move them: a round
of its own.

### The search among long documents (2026-10-30)

That round, on a copy of the lab's index: 29,265 notes, 592 MB, from
200 MB of markdown, half of the notes under 1.3 KB and one in a hundred
over 72 KB, the longest 3.2 MB. Three sets of queries, searched in one
process with 2 CPUs, five rounds each: 80 drawn with a fixed seed from the
words of its notes, one to three in a row; the 45 of level R; and the 80
again, each word of a query also searched as a variant.

Nearly all of a slow search went into snippets. FTS5 cuts a snippet from
the whole body of a note, reading and tokenizing it again, about 10 ms a
megabyte whatever the hits in it, and a search cut one for each of the 33
candidates of its pool, kept or not. A query of three words found in 23
notes, converted manuals of 0.2 to 2.7 MB, took 2.3 ms without its
snippets and 117 ms with them.

A note of 8 KB or more now gets its snippet only if it is among the hits:
the query of the pool cuts the snippets of the shorter notes, as before,
and a second statement those of the long hits, one for each phrasing of
the search. Every note of level B's vaults is under 4.3 KB, so they search
as they did, the condition on each candidate aside (about 4 µs, 1% of
the median). The second statement runs the match again (`+rowid IN (…)`,
so that FTS5 does not run it once per hit), and a search whose long
candidates are all hits saves nothing: it pays the match twice. That is
little unless a word of the query is a prefix of thousands of terms
(`INC-2`, whose `2*` expands to every number). Of the 45 queries of level
R on this index, 25 are faster, 10 within 3%, and 10 slower, each of them
finding 12 notes or fewer: 3 to 37% more, `INC-2` from 153 to 188 ms.

| | median | p95 | slowest |
|---|---|---|---|
| 80 queries of the vault, main | 19.3 ms | 48.5 ms | 119 ms |
| the same, snippets of long hits only | 6.4 ms | 19.2 ms | 41 ms |
| 45 queries of level R, main | 35.8 ms | 241 ms | 379 ms |
| the same, snippets of long hits only | 8.8 ms | 128 ms | 189 ms |
| 80 queries with variants, main | 112 ms | 366 ms | 612 ms |
| the same, snippets of long hits only | 29 ms | 86 ms | 105 ms |

`BenchmarkSearch_LongNotes`, the searches of `TestSearchWith_LongHits` on
its corpus of long notes, and `BenchmarkSearch/mixed` on the large vault
of level B, before and after in turn, ten runs each:

| | before | after |
|---|---|---|
| long notes, 3 hits of 36 found | 15.3 ms | 3.3 ms |
| long notes, with two variants | 23.4 ms | 3.9 ms |
| long notes, every long candidate a hit | 1.66 ms | 1.76 ms |
| long notes, the ten searches (geometric mean) | 9.9 ms | 5.4 ms |
| level B's large vault: p50 · p95 | 0.431 · 2.16 ms | 0.435 · 2.16 ms |

The threshold, on the 80 queries: from 4, 8, 16 and 32 KB the median is
6.0, 6.3, 7.3 and 9.6 ms, the p95 about 19 ms for all four. 8 KB is the
lowest above every note of level B, where a snippet costs less than the
statement that would cut it later.

What was tried and left out:

- **No deferral when a word is short.** Keeping the snippets in the pool
  for a query with a word under four characters, the prefixes that cost
  the most to match again, gave the expensive queries back their time but
  took the savings from the others: the p95 of the 45 queries of level R
  went from 128 to 133 ms, with variants from 86 to 101.
- **A range of rowids for the second statement.** Matching again within
  the hits' smallest and largest rowid costs the same: FTS5 expands a
  prefix in full before it reads any row.

What is left is FTS5's own work again: a snippet of a long hit still reads
its whole body (eleven hits of 0.2 to 2.7 MB are 38 ms), and bm25 goes
over every note found (25 ms for a word in 20,000 notes).

### The search on the large vault, again (2026-10-30)

After that round level B's large vault still had its p95 at 3.1–3.3 ms.
Taken call by call, the 150 searches of a run make it the same way every
time: the 95th percentile is the 8th slowest call, and seven of the eight
are `cdn` (four calls, about 4.4 ms, 1,019 notes found) and `comp tickets`
(three, about 3.5 ms, 483 notes). The eighth is the slowest call of `redis
streams` (211 notes) or `INC-2` (8 notes, whose `2*` expands to every
number), 2.9 to 3.4 ms by the run: from one run to the next the p95 goes
from 3.0 to 4.0 ms. Through MCP a call costs about 0.45 ms besides the
search, a third of it the server's CPU (mcp-go, JSON, HTTP) and 0.06 ms the
proxy of the published port.

- **The pool query was parsed at every search**, the one read of a search
  not prepared: about 6% of its SQL time. Its text changes only with the
  number of projects in or out of the scope, so it now comes from the
  statement cache.
- **Every connection read the index into its own page cache**, and a page
  it had not read yet cost a read call and a copy: the first search of each
  query after a start was a third slower than the next. The connections now
  map the first 256 MiB of the file (`mmap_size`); writes still go through
  write calls, and the mapped pages stay clean.

`BenchmarkSearch/mixed`, before and after in turn, ten runs each:

| | sec/op | p50 | p95 |
|---|---|---|---|
| v2.120.6 | 29.06 ms | 438.5 µs | 2.189 ms |
| the pool query prepared | 27.63 ms | 408.5 µs | 2.139 ms |
| and the index mapped | 26.63 ms | 405.0 µs | 2.000 ms |

Through MCP, with level B's calls, ten runs each in turn: the median from
0.979 to 0.950 ms, the p95 from 3.37 to 3.33, inside the noise; the guard
measured 3.13 ms (v2.120.6: 3.27). The cold start and the scan are the
same with the map.

What was tried and left out:

- **FTS5 ordering the pool itself** (`ORDER BY rank`): the same rows, but
  FTS5 runs the match again inside, and the 45 queries went from 19.7 to
  23.7 ms.
- **Snippets for the hits only.** With `ORDER BY … LIMIT 33` SQLite computes
  the columns of every row that joins the best so far, also of the rows
  pushed out later: about a hundred snippets for the eleven hits of `cdn`.
  A second statement for the hits saves 0.8 ms there and costs 0.8 on
  `INC-2`, which matches its prefix again. A budget of eleven snippets a
  search, kept by a Go function the query calls, made the p95 4% shorter
  and the median 3% longer: the function costs on every row.
- **Fewer collections** (`GOGC=400`): the same p95, the memory peak from 59
  to 92 MB.

The rest is FTS5's own work: bm25 over every note found, the IDF of each
phrase computed by matching it again, and the expansion of short prefixes.

### The pool back at 50 (2026-10-30)

The pool of 33 of the search on the large vault above was wrong to keep.
It rested on the structural factors only breaking near-ties, and on level
B's vaults they do: no hit came from past three times the hits. A check of
a pool of 22 on the lab's index (29,265 notes; 80 queries drawn from its
notes, eleven hits as memory_search asks for ten) showed that they do
more there. A title match (up to ×1.5) and the backlinks of a hub (×1.45
to ×1.6 for 100 to 300 of them) lift notes from past the 50th place in
bm25 into the first hits:

| | hits lost, of 836 | of them with a title match |
|---|---|---|
| a pool of 33 against 50 | 38 | 23 |
| a pool of 50 against 100 | 29 | 11 |
| a pool of 100 against 200 | 45 | 34 |

Some of the hits lost were the first. Level B's vaults showed none of it:
0 differences in every comparison. A pool of 22 changed 130 of 417
searches on the lab, and was left out.

Searches of up to 15 hits rank 50 candidates again, as before v2.120.5;
larger ones and searches with variants never changed. In one process
`BenchmarkSearch/mixed` takes 5% more and its p95 is 16 to 21% longer
(2.11-2.13 to 2.46-2.57 ms). The hits depend on the pool whatever its
size, and on the lab a deeper pool also lifts large hub notes on their
backlinks alone: which order is better needs a set of questions with
their answers on a real corpus to judge it by.

That set came the same day: 1,257 questions with the note that answers
them, on the lab's index without the notes that quote them. 1,002 come
from the question banks of three converted corpora, and 255 were written
from notes of the agents' memory there. Each was turned into a
memory_search call by an agent that saw only the question. Against the
ranking of v2.120.8, the MRR of the first ten hits moves by +0.001 with a
pool of 500 and by -0.002 with one of 33; on queries of one or two words
a pool of 33 loses 0.048. Fifty stays. The title factor and the backlinks
do matter, and pull opposite ways on converted documents. Their image
notes have descriptive titles and each links its article: without the
title factor the article itself comes first more often, and without the
backlinks it falls behind its images. Counting an image note of the
expected article as found, no variant moves those corpora by more than
0.004. In the agents' memory the title factor is worth 0.03 to 0.04, and
a cap on the backlinks would gain 0.017 there and lose 0.045 on the
documents. The ranking stays as it is.

### Short words in a query (2026-10-30)

The queries of that set, as agents write them, are long: 5.1 words on
average, and 3.5 phrasings with `any_of`. On a vault of 30,000 notes (a
copy of the lab's, scanned afresh) a search of them took 76 ms on average
in one process, against the 1 ms of level B's queries of one to three
words. The cost was the short words. Each word of a query is a prefix
query, and FTS5 merges the lists of every indexed word that starts with
it, unless the prefix is indexed too. That vault holds 274,412 words:

| prefix | words that start with it | native SQLite |
|---|---|---|
| `a*` | 19,845 | 140 ms |
| `con*` | 3,615 | 28 ms |
| `di*` | 3,212 | 21 ms |
| `per*` | 1,013 | 11 ms |
| `file*` | 53 | 2 ms |

Agents write them ("stampa di controllo", "invio con allegato"). Level B's
large vault has 1,751 words and does not show it; `BenchmarkSearch_ShortWords`
does, on a corpus of 2,000 notes in a language of many words.

FTS5 can index the prefixes of a few lengths with each word. On the 5,807
phrasings of the set, in native SQLite, the first 50 rows of each the same
whatever the option:

| `prefix=` | full-text lists | mean | p95 | p99 |
|---|---|---|---|---|
| none | 67.6 MB | 13.7 ms | 68.6 ms | 271 ms |
| `'1 2'` | 137.8 MB | 1.8 ms | 4.9 ms | 16.6 ms |
| `'2 3'` | 148.8 MB | 4.5 ms | 7.3 ms | 125 ms |
| `'1 2 3'` | 179.5 MB | 1.1 ms | 3.7 ms | 6.0 ms |
| `'1 2 3 4'` | 218.0 MB | 1.1 ms | 3.2 ms | 5.4 ms |

The index takes `'1 2 3'` (schema v7: the first start re-indexes the
vault). Dropping the `*` of a word that no other indexed word starts
with, the other way to skip the merge, found 62 such words of 246 and
saved nothing.

In one process, on the 30,000 notes, from the same copy:

| | before | after |
|---|---|---|
| a search: mean · p50 · p95 · p99 | 76 · 11.7 · 269 · 726 ms | 30 · 8.3 · 65 · 221 ms |
| with `any_of`: p50 · p95 · p99 | 57 · 598 · 1,389 ms | 29 · 123 · 420 ms |
| searches over 1 s, of 2,514 | 31 | 11 |
| index file | 434 MB | 558 MB |
| cold boot scan | 34.2 s | 46.9 s, 38.7 s with the bulk load below |

The paths and snippets of the 2,514 searches are the same. Interleaved,
ten runs: `BenchmarkSearch_ShortWords` -43% to -75% on its searches with
a short word, the one without unchanged; `BenchmarkSearch_LongNotes` -3%
to -5%; level B's mixed search on its large vault -11% (p50 -10%, p95
unchanged); `BenchmarkScanInto` unchanged.

Writing the prefixes costs at the cold scan, a first start or a re-index:
on level B's large vault 1.801 s became 2.704 (interleaved, n=10). Part
of it was FTS5 merging its segments a little at every write, the same
rows again and again over a load of the whole vault, then once more in
`Compact`. A scan that writes most of the vault now turns that merging
off (`automerge` 0, `crisismerge` 64) and gives the defaults back at the
end: 2.332 s with the prefixes, and 1.730 -> 1.611 s (-6.9%) without
them. The guard's cold start of the large vault, at the end of the
round, is 2,604 ms (runs of 2,580 to 2,654), the reference 2,036 and the
objective 3,500: the reference moves to it, the price of the prefixes.
Every other number of the guard is within its room.

What was left was the snippet. With the prefixes, the snippets of the
long hits took ten times the time of the searches themselves (24.2 s
against 2.4 s for the 1,257 queries, in native SQLite), and a few
searches took seconds; one with variants took 167 s. FTS5's `snippet()`
tries every match of a note as the start of its window, and scores each
one by reading every match again. On a report of 2.4 MB where `a*` and
`de*` are everywhere, one snippet takes 35 s in native SQLite; one of
1.2 MB 8.5 s, one of 300 KB 0.4 s.

From 256 KB on, the snippet is now cut in Go (`cutSnippet`), in one pass
over the body: the window of 24 tokens with the most words of the query,
then the most matches, centered as FTS5 centers its own. Unlike FTS5's,
it does not look for the start of a sentence, and it counts the words of
the query where FTS5 counts its phrases, each inflection of a word one
of them. Interleaved, ten runs: `BenchmarkSearch_HugeNotes`, on notes of
one to two megabytes, 9.1 and 11.6 s -> 43 and 40 ms;
`BenchmarkSearch_LongNotes`, whose longest notes are just over the
threshold, -7% to -13% on four searches of ten, the others unchanged. On
the 30,000 notes:

| | prefixes | and the snippet in Go |
|---|---|---|
| a search: mean · p50 · p95 · p99 | 30 · 8.0 · 66 · 221 ms | 18 · 8.0 · 65 · 193 ms |
| with `any_of`: mean · p50 · p95 · p99 | 212 · 29 · 129 · 427 ms | 43 · 29 · 121 · 264 ms |
| the slowest search | 166 s | 0.89 s |
| searches over 1 s, of 2,514 | 11 | 0 |

The paths are the same. 602 hits, in 48 notes of 256 KB or more, have
another snippet, each with matches marked. Of the 143 in searches
without variants, by a rough count 21 mark more words of the query than
FTS5's and the others as many.

## Limits

- A small vault (87 notes) written by the maintainers, who also wrote the
  questions; the blind queries reduce but do not remove that bias.
- One query set; a different agent would type different queries.
- Level R measures the rank of the first gold note, not whether the agent
  ends up answering correctly — that is level A.
- Level B measures one machine, a small server and a synthetic vault; its
  timings are milliseconds on loopback, where a few hundred microseconds
  are noise. Compare runs of the same day and machine.
- Level A uses one model and a regex check per answer; negative questions
  are checked by hand. The backlog benchmark is synthetic and small enough
  for one read (43 KB as sections), its bodies are one or two sentences,
  and its questions are written from the same data as its answers. The by-configuration table comes from one run per
  configuration; the full run from three.
