# Benchmark

How well does an agent find what it needs in a gosidian vault? Other memory
servers publish recall figures measured on conversational memory
(LongMemEval); gosidian is a wiki of notes, so we measure on a vault of
notes instead. Everything needed to rerun it is in [`bench/`](../bench/README.md).

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
(read-only files, `_index.md`, `MIRROR.md`) and takes the orientation
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

## Limits

- A small vault (87 notes) written by the maintainers, who also wrote the
  questions; the blind queries reduce but do not remove that bias.
- One query set; a different agent would type different queries.
- Level R measures the rank of the first gold note, not whether the agent
  ends up answering correctly — that is level A.
- Level A uses one model and a regex check per answer; negative questions
  are checked by hand. The by-configuration table comes from one run per
  configuration; the full run from three.
