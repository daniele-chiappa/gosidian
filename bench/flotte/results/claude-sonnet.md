# The first run: Claude Code with sonnet in every role (2026-10-25)

The team of [`squadre/claude-sonnet.yaml`](../squadre/claude-sonnet.yaml)
on gosidian v2.115.0, in the maintainers' lab, on a Claude subscription.
The numbers are in [`claude-sonnet.json`](claude-sonnet.json); this page
says what they mean and what happened.

## The four measures

**Knowledge.** The 20 questions, answered by the fixed agent (haiku, read
only) from a copy of the team's 41 notes, and from the 10 sources for
the ceiling; two local judges, -10 to 10:

| Notes | Judge | Mean | Right | Zero | Wrong | Contradicts |
|---|---|---|---|---|---|---|
| the team's (`conoscenza/`) | gemma4 | 2.9 | 11 | 2 | 7 | 7 |
| the team's (`conoscenza/`) | qwen3 | 5.1 | 16 | 1 | 3 | 12 |
| the sources (`fonti/`) | gemma4 | 7.6 | 17 | 1 | 2 | 2 |
| the sources (`fonti/`) | qwen3 | 7.8 | 19 | 0 | 1 | 11 |

The team's notes hold less than the sources: they summarise what the
plans need. The questions they miss are gaps, not errors: no medical
clearance for a chronic condition (q05), no SMART goals for adherence
(q20), the older adults' strengthening days only for adults 18–64 (q02);
the agent says so (NON_TROVATO), and a partial answer still scores below
zero. The two judges disagree often (q01: -5 and 9 for a right answer):
read the two columns together, never one alone.

**Plans.** The five plans against their 40 checks (yes 1, partly 0.5):

| Person | gemma4 | qwen3 |
|---|---|---|
| giulia | 8/8 | 8/8 |
| franco | 8/8 | 8/8 |
| sara | 7.5/8 | 8/8 |
| marco | 8/8 | 8/8 |
| luca | 8/8 | 7/8 |

A control plan for Franco, wrong on purpose (jumps, daily running, no
balance, no progression, machines he does not have, no sources), scores
0/8 with both judges: the checks tell a bad plan from a good one. They are
a floor, not a ranking: Franco's plan still asks for balance with eyes
closed in phase 2, which its own exercise note forbids for him, and no
check catches it (the team's reviewer did, twice).

**App.** `main` built and tested (14 tests of the team, green), published,
and used on a computer and on a phone (390 px): 24 of 24 steps (the five
people, each plan, the exercises), no error in the console, no failed
request.

**Cost.** 18 tasks (14 completed, 1 failed, 3 canceled when the run was
closed), 28 attempts, 10.67 USD at the API's list prices (with the
subscription: 21% of a five-hour session, 1% of the week), 16.5 M
tokens, 95 minutes from the objective to the close (the last work at 73
minutes), 2 approvals from a person (the app's merges, from the phone).

| Role | Tasks | Attempts | USD | Tokens |
|---|---|---|---|---|
| coordinatore | 1 | 1 | 0.52 | 0.4 M |
| conoscenza | 9 | 14 | 3.29 | 4.7 M |
| sviluppo | 4 | 10 | 5.19 | 6.9 M |
| revisore | 4 | 3 | 1.66 | 4.5 M |

## What happened

1. The coordinator split the objective into 9 tasks in 40 seconds: two
   of topics, two of the catalogue, three of plans, the app, the review.
2. The first two tasks stopped at the step's 300,000 tokens, the claude
   plugin's default, which counts the cache reads too: an agent reading
   the WHO guidelines (380 KB) passes it in seven turns. The lab raised
   it (3 M a step, 8 M a run); the tasks were given back by hand.
3. The knowledge was written in 9 minutes (topics, 27 exercises, three
   plans). One task (Luca's and Marco's plans) closed without writing its
   notes: its agent wrote «the task says I cannot write» and put the
   plans in its report, with the write tools allowed.
4. The app's first two attempts failed at the preview: the skeleton's
   build copied `data/.gitkeep`, a file of no type (the lab's mistake,
   fixed on `main`). The third passed and was approved from the phone,
   with Luca and Marco shown as «not available yet» and the test that
   wants them marked as a todo.
5. The reviewer found everything: the missing plans, Franco's eyes
   closed, citations to check. The coordinator gave the two plans (written
   this time) and the app's data (approved), but not Franco's fix.
6. The second review took the developer's «I ran nothing» (true: the
   pipeline runs the checks) for checks never run, and the coordinator
   believed it: it asked the developer to run commands it cannot run.
   The agent changed nothing, the pipeline ran the checks on a branch that
   does not exist, and the attempts failed before the gate.
7. After 8 rounds of the coordinator (5 in the team's block, 3 more to
   make up for the lab's troubles) the objective went to a person, who
   closed the run.

## What gosidian learns

- The work pipeline runs the checks when the agent pushed no branch: they
  fail on a branch that is not there and the task loses an attempt,
  instead of the gate's «changed nothing».
- The gate of a role that writes in the vault does not check that it
  wrote: a task without its notes closes completed.
- The coordinator and the reviewer see the agents' reports, not the
  pipeline's facts: what the pipeline ran and how it ended (the checks,
  the merge) must read as the pipeline's, apart from the agent's words.
- Giving a task back with its attempts set to zero by hand made the
  server take up its old runs again (their idempotency keys): a run that
  ended must never hold a task again.
- The plugin's default token budget of a step is too low for agents that
  read long documents.
