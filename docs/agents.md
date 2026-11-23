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

The [agents preset](deployment.md#presets) starts gosidian with what a
team needs: pi for the agents, exec for the checks, preview and notify.
`memory_create_team` writes a team to start from: its note, an agent
note per role, the [pipelines](#the-pipelines-of-a-team) that give its
tasks to their agents and its objectives to its coordinator and tell its
alert when it stops by itself, the database of the tasks and a note that
shows them.

## A team

```team
coordinator: capo            # the role that receives the objectives
split: context               # or craft: how the coordinator splits them
budget: {day_usd: 20, task_usd: 3}
max_agents: 3                # agents of the team at work at once
max_rounds: 5                # the coordinator's runs on an objective
alert: ["#lab/agents"]       # where the pipelines send what they tell
approvers: [alice]
guide: "[[fitness/rules]]"   # the rules every role follows
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
    skills: ["[[fitness/skills/vitest]]"]
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
| `reopen_on` | which problems of a review (a role with `review: true`) reopen the work. `serious` (default): the coordinator is told to give no task for a review that leaves only minor problems open, to list them in its summary as left open, and to bring what is made from the reviewed work in line with it once, after the review that leaves no serious problem. `any`: every problem reopens it. It changes what the coordinator is told: the plan stays its agent's |
| `alert`, `approvers` | for the pipelines: where they tell, who approves |
| `guide` | a note of rules every role follows: its agent receives it [in its system prompt](#guides-and-skills) |
| `rooms` | the [rooms](#rooms) its roles give objectives to: a name, and the note of the room's team (`front: "[[fitness/front]]"`) |
| `roles` | the roles, by name |

A role:

| Field | |
|---|---|
| `profile` | a [profile](#profiles) whose fields the role takes where it writes none: its name in the project's `profiles/` folder (`reviewer`), or a link to a note of the project |
| `agent` | its agent note: the note's body, below its title, is the agent's system prompt |
| `run` | the node that runs it: a plugin's `<name>.run` of the [agent-run contract](plugins.md#agent-nodes-agent-run1) (`claude.run`, `pi.run`) |
| `model`, `effort`, `max_turns`, `max_seconds`, `max_tokens` | for the executor, within its preset (`max_tokens` counts the cache reads too: a role that reads and writes much, as an app's, may need more than the preset's default) |
| `access` | its agent's level of the agent-run contract: `read`, `edit` (also writes files) or `exec` (also runs commands, in its executor's sandbox: its checks too, before the pipeline runs them again); default the executor's, `edit` with a `repo`. `read` with a `repo`: the agent reads a clone of its base branch, the work merged so far, and changes none of its files: no commit, no pull request, nothing for a person to approve, and the gate judges it on its report, as a role without a repository (a reviewer of an app's files: make its task depend on the tasks whose work it reads). `exec` needs a preset that allows it (Bash for `claude`), and the commands reach what the executor's container reaches: give it on purpose. The commit takes every file the agent leaves that the repository does not ignore: the agent is told to delete what its checks built, and a `.gitignore` with the build's folder (`dist/`) spares it |
| `repo` or `host` | the repository its executor works on (or reads, with `access: read`), or the host its commands run on |
| `paths`, `forbid` | the paths of the repository it may and may not change (a folder ends with `/`); none for a role that reads it |
| `checks` | the script that tests its work, in the repository's folder (`npm test && npm run build`) |
| `preview` | the folder of the build its checks make (`dist`): it is published and looked at before a person approves |
| `writes` | the folders of the vault its MCP token may write. Without, a run of the role gets no write token. A role that writes must write: a task whose agent wrote no note there goes back, as a repository's that changed nothing |
| `delegates_to` | the roles it may give tasks to, and the [rooms](#rooms) it may give objectives to. One level: a role that receives from another gives to none |
| `approve` | `merge` (a person approves before its pull request is merged), `ops` (before a command runs on a host) or `none`; default `merge` with a `repo` it changes, `ops` with `host`, else `none` |
| `guide` | a note of rules of the role, after the team's [in its agent's system prompt](#guides-and-skills) |
| `skills` | notes `type: skill` its agent receives whole, after the guides; at most 20 |
| `tools` | the [plugins' tools](#a-roles-tools) its agent sees through gosidian's MCP: a plugin's name (`vec-pg`, all its tools) or one tool's (`vec-pg_search`); `[]` for none. Without it, every plugin exposed in the project, as before |
| `review` | `true`: the role judges the work of others. Its prompt asks it to end its report with a list item for each problem left open, starting with `[serious]` or `[minor]`, and a line `VERDICT: passed` or `VERDICT: to correct`. The gate counts them among the pipeline's findings (`The review: to correct, 2 serious and 1 minor problems left open.`, and `review` in its output: `verdict`, `serious`, `minor`); a report without the verdict line, empty or cut goes back |

A role with `checks` or `preview` needs a `repo` it changes (not
`access: read`), `approve: merge` too,
and a role whose work a person approves needs the team's `alert`. The
lint rule `team-invalid` reports a block that does not read, on its line,
a role whose agent note is missing, and a guide or a skill missing from
the project or a skill that is no note `type: skill`.

### Profiles

A profile is a note `type: profile` of the project with one ```profile
block: the fields of a role that are not of a team's repository, host or
folders. A role that names it takes those it does not write itself, so
one note gives several roles, in several teams, their executor and model,
their guide, skills and tools.

```profile
run: claude.run
model: sonnet
access: read
tools: [vec-pg]
skills: [skills/sql]
guide: guides/data
```

```team
roles:
  numbers:
    profile: analyst           # profiles/analyst.md
    model: opus                # the role's own field wins
```

| Field | |
|---|---|
| `agent`, `run`, `model`, `effort`, `max_turns`, `max_seconds`, `max_tokens`, `access`, `review`, `guide`, `skills`, `tools` | as a role's; a role's own field wins, a list too (`tools: []` in the role, none) |

The team reads with the profile's fields as if the role wrote them, and
its checks hold for what the role ends up with: a profile with `access:
read` given to a role with a `repo` makes it read the repository, and a
reviewer's profile makes it judge. A profile that is missing, or holds a
field of a team's repository (`repo`, `paths`, `writes`…), is a problem
of the team, on the role's line: the team does not run until it reads. A
profile changed changes every role that takes it, at its next run.

`memory_create_team` writes three profiles to start from in a project
without them, each with the team's executor: `developer` (`access:
edit`), `reviewer` (`access: read`, `review: true`) and `analyst`
(`access: read`, with the tools and skills to fill in). The lint rule
`team-invalid` reports a profile note that does not read too.

### A role's tools

The plugins a project's owner exposed in it (`gosidian plugin expose`)
offer their tools to the agents of the project: an archive's search
(`vec-pg_search`), the layout engine's (`layout_open`, `layout_render`).
A role's `tools` narrows what its agent sees of them:

```yaml
  analyst:
    run: claude.run
    tools: [vec-pg, layout_render]   # every tool of vec-pg, one of layout
  writer:
    run: pi.run
    tools: []                         # no plugin's tool
```

The MCP tokens of the role's runs carry the list: a tool outside it is
neither listed nor callable (`tools/list`, `tools/call`). It narrows and
never widens: a plugin not exposed in the project stays out, and so does
a write tool for a role that reads. gosidian's own tools (`memory_*`, the
task tools) stay. The executor's own tools (Claude Code's Bash, pi's
read) are something else: its preset and the role's `access` set them.
The coordinator reads a role's tools (`tools vec-pg, layout_render`, `no
plugin's tools`), and [the office](#the-office) says which of them name a
plugin not exposed in the project.

### Guides and skills

A role's agent receives in its system prompt, after its agent note's
body: the team's `guide`, the role's own `guide`, then its `skills`, each
whole below a heading with its title and path (`# Guide: Rules
(fitness/rules.md)`, `# Skill: Vitest (fitness/skills/vitest.md)`, with
the skill's `description` first). A note the team and the role both name
comes once. They are notes of the team's project: a link without the
project (`skills/vitest`) is in it. Their bodies, below their titles,
together at most `[agents] max_prompt_notes` (64 KiB): a role over it,
a skill that is no note `type: skill`, or a note that is missing stops
the run before its agent, and the attempt fails with why. A coordinator
reads the skills of the roles it gives tasks to (`skills vitest`), to
pick the role for a task. The system prompt is the same in every turn
of a step, which a provider that caches prompts reads again for less;
still, each skill is in every step of the role: give it the few its
tasks need.

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

The states are A2A's, with `review` and `backlog`:

| `status` | |
|---|---|
| `backlog` | not ready yet: nothing takes it until a person moves it to `submitted` (a [board](board.md)'s first columns) |
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
| `budget` | the team spent its `day_usd`: once a day | `event`, `team`, `project`, `day`, `spent_usd`, `budget_usd`, `alert` (the team's), `title` and `text` (a message ready to send) |
| `waiting` | a run of the team is [held](#limits) by something outside it: once, until none of its runs is held | `event`, `team`, `project`, `task`, `why`, `retry_at`, `alert`, `title`, `text` |

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
| `task.context` | `task` (default `{{ trigger.task }}`), `prompt` (text added at the end) | `task`, `body`, `team`, `role`, `system` (the agent note's body, with the role's [guides and skills](#guides-and-skills)), `prompt` (the task for its agent: the role, the task, its paths, the reports of the tasks it depends on; for a coordinator, the roles it gives tasks to, with the repository or host each sees, or the vault only, and its sub-tasks' reports), `max_cost` (the task's budget, or the team's `task_usd`), `paths`, `forbid`, `depends_on`, `children` |
| `task.update` | `task`, `status` (`working`, `input-required`, `review`, `submitted`), `message` (a dated section of the task), `branch` (the branch the next attempt continues, `{{ verdict.branch }}`; empty for none) | `task` |
| `task.complete` | `task`, `status` (`completed`, the default, `failed`, `canceled`), `report` | `task` |
| `task.run` | `task`, `prompt` (added at the end), `plan` (for a coordinator: ask for a plan); any other setting goes to the executor and wins | the executor's output: it runs the role's `run` node with the prompt of `task.context`, the agent note with the role's guides and skills as `system`, the role's model, effort and limits, the task's budget as `max_cost`, the role's `access`, the role's `repo` (with `access: edit` unless the role says another, a pull request titled as the task, and the task's `branch` with `resume` when an attempt sent back left one and the executor's plugin takes it) and `mcp: write` when the role `writes`, `read` otherwise; and `next`: `checks` and `preview`, true when the role has them and the agent pushed a branch, for the `when` of the nodes that run them |
| `task.gate` | `agent` (`{{ agent }}`), `checks` (`{{ checks }}`, exec.run's output), `preview` (`{{ look }}`, preview.check's), `choices` (the labels of `approve`, `redo`, `discard`) | `ok`, and what follows: `close` (no approval needed), `ask` (a person approves), `redo` (another attempt is left), `fail`; `files`, `outside`, `forbidden` (the files the agent changed against the role's and task's paths and forbid), `notes` (the notes the run wrote in the role's `writes`), `pr_url`, `branch`, `resume` (another attempt may continue the branch: the agent pushed it, within its paths), `image` (the preview's screenshot), `link`, `title`, `text` (for the person: the pipeline's findings and the beginning of the agent's report), `report` (the same with the whole report, for the task's note; `task.verdict` reads it), `feedback` (for another attempt), `choices` |
| `task.verdict` | `gate`, `answer` (wait.call's: `choice`, `by`, `comment`) | `close` with `status`, `merge`, `close_pr`, `redo`, `report`, `feedback`; `branch` (the branch the next attempt continues, for `task.update`; empty when it starts from the base) and `delete_branch` (for `task.pr`: closing the pull request deletes its branch) |
| `task.pr` | `url`, `action` (`merge`, `close`), `delete_branch` (default true) | the output of the executor plugin's `pr` node, which merges or closes with its preset's credentials |
| `task.plan` | `plan` (`{{ plan.json }}`) | `action`, `close`, `ask`, `wait`, `summary`, `question`, `created` (`key`, `path`, `assignee`, `title`), `repointed`, `skipped`, `ordered` and `ignored` (a `replaces` that names no failed or canceled sub-task) when it [put the plan in order](#the-coordinator), `lowered` (the tasks whose `budget_usd` was above the team's `task_usd`: `key`, `asked_usd`, `budget_usd`) |

**The report.** What the gate says, and the task's `## Report` after it,
starts with what the pipeline found after the agent (the files, the notes
written, the checks it ran and how they ended, the preview), then the
agent's own words; an approval adds the person's answer and the merge or
close of the pull request. An agent runs checks only with `access: exec`,
and then the pipeline runs them again: the coordinator and the tasks that
depend on one read the pipeline's part as facts, and their prompt says
so. The agent of a role with `checks` is told the command, and whether it
may run it.

The task's note keeps the agent's whole report, up to `[agents]
max_report` (64 KiB); the message to a person and the feedback for
another attempt carry its first 3,000 bytes. A task that depends on
another, and a coordinator, read the other task's report up to `[agents]
max_prompt_report` (16 KiB): one cut says so, with the note that holds
the rest. An answer cut at the model's output limit (`stop: length`) is
said among the pipeline's findings, and so is why a node of the checks
or of the preview failed.

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

`memory_create_team` writes three pipelines, which a team changes as it
likes.

**`pipelines/<team>-alert.md`** takes `budget` and `waiting` and sends
the event's `title` and `text` to the team's `alert` (`notify.send`): a
team that stops by itself says so once. A team without `alert` sends
nothing.

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
   outside `paths`, a role that changes a repo whose agent changed nothing, a role
   that writes in the vault whose agent wrote no note there (the notes
   of an attempt before, ended at a limit, count: the server remembers
   them until the task closes or it restarts), a role that changes no
   repository and has no `writes` whose answer is empty or cut (its
   report is its work), a role that reads a repository its executor did
   not give it, checks that
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

**Another attempt continues the branch.** When the gate or a person sends
a task back, and the agent pushed its branch within the role's paths, the
pull request is closed and its branch kept (`delete_branch: false`); the
task keeps it in `branch`, and the next attempt continues it (`resume`):
its agent finds the files as the attempt before left them, and reads in
the task what went wrong. A branch with a file the role must not change,
or outside its paths, goes with its pull request, and the next attempt
starts from the base. A plugin too old for `resume` (claude and pi before
0.4.0) starts each attempt from the base, as before.

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
   only then writes the tasks, as the server: the agent writes none. It
   also keeps the plan in order by itself, and its output says what it
   did:
   - a task with `replaces` (the path of a sub-task that failed or was
     canceled) takes the tasks that waited for that one (`repointed`);
   - a task with the role and the title of a sub-task still open is not
     written again: the open one stands for its key (`skipped`);
   - two open tasks of roles on the same repository whose `paths` may
     meet (always, when one of them names none) work one after the
     other: the later waits for the earlier, unless one already waits
     for the other (`ordered`). Side by side, each from the base, their
     pull requests would conflict;
3. the run ends: the objective waits for its sub-tasks, held by nobody;
4. when they closed (or one failed or asks for input), the coordinator
   runs again with their reports: it gives more tasks, closes the
   objective (`task.complete` with its summary), or asks a person
   (`input-required` with its question).

A coordinator's agent may also give tasks itself, with
`memory_task_create`: its MCP token, even a read one, creates tasks only
for the roles and rooms it delegates to, under its own task.

### Rooms

A room is a team that another gives objectives to: a sub-team with its
own coordinator, budget and limits, and its own pipelines. The team lists
it in `rooms`, by a name its roles delegate to:

```team
coordinator: capo
budget: {day_usd: 20}
rooms:
  front: "[[fitness/front]]"     # a note type: team with a coordinator
roles:
  capo:
    run: claude.run
    delegates_to: [knowledge, front]
```

- A task the coordinator gives to `front` (in its plan, or with
  `memory_task_create` and `assignee: front`) is an objective of the
  room's team, for the room's coordinator, a sub-task of the team's
  objective. The room's coordinator splits it among its roles as any
  objective, and its summary closes it: the team's coordinator then reads
  it as the report of that sub-task. The coordinator's prompt says what
  the room is: its team and its roles.
- The room's runs spend of the team's budget too: the team's `day_usd`
  counts what the team and its rooms spent today, and a room's task waits
  while either its own day or its team's is spent (`memory_tasks` says
  which). Its `max_agents` and `max_rounds` are its own.
- One level more: a room has no rooms, it is one team's, and only a
  room's objective, a sub-task, has sub-tasks of its own.
- The lint rule `team-invalid` reports a room whose note is missing, does
  not read as a team, has no coordinator or rooms of its own, or is the
  room of two teams. The office draws a team's rooms beside it.

A coordinator's run that neither closes the objective nor gives it new
tasks puts it in `input-required`, and so does the `max_rounds`-th round.
There the rounds start again from none: the person who puts the
objective back to `working` gives it `max_rounds` more, and the
coordinator runs on what is there.

## Limits

- **Budget.** The cost of a step is the `cost` and `usage.total_tokens`
  of its output (the agent-run contract), stored with the step and added
  to its task. A step that fails after spending (an agent stopped at its
  `max_tokens`) counts too when its plugin says what it spent in the
  error's structured content (claude and pi 0.3.2). A team's
  spend is the sum over its runs in the day; a ready task does not start
  while the team spent its `day_usd`, nor does the coordinator of an
  objective whose sub-tasks closed (`memory_tasks` says so for both), and
  the event `budget` starts once a day. A run already at work ends: the
  task's budget reaches its agent as `max_cost`, which the plugin keeps.
  The budget a coordinator's agent gives a task in its plan is at most
  the team's `task_usd` (a person may write more in the task's note),
  and `max_cost` is at most what the executor's preset lets a step spend
  (the `maximum` of `max_cost` in its input schema; none is sent to a
  preset that lets it spend nothing, a free endpoint). A task's
  `cost_usd` counts every attempt: its note says what each attempt that
  failed spent, and what the one that closed it did.
- **Held, not failed.** In a task's run, a step whose plugin is away
  (unreachable, restarting, serving a manifest nobody accepted yet) or
  says it waits for a limit outside it (the provider's key, the
  subscription's quota: `wait` in the
  [agent-run contract](plugins.md#agent-nodes-agent-run1)) does not
  fail: it goes back to the queue in the same attempt and tries again
  after a minute, then twice as long each time, at most `[agents]
  hold_max` (an hour), or when the plugin says the limit ends, if
  sooner. The task stays `working`, its attempts do not grow, what the
  step spent before counts; `memory_tasks` says why it waits and when it
  tries again, and the event `waiting` starts once. Canceling the run
  ends the wait: the task then asks a person.
- **A queue that cannot move.** An objective whose open sub-tasks all
  wait behind one that failed, was canceled or is no task goes to
  `input-required` with the reason, once its coordinator had its say on
  the failed one: a person points the dependencies elsewhere, cancels
  what is overtaken or closes it.
- **Agents at once.** The steps of a task's run that run an agent take a
  place in `team:<project>/<team>` (`max_agents`) and in `agents`
  (`[agents] max_running`, for the whole server) of the
  [queue](pipelines.md#calls-at-once): the Runs window shows them. They
  also take a place in their executor's plugin, whose `run` bears as
  many steps at once as its workers (`PI_WORKERS`, `CLAUDE_WORKERS`,
  [plugins](plugins.md#the-manifest)), while the merge of a pull request
  takes none of them. And each holds one of the server's `[pipelines]
  workers` (default 2) as long as its agent works, minutes: give
  `workers` the agents you want at once and room for the other steps,
  or two agents at work hold every other pipeline of the server. The
  smallest of the four says how many agents work together.
- **One level** of delegation, and one more through a [room](#rooms),
  `max_rounds`, `[agents] max_tasks` sub-tasks of an objective (50),
  `max_attempts`.
- **Writes.** A run of a team's task gets an MCP token that writes only in
  its role's `writes` (the task tools aside), and none without them.
- **Tools.** Its tokens see only the plugins' tools of its role's
  `tools`, when the role names them.

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

The task tools are in the `core` profile of the tokens. They also work
on the cards of a [board](board.md): there an agent takes the board's
next card without a team instead of a role's task.

## The board

The database `tasks.md` shows the tasks as a board by status, and each
task its sub-tasks and the tasks that wait for it. The note
`<team>-tasks.md` that `memory_create_team` writes groups a team's tasks:
what waits for a person (`input-required`, `review`), what is at work, the
board, what is done.

A note `type: board` is another way to work on a project's tasks
([boards](board.md)): its columns, each a state with a limit of cards at
work, and the order of the cards in each. Its cards are tasks without a
team, which an agent in a session takes one after the other and a person
drags in the web UI. A card with a team and a role is that team's task
too: its runs take it in the board's order and within its limits, and an
objective's sub-tasks go on its board ([Teams](board.md#teams)).

## The office

The plugin `office` (IMP-233) shows a team as a map of its office: **Open
in the editor** on the team's note opens it in a window. Seen from above,
each role has a desk, with the coordinator at the head and arrows to the
roles it delegates to. Each role shows its state, its open tasks and what
its runs spent today. A list of the roles, with the keys, does the same
without the map.

| State | |
|---|---|
| waiting | a task waits for a person: `input-required`, `review` |
| held | a task waits for a budget, a limit of agents or the tasks it depends on: its `waits` |
| working | a run holds a task |
| queued | a task is ready and no run holds it yet |
| idle | no open task |

The map reads the team as the server does, with the problems of its
block on their lines. A click on a task or on an agent opens its note.

Whoever may write the project changes the team on the map too: roles,
executors and models, delegations, folders, profiles, rooms. The changes
are a draft the server checks at every step without writing, with each
problem on its role and line; **Save**, in the window's bar, writes the
block when the note did not change meanwhile: the office's page proposes
the changes, it does not write them. The server writes again only the entries that
change, so the comments, the blank lines and the fields the map does not
show stay as they are: the team is still its note. A role with open
tasks keeps its name until they are finished or given to another role. A
role added writes its agent note `agents/<role>.md` when it is missing,
and a room created the notes of a new team, as `memory_create_team`
does. The window gives the page
the team again when the note or a task changes, when a run changes, and
every 10 seconds. The page holds no credentials: it gets what the person
at the window may read ([the bridge office/1](plugins.md#the-bridge-office1)).
