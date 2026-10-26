# The fourth run: the same team, on gosidian v2.118.0 (2026-10-25 and 26)

The team of [`squadre/claude-sonnet.yaml`](../squadre/claude-sonnet.yaml)
a fourth time, on gosidian v2.118.0 with claude 0.3.2: the fixes of phase
1f (a failed sub-task calls its coordinator once, and all of them once
all are closed; a coordinator with nothing to give answers `wait`). The
team's day budget was raised from 40 to 80 USD in the lab's note only,
since the first three runs and the tests had spent 30.53 USD that day;
this file keeps 40. The numbers are in
[`claude-sonnet-giro4.json`](claude-sonnet-giro4.json).

## The four runs

| | First | Second | Third | Fourth |
|---|---|---|---|---|
| gosidian | v2.115.0 | v2.116.0 | v2.117.0 | v2.118.0 |
| Knowledge (gemma4 / qwen3) | 2.9 / 5.1 | 0.75 / 4.15 | 2.0 / 3.0 | 4.4 / 6.0 |
| Plans (of 40, gemma4 / qwen3) | 39.5 / 39 | 37 / 34.5 | 33 / 35 | 38 / 39 |
| App (steps, computer and phone) | 24/24 | 24/24 | 24/24 | 24/24 |
| Cost counted | 10.67 USD, 16.5 M tokens | 10.35 USD, 16.0 M tokens | 8.98 USD, 29.3 M tokens | 12.99 USD, 21.6 M tokens |
| Subscription (session, week) | 21%, 1% | 27%, 2% | 35%, 3% | 23%, 2% |
| Time to the close | 95 min | 127 min | 105 min | 82 min of work, 587 with a night's wait for an approval |
| Approvals | 2 | 1 | 2 | 5 |
| How it ended | closed by a person, the coordinator at its rounds | closed by a person: the reviews reached the coordinator empty | closed by a person: the coordinator's rounds burnt by a failed sub-task | **closed by the coordinator**, at its fourth round of five |

No step of the fourth run stopped at its tokens, so every attempt has its
price: the 12.99 USD hold the reviewer's two and the developer's three
failed attempts.

## The four measures

**Knowledge.** The 20 questions from a copy of the team's notes, two
local judges, -10 to 10 (the sources' ceiling: 7.6 and 7.8):

| Notes | Judge | Mean | Right | Zero | Wrong | Contradicts |
|---|---|---|---|---|---|---|
| the team's (`conoscenza/`) | gemma4 | 4.4 | 13 | 2 | 5 | 6 |
| the team's (`conoscenza/`) | qwen3 | 6.0 | 17 | 2 | 1 | 12 |

The best of the four runs, and still below the sources. The team wrote 11
notes of topics and 24 exercises, in five tasks; how far the agents read the 380 KB WHO guidelines makes the difference
from run to run. One run is an example, not a measure.

**Plans.**

| Person | gemma4 | qwen3 |
|---|---|---|
| giulia | 8/8 | 8/8 |
| franco | 7.5/8 | 8/8 |
| sara | 7.5/8 | 8/8 |
| marco | 8/8 | 8/8 |
| luca | 7/8 | 7/8 |

**App.** `main` built and tested (the team's tests, green), published,
used on a computer and on a phone: 24 of 24 steps, no error in the
console, no failed request. Five pull requests merged (2, 3, 5, 6, 8),
three closed by the pipeline after their checks failed (1, 4, 7).

**Cost.** 25 tasks (all completed), 30 attempts, 12.99 USD and 21.6 M
tokens counted. 587 minutes from the objective to the last task that
moved, of which 505 waiting for one approval during the night; 82 of
work. 5 approvals from a person: 15, 1, 1 and 7 minutes after the
request, and the last one 8 hours 25 minutes after.

| Role | Tasks | Attempts | USD | Tokens |
|---|---|---|---|---|
| coordinatore | 1 | 1 | 0.64 | 0.2 M |
| conoscenza | 14 | 14 | 4.33 | 5.6 M |
| sviluppo | 5 | 8 | 4.87 | 7.2 M |
| revisore | 5 | 7 | 3.15 | 8.6 M |

## What happened

1. The coordinator split the objective into 14 tasks, finer than before:
   the knowledge in five, one plan per person, the app in two (the data
   with its tests, then the pages), two reviews. The plans started with
   the knowledge, not after it.
2. The knowledge and the five plans were written at the first attempt in
   15 minutes (3.09 USD). The review of the topics and the catalogue
   against the sources stopped twice at the reviewer's 30 turns, priced
   (0.61 and 0.64 USD), and closed at the third.
3. The app's data: the developer's own test failed at the first attempt,
   and the gate sent it back with its output; the second passed and was
   approved. The pages passed at the first attempt.
4. All the sub-tasks closed, and the coordinator was called once: it read
   both reviews whole and gave four tasks (the plans' durations, Marco's
   plan and a note, the data again, a final review). Three more rounds
   followed, each on a smaller defect of Sara's plan (the 30 minutes'
   cap, the rests, the repetitions, the sums of the minutes), each
   corrected at the first or second attempt.
5. The last data step waited a night for its approval (22:11 to 06:36).
   Then the last review found nothing open, and the coordinator closed
   the objective with its report: what is there, what was corrected, the
   limits the notes declare.

## What gosidian learns

- **Phase 1f works**: the coordinator is called once its sub-tasks are
  closed, never for nothing, and it closes the objective by itself. It
  used four rounds of its five: one more defect in Sara's plan and the
  objective would have gone to a person.
- **The developer cannot run its own tests**: a role with a repository
  runs with `access: edit` (`internal/pipeline/taskflow.go`), so the
  agent writes tests it cannot run, and says so in every report. Three
  of its five tasks failed their first attempt on its own test. To weigh:
  a role's `access: exec`, with the checks in the executor's sandbox.
- **A retry starts again from `main`**: the agent did not find its
  previous attempt's branch and wrote everything again, without knowing
  which line had failed. To weigh: a task the gate sends back continues
  on the agent's branch.
- **The coordinator's dependencies**: its first final review did not
  depend on the realignment of the data, so it checked the data of
  before. The prompt could say that a review depends on the tasks whose
  work it checks.
- **The team**: a review against the sources needs more than the
  reviewer's 30 turns; this file, kept as it was for the comparison,
  does not change them.
