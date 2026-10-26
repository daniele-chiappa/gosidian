package fleet

import (
	"fmt"
	"path"
	"strings"
)

// The notes a new team starts with (memory_create_team): the team, the
// database of the project's tasks when it has none, and a note that shows
// the team's tasks and their cost.

// DatabaseNote is the database note of project's tasks.
func DatabaseNote(project string) []byte {
	src := project + "/" + Folder
	return []byte(fmt.Sprintf(`---
title: Tasks
description: "The tasks of the project's teams of agents, one note per task in %[1]s/ (IMP-195). The server takes, releases and closes them; people cancel them or submit them again."
type: database
source: %[1]s
rows: {type: task}
fields:
  title: {type: text, required: true}
  status: {type: select, required: true, options: [%[2]s]}
  team: {type: relation, required: true}
  assignee: {type: text, required: true}
  parent: {type: relation}
  depends_on: {type: relation}
  paths: {type: list}
  forbid: {type: list}
  budget_usd: {type: number}
  cost_usd: {type: number}
  tokens: {type: number}
  claimed_by: {type: text}
  lease_until: {type: date}
  attempts: {type: number}
  rounds: {type: number}
  children_state: {type: text}
  created: {type: date}
  updated: {type: date}
row_views:
  - title: Sub-tasks
    from: %[1]s
    where: [parent contains this]
    sort: path
    columns: [title, status, assignee, cost_usd]
  - title: Tasks that wait for this one
    from: %[1]s
    where: [depends_on contains this]
    sort: path
    columns: [title, status, assignee]
tags: [%[3]s, type:index]
---

# Tasks

The tasks of the teams of this project. A task starts when it is
submitted and every task it depends on is completed; the server then
holds it for the run that works on it.

`+"```view"+`
from: %[1]s
as: board
group_by: status
columns: [title, assignee, cost_usd]
`+"```"+`
`, src, strings.Join(States, ", "), project))
}

// TeamNote is the note of a new team named name, with a role per entry
// of roles that runs with run, each with its agent note
// (<project>/agents/<role>.md); coordinator, when one of them, gives tasks
// to the others.
func TeamNote(project, name, run string, roles []string, coordinator string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\ndescription: %s\ntype: %s\ntags: [%s, type:team]\n---\n\n# %s\n\n", yamlString(name),
		yamlString("A team of agents of "+project+" (IMP-195): its roles, budget and approvals."), TeamType, project, name)
	b.WriteString("The team's roles take the tasks given to them (" + Folder + "/). Change the block to change the team; the format is in docs/agents.md.\n\n")
	b.WriteString("```team\n")
	if coordinator != "" {
		fmt.Fprintf(&b, "coordinator: %s\n", coordinator)
	}
	b.WriteString("budget: {day_usd: 10, task_usd: 2}\nmax_agents: 2\n")
	b.WriteString("roles:\n")
	for _, r := range roles {
		fmt.Fprintf(&b, "  %s:\n    agent: %q\n    run: %s\n", r, Link(AgentPath(project, r)), run)
		if r == coordinator {
			var others []string
			for _, o := range roles {
				if o != r {
					others = append(others, o)
				}
			}
			if len(others) > 0 {
				fmt.Fprintf(&b, "    delegates_to: [%s]\n", strings.Join(others, ", "))
			}
		}
	}
	b.WriteString("```\n")
	return []byte(b.String())
}

// AgentPath is the agent note of a role of a new team.
func AgentPath(project, role string) string { return project + "/agents/" + role + ".md" }

// AgentNote is the agent note of a role of a new team: its body is the
// agent's system prompt.
func AgentNote(project, role string, coordinates bool) []byte {
	text := "You are the agent " + role + " of a team of the project " + project + ". Do the task you are given, within its paths, and answer with a short report: what you did, where it is, what is left."
	if coordinates {
		text = "You coordinate a team of the project " + project + ". Split each objective into tasks for the roles you give tasks to, each with what it needs to know and how it is accepted; read their reports, and say when the objective is reached or a person must decide."
	}
	return []byte(fmt.Sprintf("---\ntitle: %s\ntype: agent\ntags: [%s, type:agent]\n---\n\n# %s\n\n%s\n", yamlString(role), project, role, text))
}

// WorkPath and CoordinatePath are the pipelines of a new team: the one
// that gives its roles' tasks to their agents, and its coordinator's.
func WorkPath(project, team string) string { return project + "/pipelines/" + team + "-work.md" }

func CoordinatePath(project, team string) string {
	return project + "/pipelines/" + team + "-coordinate.md"
}

// WorkPipeline gives the ready tasks of the roles of team (its note's
// path), but its coordinator's, to their agents: the agent works, the
// role's checks and preview run, the gate looks at the files it touched,
// a person approves when the role asks for it, and the pull request is
// merged, the task closed, or given back for another attempt.
func WorkPipeline(team string) []byte {
	return []byte(fmt.Sprintf(`---
title: %[2]s
description: "The tasks of the roles of %[1]s, but its coordinator's: the agent, the checks, the gate, the approval (IMP-195)."
type: pipeline
---

# %[2]s

The agent of the task's role works on it (task.run); the role's checks
and preview run on the branch it pushed (agent.next), the preview only
when the checks passed; the gate looks at what it did (failed checks, a
preview that was not published, go back; a node of the checks or of the
preview that failed goes on to the gate, which gives the work back and
closes the pull request); a person
approves when the role asks for it (approve: merge or ops), from the
team's alert; then the pull request is merged and the task closed, or the
task goes back for another attempt with what to fix.

`+"```pipeline"+`
trigger: {on: task, team: "%[1]s", coordinator: false}
nodes:
  - id: ctx
    use: task.context
  - id: agent
    use: task.run
    timeout: 15m
  - id: checks
    use: exec.run
    when: "{{ agent.next.checks }}"
    timeout: 16m
    with:
      repo: {name: "{{ ctx.role.repo }}", ref: "{{ agent.repo.branch }}"}
      script: "{{ ctx.checks_script }}"
      fail_on_error: false
    on_error: continue
  - id: publish
    use: preview.publish
    when: "{{ agent.next.preview }}"
    unless: "{{ checks.exit_code }}"
    on_error: continue
    with: {site: "{{ checks.files.0.path }}", ttl: 3d, title: "{{ agent.repo.branch }}"}
  - id: look
    use: preview.check
    when: "{{ publish.id }}"
    on_error: continue
    with:
      id: "{{ publish.id }}"
      steps: [{action: screenshot, name: page, full_page: true}]
  - id: gate
    use: task.gate
    after: [agent, checks, look]
    with: {agent: "{{ agent }}", checks: "{{ checks }}", preview: "{{ look }}"}
  - id: review
    use: task.update
    after: [gate]
    when: "{{ gate.ask }}"
    with: {status: review, message: "{{ gate.text }}"}
  - id: ask
    use: notify.ask
    after: [review]
    with:
      to: "{{ ctx.team.alert }}"
      approvers: "{{ ctx.team.approvers }}"
      title: "{{ gate.title }}"
      text: "{{ gate.text }}"
      link: "{{ gate.link }}"
      image: "{{ gate.image }}"
      choices: "{{ gate.choices }}"
      resume_url: "{{ run.resume_url }}"
      expires: 24h
  - id: answer
    use: wait.call
    after: [ask]
    with: {timeout: 24h}
  - id: verdict
    use: task.verdict
    after: [gate, answer]
    with: {gate: "{{ gate }}", answer: "{{ answer }}"}
  - id: merge
    use: task.pr
    after: [verdict]
    when: "{{ verdict.merge }}"
    with: {url: "{{ gate.pr_url }}", action: merge}
  - id: drop
    use: task.pr
    after: [verdict]
    when: "{{ verdict.close_pr }}"
    with: {url: "{{ gate.pr_url }}", action: close}
  - id: close
    use: task.complete
    after: [verdict, merge, drop]
    when: "{{ verdict.close }}"
    with: {status: "{{ verdict.status }}", report: "{{ verdict.report }}"}
  - id: again
    use: task.update
    after: [verdict, drop]
    when: "{{ verdict.redo }}"
    with: {status: submitted, message: "{{ verdict.feedback }}"}
`+"```"+`
`, Link(team), strings.TrimSuffix(path.Base(team), ".md")+"-work"))
}

// CoordinatePipeline gives the objectives of team's coordinator to its
// agent, when they arrive and when their sub-tasks closed: it answers with
// a plan, which task.plan writes as tasks, or closes the objective, or asks
// a person.
func CoordinatePipeline(team string) []byte {
	return []byte(fmt.Sprintf(`---
title: %[2]s
description: "The objectives of the coordinator of %[1]s: its plan written as tasks, then its verdict when they closed (IMP-195)."
type: pipeline
---

# %[2]s

The coordinator's agent answers with a plan (task.run with plan: true):
tasks for the roles it gives tasks to, which task.plan writes; or the
objective done; or a question for a person. When the sub-tasks closed it
runs again with their reports.

`+"```pipeline"+`
trigger: {on: task, team: "%[1]s", coordinator: true, events: [ready, children_done]}
nodes:
  - id: plan
    use: task.run
    timeout: 15m
    with: {plan: true}
  - id: apply
    use: task.plan
    with: {plan: "{{ plan.json }}"}
  - id: close
    use: task.complete
    after: [apply]
    when: "{{ apply.close }}"
    with: {report: "{{ apply.summary }}"}
  - id: ask
    use: task.update
    after: [apply]
    when: "{{ apply.ask }}"
    with: {status: input-required, message: "{{ apply.question }}"}
`+"```"+`
`, Link(team), strings.TrimSuffix(path.Base(team), ".md")+"-coordinate"))
}

// ViewNote is the note that follows a team: what waits for a person, what
// is at work, what is done, and the cost of each task.
func ViewNote(project, team string) []byte {
	src := project + "/" + Folder
	link := Link(team)
	return []byte(fmt.Sprintf(`---
title: %[3]s
description: "The tasks of %[2]s at a glance (IMP-195)."
tags: [%[4]s, type:index]
---

# %[3]s

## Waits for you

`+"```view"+`
from: %[1]s
where:
  - team = %[2]s
  - status in [input-required, review]
sort: path
columns: [title, status, assignee]
`+"```"+`

## At work

`+"```view"+`
from: %[1]s
where:
  - team = %[2]s
  - status in [submitted, working]
sort: path
columns: [title, status, assignee, attempts, cost_usd]
`+"```"+`

## Board

`+"```view"+`
from: %[1]s
where:
  - team = %[2]s
as: board
group_by: status
columns: [title, assignee, cost_usd]
`+"```"+`

## Done

`+"```view"+`
from: %[1]s
where:
  - team = %[2]s
  - status in [completed, failed, canceled]
sort: path desc
limit: 50
columns: [title, status, assignee, cost_usd]
`+"```"+`
`, src, link, "Team "+strings.TrimSuffix(team[strings.LastIndex(team, "/")+1:], ".md"), project))
}
