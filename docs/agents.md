# Teams of agents

A team of agents works on a project's tasks by itself: each task is a
note, each role of the team an agent that a pipeline runs, and gosidian
holds the tasks, counts what they cost and keeps the limits (IMP-195). The
people follow the work in the notes, answer the questions, approve, and
change the team by editing a note.

- **A task** is a note of the project's `tasks/` folder, a row of the
  database `tasks.md`: what to do, the role that does it, the tasks it
  waits for, the objective it is part of.
- **A team** is a note `type: team` with a ```team block: its roles, the
  agent and the executor of each, what each may touch, the budget.
- **A pipeline on task** takes the ready tasks of some roles: when a task
  is ready, gosidian takes it for a new run of the pipeline, which gives
  it to its agent and closes it with the agent's report.

`memory_create_team` writes a team to start from: its note, an agent
note per role, the [pipelines](#the-pipelines-of-a-team) that give its
tasks to their agents and its objectives to its coordinator, the
database of the tasks and a note that shows them.

## A team

```team
coordinator: capo            # the role that receives the objectives
split: context               # or craft: how the coordinator splits them
budget: {day_usd: 20, task_usd: 3}
max_agents: 3                # agents of the team at work at once
max_rounds: 5                # the coordinator's runs on an objective
alert: ["#lab/agents"]       # where the pipelines send what they tell
approvers: [alice]
roles:
  capo:
    agent: "[[fitness/agents/capo]]"
    run: claude.run
    model: sonnet
    delegates_to: [knowledge, dev]
  dev:
    agent: "[[fitness/agents/dev]]"
    run: claude.run
    model: sonnet
    repo: agents-fitness
    paths: [web/, src/]
    forbid: [package.json]
    checks: npm test && npm run build
  knowledge:
    agent: "[[fitness/agents/knowledge]]"
    run: pi.run
    writes: [fitness/knowledge]
```

| Field | |
|---|---|
| `coordinator` | the role that receives the objectives and splits them into tasks; none for a team whose tasks people write |
| `split` | `context` (default): each task a whole piece of the work, with what it needs to know; `craft`: a chain of crafts, each passing its result on. It changes what the coordinator is told |
| `budget` | `day_usd`: what the team may spend in a day of the server's [time zone](configuration.md#time-zone); `task_usd`: the budget of a task that gives none. 0: no limit |
| `max_agents` | the team's agents at work at once; 0: no limit of the team |
| `max_rounds` | how many times the coordinator runs on one objective after its sub-tasks closed (default 5): then a person decides |
| `alert`, `approvers` | for the pipelines: where they tell, who approves |
| `roles` | the roles, by name |

A role:

| Field | |
|---|---|
| `agent` | its agent note: the note's body, below its title, is the agent's system prompt |
| `run` | the node that runs it: a plugin's `<name>.run` of the [agent-run contract](plugins.md#agent-nodes-agent-run1) (`claude.run`, `pi.run`) |
| `model`, `effort`, `max_turns`, `max_seconds`, `max_tokens` | for the executor, within its preset (`max_tokens` counts the cache reads too: a role that reads and writes much, as an app's, may need more than the preset's default) |
| `repo` or `host` | the repository its executor works on, or the host its commands run on |
| `paths`, `forbid` | the paths of the repository it may and may not change (a folder ends with `/`) |
| `checks` | the script that tests its work, in the repository's folder (`npm test && npm run build`) |
| `preview` | the folder of the build its checks make (`dist`): it is published and looked at before a person approves |
| `writes` | the folders of the vault its MCP token may write. Without, a run of the role gets no write token. A role that writes must write: a task whose agent wrote no note there goes back, as a repository's that changed nothing |
| `delegates_to` | the roles it may give tasks to. One level: a role that receives from another gives to none |
| `approve` | `merge` (a person approves before its pull request is merged), `ops` (before a command runs on a host) or `none`; default `merge` with `repo`, `ops` with `host`, else `none` |

A role with `checks` or `preview` needs a `repo`, `approve: merge` too,
and a role whose work a person approves needs the team's `alert`. The
lint rule `team-invalid` reports a block that does not read, on its line,
and a role whose agent note is missing.

## A task

```yaml
---
title: The beginner's plan
type: task
status: submitted
team: "[[fitness/team]]"
assignee: dev
parent: "[[fitness/tasks/20261025-093000-the-app]]"
depends_on: ["[[fitness/tasks/20261025-093010-exercises]]"]
paths: [web/]
budget_usd: 2
tags: [fitness, type:task]
---

# The beginner's plan

What to do, and how it is accepted.
```

The states are A2A's, and `review`:

| `status` | |
|---|---|
| `submitted` | waiting to start |
| `working` | at work, or an objective waiting for its sub-tasks |
| `input-required` | waits for a person's answer |
| `review` | the work is done and waits for a person's approval |
| `completed`, `failed`, `canceled` | closed |

The server keeps `claimed_by` (who holds it: `run-<id>`, or an outside
agent's token), `lease_until` (an outside agent's), `attempts`, `rounds`,
`cost_usd` and `tokens`. People change `status` by hand: `canceled` to
stop a task, `submitted` to start it again.

When a task closes, its `## Report` section holds the agent's report, the
status, the holder, the attempt and the cost: the tasks that depend on it
and the coordinator read it (the last `## Report`, for a task that closed
twice). The headings of a text the server writes into a task's section,
an agent's report or a message, go two levels down (`#` to `###`), but in
its code blocks: the section ends at the next `##`, and holds all of it.

## The pipelines on task

```pipeline
trigger: {on: task, team: team, roles: [dev, knowledge]}
nodes:
  - id: ctx
    use: task.context
  - id: agent
    use: claude.run
    with:
      prompt: "{{ ctx.prompt }}"
      system: "{{ ctx.system }}"
      max_cost: "{{ ctx.max_cost }}"
      mcp: write
  - id: done
    use: task.complete
    with:
      report: "{{ agent.text }}"
```

The trigger `task` takes the tasks of `team` (a link to its note; none:
every team of the project) and of `roles` (none: all), or with
`coordinator: true` only the team's coordinator's, with `false` only the
others', on its `events`:

| Event | Starts when | `trigger` holds |
|---|---|---|
| `ready` (default) | a task is `submitted`, every task it depends on is `completed`, nobody holds it and its team has budget left | `event`, `task`, `title`, `team`, `role`, `project`, `attempt` |
| `children_done` | an objective (a task with sub-tasks), `working` and held by nobody, has all its sub-tasks closed, or one `failed` or `input-required`: once for each state of those (of all the sub-tasks once they are all closed; while some are at work, of the ones that failed or wait for a person, so a failed one calls the coordinator once, not again each time another starts or closes), at most `max_rounds` times | as `ready`, with `round` and `children` (`path`, `title`, `status`, `assignee`, `cost_usd`) |
| `budget` | the team spent its `day_usd`: once a day | `event`, `team`, `project`, `day`, `spent_usd`, `budget_usd` |

The first pipeline of the project, in the order of the paths, that takes
a task's event starts it. The server reads the tasks every minute, and a
second after a task is written.

**Holding a task.** The trigger takes a ready task for the run as it
starts it: `working`, `claimed_by: run-<id>`, one attempt more. Two
starts never take the same task. When the run ends:

- a task the run closed, or put in `input-required`, stays so;
- an objective with sub-tasks still open stays `working`, held by nobody:
  it waits for them;
- a run that failed gives the task back, `submitted`, with the error in a
  dated section; at the `[agents] max_attempts`-th attempt (3) the task
  goes `failed`;
- a run canceled, or one that ended without closing its task, puts it in
  `input-required`: a person decides; so does a run that ends while the
  task waits in `review` for an approval that will not come.

A run that ended never holds a task again: a task whose `attempts` a
person set back by hand gets a new run, not its old one.

**The nodes.**

| Node | `with` | Output |
|---|---|---|
| `task.context` | `task` (default `{{ trigger.task }}`), `prompt` (text added at the end) | `task`, `body`, `team`, `role`, `system` (the agent note's body), `prompt` (the task for its agent: the role, the task, its paths, the reports of the tasks it depends on; for a coordinator, the roles it gives tasks to, with the repository or host each sees, or the vault only, and its sub-tasks' reports), `max_cost` (the task's budget, or the team's `task_usd`), `paths`, `forbid`, `depends_on`, `children` |
| `task.update` | `task`, `status` (`working`, `input-required`, `review`, `submitted`), `message` (a dated section of the task) | `task` |
| `task.complete` | `task`, `status` (`completed`, the default, `failed`, `canceled`), `report` | `task` |
| `task.run` | `task`, `prompt` (added at the end), `plan` (for a coordinator: ask for a plan); any other setting goes to the executor and wins | the executor's output: it runs the role's `run` node with the prompt of `task.context`, the agent note as `system`, the role's model, effort and limits, the task's budget as `max_cost`, the role's `repo` (with `access: edit` and a pull request titled as the task) and `mcp: write` when the role `writes`, `read` otherwise; and `next`: `checks` and `preview`, true when the role has them and the agent pushed a branch, for the `when` of the nodes that run them |
| `task.gate` | `agent` (`{{ agent }}`), `checks` (`{{ checks }}`, exec.run's output), `preview` (`{{ look }}`, preview.check's), `choices` (the labels of `approve`, `redo`, `discard`) | `ok`, and what follows: `close` (no approval needed), `ask` (a person approves), `redo` (another attempt is left), `fail`; `files`, `outside`, `forbidden` (the files the agent changed against the role's and task's paths and forbid), `notes` (the notes the run wrote in the role's `writes`), `pr_url`, `branch`, `image` (the preview's screenshot), `link`, `title`, `text` (for the person and the report), `feedback` (for another attempt), `choices` |
| `task.verdict` | `gate`, `answer` (wait.call's: `choice`, `by`, `comment`) | `close` with `status`, `merge`, `close_pr`, `redo`, `report`, `feedback` |
| `task.pr` | `url`, `action` (`merge`, `close`) | the output of the executor plugin's `pr` node, which merges or closes with its preset's credentials |
| `task.plan` | `plan` (`{{ plan.json }}`) | `action`, `close`, `ask`, `wait`, `summary`, `question`, `created` (`key`, `path`, `assignee`, `title`) |

**The report.** What the gate says, and the task's `## Report` after it,
starts with what the pipeline found after the agent (the files, the notes
written, the checks it ran and how they ended, the preview), then the
agent's own words; an approval adds the person's answer and the merge or
close of the pull request. The agents run no checks themselves: the
coordinator and the tasks that depend on one read the pipeline's part as
facts, and their prompt says so.

Only the run that holds a task moves or closes it, with these nodes: the
agent of the run moves no task with the MCP tools, its own neither, so it
never closes its work past the checks and approvals of its pipeline. It
gives its report as its answer. `task.update` with
`submitted` lets it go for another attempt (a «redo» after a review),
with what the attempt must fix in `message`.

An agent node of the [agent-run contract](plugins.md#agent-nodes-agent-run1)
also takes `role: "[[fitness/agents/dev]]"`: gosidian reads the agent
note and gives its body as `system`, before the node's own.

## The pipelines of a team

`memory_create_team` writes two pipelines, which a team changes as it
likes.

**`pipelines/<team>-work.md`** takes the ready tasks of every role but
the coordinator (`coordinator: false`):

1. `task.run`: the role's agent works on the task;
2. with the role's `checks`, `exec.run` runs them on the branch the agent
   pushed (`when: "{{ agent.next.checks }}"`: never on a branch that is
   not there), and gives their exit code to the gate
   (`fail_on_error: false`); with its `preview`, the build is published
   and a screenshot taken when the checks passed
   (`unless: "{{ checks.exit_code }}"`). These nodes go on after an error
   (`on_error: continue`): the gate gives the work back and its pull
   request is closed, instead of a run that fails with the pull request
   open;
3. `task.gate` looks at what the agent did: a file in `forbid` or
   outside `paths`, a role with a repo whose agent changed nothing, a role
   that writes in the vault whose agent wrote no note there, checks that
   fail (an exit code other than 0, a signal, the time out) or did not
   run, a preview that was not published or does not open, and the work
   goes back;
4. a role that needs an approval (`merge`, `ops`): the task goes in
   `review`, `notify.ask` asks the team's `alert` and `approvers` with the
   files, the checks, the agent's report, the pull request and the
   screenshot, and `wait.call` waits for the answer (24 hours);
5. `task.verdict`: **approve** merges the pull request (`task.pr`) and
   completes the task; **redo** closes the pull request and gives the
   task back, `submitted`, with what to fix in a dated section, which the
   next attempt reads; **discard** closes the pull request and cancels
   the task. Without an attempt left, redo is not offered and a gate that
   fails fails the task.

`task.pr` calls the `pr` node of the executor's plugin (`claude.pr`,
`pi.pr`): the plugin merges and closes with the credentials of its
preset, and deletes the agent's branch; gosidian never holds them.

**`pipelines/<team>-coordinate.md`** takes the coordinator's objectives
(`coordinator: true`, `ready` and `children_done`): `task.run` with
`plan: true` asks its agent for a plan as JSON, and `task.plan` writes
it.

## The coordinator

A coordinator is a role with `delegates_to`, and a pipeline on task that
takes its role's `ready` and `children_done`:

1. a person (or another tool) writes an objective for the coordinator;
2. its agent answers with a plan: `tasks` (each for a role it gives tasks
   to, with a key of its own and `depends_on` other keys or tasks), or
   `wait` when the sub-tasks at work are enough (one failed and the others
   cover it), or `done` with a summary, or `ask` with a question. `task.plan` checks the
   whole plan (the roles, one level, `[agents] max_tasks`, the keys) and
   only then writes the tasks, as the server: the agent writes none;
3. the run ends: the objective waits for its sub-tasks, held by nobody;
4. when they closed (or one failed or asks for input), the coordinator
   runs again with their reports: it gives more tasks, closes the
   objective (`task.complete` with its summary), or asks a person
   (`input-required` with its question).

A coordinator's agent may also give tasks itself, with
`memory_task_create`: its MCP token, even a read one, creates tasks only
for the roles it delegates to, under its own task.

A coordinator's run that neither closes the objective nor gives it new
tasks puts it in `input-required`, and so does the `max_rounds`-th round.

## Limits

- **Budget.** The cost of a step is the `cost` and `usage.total_tokens`
  of its output (the agent-run contract), stored with the step and added
  to its task. A step that fails after spending (an agent stopped at its
  `max_tokens`) counts too when its plugin says what it spent in the
  error's structured content (claude and pi 0.3.2). A team's
  spend is the sum over its runs in the day; a ready task does not start
  while the team spent its `day_usd` (`memory_tasks` says so), and the
  event `budget` starts once a day. A run already at work ends: the
  task's budget reaches its agent as `max_cost`, which the plugin keeps.
- **Agents at once.** The steps of a task's run that run an agent take a
  place in `team:<project>/<team>` (`max_agents`) and in `agents`
  (`[agents] max_running`, for the whole server) of the
  [queue](pipelines.md#calls-at-once): the Runs window shows them.
- **One level** of delegation, `max_rounds`, `[agents] max_tasks`
  sub-tasks of an objective (50), `max_attempts`.
- **Writes.** A run of a team's task gets an MCP token that writes only in
  its role's `writes` (the task tools aside), and none without them.

`[agents]` in the [configuration](configuration.md#agent-teams) sets the
server's limits.

## Outside agents

An agent outside gosidian (a Claude Code session, a script) works on the
tasks over MCP with its token:

- `memory_tasks`: the tasks of a project, with why a submitted one does
  not start, and each team's spend of the day;
- `memory_task_claim`: takes a ready task, with a lease of `[agents]
  lease` (15 minutes);
- `memory_task_heartbeat`: renews the lease; a lease that ends gives the
  task back, as a failed attempt;
- `memory_task_update`, `memory_task_complete`: as the nodes;
- `memory_task_create`: gives a task to a role.

The task tools are in the `core` profile of the tokens.

## The board

The database `tasks.md` shows the tasks as a board by status, and each
task its sub-tasks and the tasks that wait for it. The note
`<team>-tasks.md` that `memory_create_team` writes groups a team's tasks:
what waits for a person (`input-required`, `review`), what is at work, the
board, what is done.
