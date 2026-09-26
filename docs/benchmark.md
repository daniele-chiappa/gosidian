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
- Since 2026-09-26 search snippets are twice as long (24 tokens): the
  only wrong gosidian answer of the full run below — taken from a snippet
  that cut the fact in half — does not recur.

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
