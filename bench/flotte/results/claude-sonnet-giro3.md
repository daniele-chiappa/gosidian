# The third run: the same team, on gosidian v2.117.0 (2026-10-25)

The team of [`squadre/claude-sonnet.yaml`](../squadre/claude-sonnet.yaml)
a third time, on gosidian v2.117.0 with claude 0.3.2: the fixes of phase
1e (a task's report keeps its headings, a failed step's tokens count, the
coordinator knows which roles see a repository, the executors say where
the vault's notes go). The numbers are in
[`claude-sonnet-giro3.json`](claude-sonnet-giro3.json).

## The three runs

| | First | Second | Third |
|---|---|---|---|
| gosidian | v2.115.0 | v2.116.0 | v2.117.0 |
| Knowledge (gemma4 / qwen3) | 2.9 / 5.1 | 0.75 / 4.15 | 2.0 / 3.0 |
| Plans (of 40, gemma4 / qwen3) | 39.5 / 39 | 37 / 34.5 | 33 / 35 |
| App (steps, computer and phone) | 24/24 | 24/24 | 24/24 |
| Cost counted | 10.67 USD, 16.5 M tokens | 10.35 USD, 16.0 M tokens | 8.98 USD, 29.3 M tokens |
| Subscription (session, week) | 21%, 1% | 27%, 2% | 35%, 3% |
| Time to the close | 95 min | 127 min | 105 min |
| Approvals | 2 | 1 | 2 |
| How it ended | closed by a person, the coordinator at its rounds | closed by a person: the reviews reached the coordinator empty | closed by a person: the coordinator's rounds burnt by a failed sub-task |

The third run's tokens count the steps that failed at their limit (15.6
M of the 29.3 M); the first two did not. Their cost is not counted: on
Anthropic's endpoint a step stopped before Claude Code's result has no
price, which the subscription's 35% of a session shows instead.

## The four measures

**Knowledge.** The 20 questions from a copy of the team's notes, two
local judges, -10 to 10 (the sources' ceiling: 7.6 and 7.8):

| Notes | Judge | Mean | Right | Zero | Wrong | Contradicts |
|---|---|---|---|---|---|---|
| the team's (`conoscenza/`) | gemma4 | 2.0 | 10 | 2 | 8 | 8 |
| the team's (`conoscenza/`) | qwen3 | 3.0 | 13 | 1 | 6 | 16 |

Across three runs of the same team the knowledge scores 0.75 to 5.1:
how far the agents read the 380 KB WHO guidelines, not the team, makes
the difference. One run is an example, not a measure.

**Plans.**

| Person | gemma4 | qwen3 |
|---|---|---|
| giulia | 7/8 | 6.5/8 |
| franco | 6.5/8 | 8/8 |
| sara | 6.5/8 | 8/8 |
| marco | 6.5/8 | 6.5/8 |
| luca | 6.5/8 | 6/8 |

**App.** `main` built and tested (8 tests of the team, green), published,
used on a computer and on a phone: 24 of 24 steps, no error in the
console, no failed request. The people's plans came with the app's
skeleton and the catalogue's pull request: the task that was to write
them from the corrected plans failed.

**Cost.** 18 tasks (13 completed, 2 failed, 3 canceled at the close), 20
attempts, 8.98 USD and 29.3 M tokens counted; 105 minutes from the
objective to the last task that moved; 2 approvals from a person (33 minutes and 1
minute after the request).

| Role | Tasks | Attempts | USD | Tokens |
|---|---|---|---|---|
| coordinatore | 1 | 1 | 0.41 | 0.2 M |
| conoscenza | 10 | 10 | 5.27 | 9.0 M |
| sviluppo | 4 | 8 | 2.72 | 18.7 M |
| revisore | 3 | 1 | 0.58 | 1.3 M |

## What happened

1. The coordinator split the objective into 10 tasks, the app's skeleton
   in parallel with the knowledge; a person approved the skeleton.
2. The knowledge and the five plans were written at the first attempt in
   25 minutes, all in the vault: no agent gave its notes back as text.
3. The review reached the coordinator whole: it gave three corrections
   (the catalogue, the length of Franco's and Luca's sessions, Giulia's
   and Sara's equipment) and a check of the five plans, all done at the
   first attempt.
4. The app's data did not fit in a step's 3 M tokens: the task that read
   every note and wrote all of it failed three times; the coordinator
   split it in two; the catalogue's half passed and was approved, the
   people's half failed (its own test caught an inconsistency in Sara's
   fifth week, then twice the limit).
5. The failed task called the coordinator again each time another
   sub-task started or closed: four rounds in ten minutes, with nothing
   new. The objective went to a person, who had the run closed and
   measured.

## What gosidian learns (phase 1f)

- **A failed sub-task called its coordinator again and again**: the
  digest of `children_done` was the state of all the sub-tasks. It is
  that of the ones that call the coordinator (failed, waiting for a
  person), and of all once all are closed.
- **A coordinator with nothing to give had no answer**: an empty plan
  failed its run, and the round counted. It answers `wait`.
- **A stopped step's cost on Anthropic's endpoint** is unknown: its
  tokens count, its dollars do not. To weigh: the list prices in the
  plugin, or a team's budget of tokens.
- **The team**: the developer's tasks that read the whole vault need more
  than the preset's 3 M tokens; a team can now give its role
  `max_tokens`, which this file, kept as it was for the comparison, does
  not.
