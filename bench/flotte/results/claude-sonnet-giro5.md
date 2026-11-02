# The fifth run: the developer runs its own tests, on gosidian v2.119.0 (2026-10-27)

The team of [`squadre/claude-sonnet.yaml`](../squadre/claude-sonnet.yaml)
a fifth time, with one change: the developer has `access: exec`, and its
note says to run `npm test && npm run build` before it finishes. gosidian
v2.119.0 (phase 1g: a role's access, another attempt continues the
agent's branch, a review depends on the work it checks) with claude
0.4.0. The work pipeline of the lab's project took the two settings of
[`docs/upgrading.md`](../../../docs/upgrading.md) for 2.119.0. The day
budget was the file's 40 USD. The numbers are in
[`claude-sonnet-giro5.json`](claude-sonnet-giro5.json).

## The five runs

| | First | Second | Third | Fourth | Fifth |
|---|---|---|---|---|---|
| gosidian | v2.115.0 | v2.116.0 | v2.117.0 | v2.118.0 | v2.119.0, developer in `exec` |
| Knowledge (gemma4 / qwen3) | 2.9 / 5.1 | 0.75 / 4.15 | 2.0 / 3.0 | 4.4 / 6.0 | 1.3 / 4.2 |
| Plans (of 40, gemma4 / qwen3) | 39.5 / 39 | 37 / 34.5 | 33 / 35 | 38 / 39 | 37.5 / 37.5 |
| App (steps, computer and phone) | 24/24 | 24/24 | 24/24 | 24/24 | 24/24 |
| Cost counted | 10.67 USD, 16.5 M tokens | 10.35 USD, 16.0 M tokens | 8.98 USD, 29.3 M tokens | 12.99 USD, 21.6 M tokens | 7.73 USD, 13.7 M tokens |
| Subscription (session, week) | 21%, 1% | 27%, 2% | 35%, 3% | 23%, 2% | 35%, 3% |
| Time to the close | 95 min | 127 min | 105 min | 82 min of work, 587 with a night's wait for an approval | 39 min |
| Approvals | 2 | 1 | 2 | 5 | 5 |
| How it ended | closed by a person, the coordinator at its rounds | closed by a person: the reviews reached the coordinator empty | closed by a person: the coordinator's rounds burnt by a failed sub-task | **closed by the coordinator**, at its fourth round of five | **closed by the coordinator**, at its fourth round of five |

The subscription's share is read before and after the run, and counts
whatever else used the account in the same five hours.

## The four measures

**Knowledge.** The 20 questions from a copy of the team's notes, two
local judges, -10 to 10 (the sources' ceiling: 7.6 and 7.8):

| Notes | Judge | Mean | Right | Zero | Wrong | Contradicts |
|---|---|---|---|---|---|---|
| the team's (`conoscenza/`) | gemma4 | 1.3 | 9 | 2 | 9 | 10 |
| the team's (`conoscenza/`) | qwen3 | 4.2 | 15 | 1 | 4 | 10 |

Below the fourth run. The team wrote 10 notes of topics in three tasks
(the fourth: 11 in five) and 28 exercises in two. Seven questions lost
most of their points against the fourth run: the WHO's pregnant and
postpartum women, chronic conditions and sedentary behaviour (q04-q06),
the progression for hypertension (q13), the time knee arthritis takes to
answer exercise (q14) and the postpartum programme (q15). There the notes
are thinner: the one after childbirth does not say that a woman active
before pregnancy can continue. The judges are strict on a partial answer
too: q05's answer gives the expected point and scores -5 with both. One
run is an example, not a measure.

**Plans.**

| Person | gemma4 | qwen3 |
|---|---|---|
| giulia | 8/8 | 7.5/8 |
| franco | 8/8 | 8/8 |
| sara | 6.5/8 | 8/8 |
| marco | 7.5/8 | 7.5/8 |
| luca | 7.5/8 | 6.5/8 |

**App.** `main` built and tested (the team's tests, 14, green),
published, used on a computer and on a phone: 24 of 24 steps, no error
in the console, no failed request. Five pull requests, all merged (1 to
5); none closed by the pipeline (the fourth run: three).

**Cost.** 23 tasks (all completed), 24 attempts, 7.73 USD and 13.7 M
tokens counted. 39 minutes from the objective to the last task that
moved. 5 approvals from a person, each within two minutes of the
request.

| Role | Tasks | Attempts | USD | Tokens |
|---|---|---|---|---|
| coordinatore | 1 | 1 | 0.52 | 0.2 M |
| conoscenza | 12 | 12 | 3.57 | 5.5 M |
| sviluppo | 5 | 5 | 1.67 | 3.1 M |
| revisore | 5 | 6 | 1.96 | 4.8 M |

## What happened

1. The coordinator split the objective into 12 tasks: three of topics,
   the catalogue in two, the plans in three, the app in two (the data with
   its tests, then the pages), two reviews. Each review depended on all
   the tasks whose work it checks; the app's data on the plans only.
2. The knowledge, the catalogue and the plans were written at the first
   attempt in 18 minutes (2.53 USD). The review of the topics and the
   catalogue against the sources stopped once at the reviewer's 30 turns
   (0.61 USD, priced) and closed at the second.
3. The app: the data (11 files, a script that makes them, 7 tests) and
   the pages passed at the first attempt. Each developer's report says it
   ran the tests and the build ("7 test verdi, build ok"), and the
   pipeline's run of the same checks on its branch agreed.
4. All the sub-tasks closed, and the coordinator gave four tasks after
   the two reviews: the plans of Franco and Luca, the notes and the
   catalogue, the data again, a final review. The final review depended on
   the data's realignment too, so it checked the data that `main` had
   after it.
5. Two more rounds, each on a smaller defect (left open by the final
   review, then Franco's plan again), each corrected at the first attempt
   with the data realigned and approved. The last review found nothing
   open, and the coordinator closed the objective with its report and the
   limits it declares.

## What gosidian learns

- **The developer that runs its tests passes at the first attempt**: five
  tasks out of five (the fourth run: three of five sent back on the
  agent's own test). Its cost fell from 4.87 to 1.67 USD, and the run's
  from 12.99 to 7.73 USD in half the time.
- **The retry from the agent's branch did not happen**: no developer's
  task went back. The lab's test of phase 1g is the only run of it.
- **Reviews wait for the work they check**: the final review checked the
  realigned data. The coordinator still used four rounds of five on small
  defects (Franco's plan, as Sara's in the fourth run).
- **Still open**:
  - the reviewer's 30 turns are short for a review against the sources;
  - the developer copies the notes into `data/sorgenti/` by hand, since
    its commands do not reach the vault;
  - nobody of the team opens the app in a browser: the pipeline's look at
    the preview is the only check at 390 px.
