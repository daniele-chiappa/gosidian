# The second run: the same team, on gosidian v2.116.0 (2026-10-25)

The team of [`squadre/claude-sonnet.yaml`](../squadre/claude-sonnet.yaml)
again, Claude Code with sonnet in every role, on gosidian v2.116.0: the
fixes of phase 1d (checks only on a pushed branch, a role that writes
must write, what the pipeline found in the reports, a run that ended
holds no task). The same objective, people, questions and checks; a new
repository from the same skeleton, and the first run's notes and tasks
moved out of the project. The numbers are in
[`claude-sonnet-giro2.json`](claude-sonnet-giro2.json).

## The four measures

**Knowledge.** The 20 questions from a copy of the team's 46 notes, two
local judges, -10 to 10 (the ceiling, from the sources, is the first
run's: 7.6 and 7.8):

| Notes | Judge | Mean | Right | Zero | Wrong | Contradicts |
|---|---|---|---|---|---|---|
| the team's (`conoscenza/`) | gemma4 | 0.75 | 9 | 1 | 10 | 10 |
| the team's (`conoscenza/`) | qwen3 | 4.15 | 15 | 1 | 4 | 14 |
| the first run's team | gemma4 | 2.9 | 11 | 2 | 7 | 7 |
| the first run's team | qwen3 | 5.1 | 16 | 1 | 3 | 12 |

More notes, fewer answers. The topic notes say themselves that some WHO
numbers "are not readable in the extract" or that the adults' sections
"were not extracted": the answers on the minutes for pregnant women
(q03) and on the sitting time of adults (q06) find nothing. The reviewer
asked for the WHO numbers to be checked, and its report never reached
the coordinator (below). Three questions the first run
missed are answered now (q01, q19, q20: a note on adherence exists).
Between two runs of the same team the questions move by up to 17 points:
one run is an example, not a measure of the team.

**Plans.** The five plans against their 40 checks (yes 1, partly 0.5):

| Person | gemma4 | qwen3 | First run |
|---|---|---|---|
| giulia | 6/8 | 6/8 | 8/8, 8/8 |
| franco | 8/8 | 8/8 | 8/8, 8/8 |
| sara | 7.5/8 | 6.5/8 | 7.5/8, 8/8 |
| marco | 8/8 | 7.5/8 | 8/8, 8/8 |
| luca | 7.5/8 | 6.5/8 | 8/8, 7/8 |

Giulia's plan misses the same two checks with both judges: less sitting
at work, and a doctor for new symptoms. The first is the knowledge's gap
(the adults' sitting time was not extracted) reaching a plan.

**App.** `main` built and tested, published, used on a computer and on a
phone (390 px): 24 of 24 steps, no error in the console, no failed
request. Approved at the first request.

**Cost.** 21 tasks (16 completed, 5 canceled at the close), 24 attempts,
10.35 USD at list prices and 16.0 M tokens counted, plus two agent steps
that stopped at the step's 3 M tokens (6.2 M tokens) that gosidian did
not count (below). With the subscription: 27% of a five-hour session, 2%
of the week. 127 minutes from the objective to the close; the app merged
at 113, after waiting 55 minutes for the approval (32 of them the
operator's: the request was not relayed).

| Role | Tasks | Attempts | USD | Tokens |
|---|---|---|---|---|
| coordinatore | 1 | 1 | 0.47 | 0.3 M |
| conoscenza | 8 | 9 | 4.44 | 5.3 M |
| sviluppo | 2 | 3 | 2.26 | 2.7 M |
| revisore | 10 | 11 | 3.19 | 7.7 M |

## What happened

1. The coordinator split the objective into 12 tasks in 80 seconds:
   three of topics, two of the catalogue, three of plans, the app, three
   reviews, with the dependencies between them.
2. The knowledge and four plans were written at the first attempt in 30
   minutes. Sara's plan came back as text again ("I cannot write files
   in this task"): this time the gate saw no note and gave it back, and
   the second attempt wrote it. The claude plugin's prompt said "You
   cannot write files in this task: answer with text" to an agent whose
   MCP writes.
3. The reviewer found real errors: an exercise for arthritis that
   contradicts its source, a caution on overhead pressing misattributed,
   the 180/110 blood pressure threshold given as a general rule in about
   25 notes. Its first attempt stopped at the step's 3 M tokens.
4. The app's first attempt stopped at 3 M tokens too; the second wrote
   15 files, the pipeline's checks passed, the preview opened, and a
   person approved from the phone.
5. The coordinator, woken when the sub-tasks closed, read the reviews as
   empty and asked for them again "with a written report"; the reviews of
   the app ended "I cannot open the repository" (the reviewer has none).
   At the third round the run was closed: the server cut every report at
   the agent's first `#` or `##` heading.

## What gosidian learns (phase 1e)

- **A task's report is cut at its first heading**: `Complete` writes the
  agent's text under `## Report` as it is, and the reader stops at the
  next heading of level 1 or 2. The coordinator never saw the reviews.
- **The coordinator does not know which roles see a repository**: it gave
  the review of the code to a role with none, twice.
- **An agent step that fails costs nothing in gosidian**: the tokens of a
  step stopped at its limit are in the plugin's error, not in the task's
  cost nor in the team's budget.
- **A role cannot set `max_tokens`**, only `max_turns` and `max_seconds`:
  the app's role could not get more than the preset's default.
- **The executors' prompt**: with gosidian's MCP writing, "you cannot
  write files" must say where notes go (claude and pi plugins).
- **The pilot's team**: its reviewer needs the repository, read only, to
  review the app.
