# Open-weight models in the team, on gosidian v2.159.0 (2026-11-12 and 13)

The pilot with the roles on [pi](../../../docs/plugins.md) and
open-weight models (IMP-195 phase 3): from a provider (the lab's preset
`pi-lab`, on OpenRouter) and on the lab's GPU (the preset `pi-gpu`,
Ollama, 16 GB, a context of 32K). pi and claude 0.4.1. Four things
ran: a short trial of seven models, the whole pilot with a mixed team,
and a reduced objective twice, with a local team and with a team on the
provider. The numbers are in [`modelli-aperti.json`](modelli-aperti.json).

These are examples of how the teams went, one run each, not measures of
the models. The mixed run did not end by itself, so the pilot's four
measures (the questions, the plans' checks, the app's steps) were not
taken on it: a reviewer on Claude Code with opus read the result instead.

## In short

| | Mixed, the whole pilot | Local, reduced | Provider, reduced |
|---|---|---|---|
| Team | [`mista.yaml`](../squadre/mista.yaml) | [`locale.yaml`](../squadre/locale.yaml) | [`ridotta-openrouter.yaml`](../squadre/ridotta-openrouter.yaml) |
| Coordinator | Claude Code, sonnet | gemma4:12b | deepseek-v4.1-flash |
| Knowledge | qwen3.8-max | gemma4:12b | qwen3.8-max |
| Developer | deepseek-v4.1-flash | gemma4:12b | deepseek-v4.1-flash |
| Reviewer | qwen3.8-max, then Claude Code with opus | Claude Code, opus | Claude Code, opus |
| Objective | [`obiettivo.md`](../obiettivo.md): five people | [`ridotto/obiettivo.md`](../ridotto/obiettivo.md): one person | the same |
| Tasks | 14 completed, 7 failed, 3 canceled by hand | 1 completed, the others failed or canceled | 13 completed, 12 at the first attempt |
| Cost counted | 8.88 USD, plus 1.62 of the opus review | 0 (local), 0.49 of opus | 3.16 USD: 1.62 on the provider, 1.54 of opus |
| Time | 40 min and then 6 h 50 min, with the stops | 42 min | 3 h 35 min, with the waits for a person |
| Approvals | 2 | 0 | 4 |
| How it ended | stopped at the coordinator's rounds, the review without a report; kept going by hand | stopped: no app, the plan in the wrong form | **closed by the coordinator**, at its fourth round of five |
| The reviewer on the result | 8 open problems, none dangerous; 21 quotes of 24 exact | to correct: five basic errors, not dangerous | passed at the fourth review, nothing open |

The costs of opus are nominal (list prices, on a subscription). The
five runs of October with sonnet in every role cost 7.73 to 12.99 USD
for the whole pilot ([the fifth](claude-sonnet-giro5.md)).

## The short trial

One small task for each model, in a project of its own: a note from one
source of the corpus (22 KB) with points, word-for-word quotes and
limits; then, for three of them, a change to a small repository with its
tests (a parameter of a function, two files, 780 seconds). The quotes
were searched word for word in the source.

**The note, from the provider:**

| Model | Attempts | USD | Tokens | Exact quotes |
|---|---|---|---|---|
| mimo-v2.6-pro | 1 | 0.012 | 137 k | 12 of 16 |
| deepseek-v4.1-flash | 1 | 0.020 | 176 k | 12 of 14 |
| qwen3.8-max | 2 (the first beyond 600 s) | 0.340 | 496 k | 32 of 32 |
| glm-5.3 | 3 (one without a note, one lost in the queue) | 0.293 | 199 k | 15 of 17 |
| glm-5.3-flash | 3 (two beyond 600 s) | 0.057 | 449 k | 15 of 24 |

**The note, on the GPU:**

| Model | Attempts | Time | Tokens | Exact quotes | The note |
|---|---|---|---|---|---|
| qwen3:14b | 1 | 3.5 min | 84 k | 1 of 8 | 2.5 KB, the quotes reworded or invented |
| gemma4:12b | 1 | 1.3 min | 99 k | 7 of 8 | 1.7 KB, one heading: short |

Both local models used the MCP tools and wrote the note in the right
folder at the first attempt; their notes are a third of the larger
models'.

**The change to the repository:** deepseek-v4.1-flash (0.007 USD),
mimo-v2.6-pro (0.015) and qwen3.8-max (0.069) each closed it at the
first attempt in about two minutes, touching only the two files, with
their tests and four acceptance tests green.

The trial cost about 0.81 USD. From it: qwen3.8-max where the quotes
count, deepseek-v4.1-flash for the app, gemma4:12b for the local team.

## The mixed team on the whole pilot

1. **The first evening (40 minutes, about 1 USD).** The coordinator
   split the objective into 13 tasks in one run (0.12 USD). The three
   tasks of notes failed five times without a note: twice on the
   preset's 500,000 tokens, three times on 780 seconds. qwen3.8-max was
   still reading its sources after 27 to 32 turns. The run was stopped.
2. **Again with 30 minutes a role.** The notes closed in 10 to 16
   minutes (0.28 to 0.39 USD each), the first half of the catalogue in
   29 minutes (0.68 USD), the second at its third attempt (1.34 USD):
   it had written all its notes when its time ended, and the next
   attempt, which found them done, was sent back for writing none.
3. **The provider key's weekly limit** was reached: the five plans
   failed three attempts each within seconds on its 403. With the limit
   raised, the coordinator's tasks in their place closed in 6 to 7
   minutes each (0.15 to 0.19 USD): smaller tasks than the notes'.
4. **The app's data** failed three times: the developer's note said it
   could run commands and the lab's pi preset gave it no shell, so it
   wrote by hand the JSON its script should have made. With `bash`
   allowed and `access: exec` it then stopped on `max_tokens` (3 and 6
   million: the count includes what is read again from the cache) and on
   60 turns, and closed with 20 million tokens and 80 turns, 0.24 USD
   for the good attempt. The site took 4 minutes and 0.06 USD.
5. **Two tasks on the same files** (the site, and a "check of the site"
   the coordinator gave without a dependency between them) made two pull
   requests in conflict: the second merge answered 405.
6. **The review** by qwen3.8-max ended twice with an empty text (0.41
   and 1.13 USD) and the task closed all the same. The objective reached
   its five rounds.
7. **A reviewer on opus**, given by hand: one task, 4 minutes, 1.62 USD
   nominal. 24 quotes checked, 21 exact; 8 open problems, none that makes
   a plan dangerous. The two most serious are wrong attributions (the
   plan after childbirth applies warnings the source writes for
   pregnancy; two plans widen a recommendation the source limits to
   advanced techniques). It could not read the app's data: they are in
   the repository, not in the vault.

Without a person the run would have stopped several times: attempts set
back to zero (four tasks), dependencies pointed again to the tasks that
replaced the failed ones (twice), three overtaken tasks canceled, the
objective's rounds lowered three times, a note on a task with the
preview's error.

## The reduced objective

One new person, one plan of eight weeks from the knowledge already in
the vault (four notes to read, at most 120 lines, a given form), the
person in the app's data, a review. Three small tasks, ten minutes an
agent, the reviewer on opus in both teams.

**The local team** (gemma4:12b, 32K):

- the coordinator's first plan took 26 seconds: three tasks with their
  dependencies, the copy in the app's task the wrong way round;
- the plan was written in 78 seconds, 69 lines, every exercise from the
  catalogue, but in two phases and not eight weeks;
- the developer failed three attempts without changing a file
  (`stop: length`: the repository's files do not fit in 32K), and so did
  the plan's correction to eight weeks written in full;
- the coordinator's later rounds: two plans refused for dependencies
  with a wrong path, a good plan of corrections with invented budgets of
  10 to 100 USD (the preset refuses above 2), a duplicate of three tasks;
- the review: to correct, five basic errors (no weeks, a hold counted in
  repetitions, a rule of progression reversed, one against the
  catalogue, a wrong attribution).

A 12B model in 32K plans and writes a short note in a minute. It holds
neither the repository nor a long note, and gets the plan's fields wrong.

**The team on the provider:**

- the plan closed at its third attempt in 12 minutes (two lost on 600
  seconds, raised to 900), the app in 2 minutes at the first;
- then the team went on by itself, four times: review, correction of
  the plan, the app's data realigned, each at the first attempt;

  | Review | Verdict | Open problems |
  |---|---|---|
  | First | to correct | 10 |
  | Second | to correct | 2, light |
  | Third | to correct | 1, light |
  | Fourth | passed | 0, three remarks that ask no change |

- the coordinator closed the objective at its fourth round;
- by role: knowledge 1.09 USD in four tasks, the developer 0.39 in
  four, the coordinator 0.14, the four reviews 1.54 nominal;
- a person answered four requests to merge, and once raised the day's
  budget: the team had reached it and waited without saying so.

The first attempt at the plan took phrases from the tasks the local
round had left in the project: the reduced runs shared a project, and a
run should start from a clean one.

## What gosidian learns

- **Small tasks close, wide ones do not.** With the same models a task
  of 12 to 14 notes took 29 minutes or failed, a plan from four notes 7.
  The limits of a role (time, turns, tokens) had to grow four times in
  the mixed run; the reduced objective ran inside ten to fifteen minutes.
- **An open team closes an objective** when its tasks are small and a
  strong reviewer judges: 1.62 USD on the provider for a plan, its app
  and three corrections.
- **A reviewer that always finds something** keeps the cycle going:
  with no threshold, each light problem costs a correction, a
  realignment of the app, a review and a person's approval.
- **Open, for phase 3d:**
  - a limit of the provider or of a subscription (a 403, a quota) burns
    the three attempts of every task in seconds; a step in line for its
    plugin spends its timeout and counts as an attempt; so does a
    restart of the plugin: the team should wait instead;
  - the day's budget stops the team in silence, and an objective whose
    queue is stuck behind a failed task stays `working`;
  - the coordinator does not point the dependencies to the task that
    replaces a failed one, gives two tasks on the same files, repeats
    tasks, and sets budgets the preset refuses;
  - the check after the agent looks only at the last attempt's writes,
    and does not tell the agent the preview's error;
  - a role without checks closes its task with an empty report;
  - `max_tokens` counts the cache's reads, where the cost should stop;
    a task's `cost_usd` adds its failed attempts;
  - the report in the task's note is cut, and why an agent stopped
    (`stop: length`) is not in it;
  - the reviewer does not read the repository;
  - the app is realigned at each change of the plan, with a person's
    approval each time;
  - one agent at a time on a pi plugin, whatever `max_agents` says.
