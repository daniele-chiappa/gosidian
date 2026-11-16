package pipeline

import (
	"slices"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fleet"
)

// widerTeam is flowTeam with a coordinator that gives tasks to the roles
// on the repository and to the reviewer too.
var widerTeam = strings.Replace(flowTeam, "delegates_to: [dev, doc]", "delegates_to: [dev, doc, web, rev]", 1)

func planOf(tasks ...map[string]any) map[string]any {
	var l []any
	for _, t := range tasks {
		l = append(l, t)
	}
	return map[string]any{"json": map[string]any{"action": "tasks", "tasks": l}}
}

func kidsOf(t *testing.T, host *fakeHost, parent string) map[string]fleet.Task {
	t.Helper()
	tasks, err := host.Tasks("p")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]fleet.Task{}
	for _, x := range tasks {
		if x.Parent == parent {
			out[x.Title] = x
		}
	}
	return out
}

// A task in the place of a failed one takes the tasks that waited for it;
// a task given again while it is open is not written twice; two tasks on
// the same repository work one after the other.
func TestTaskPlan_ReplacesRepeatsAndTheSameFiles(t *testing.T) {
	e, host, c := flowEngine(t)
	host.notes["p/squadra.md"] = widerTeam
	kid := func(title, role, status, extra string) string {
		return taskNote(title, role, status, "parent: \"[[p/tasks/o]]\"\n"+extra)
	}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "")
	host.notes["p/tasks/k1.md"] = kid("i dati", "web", "failed", "")
	host.notes["p/tasks/k2.md"] = kid("la revisione", "rev", "submitted", "depends_on:\n  - \"[[p/tasks/k1]]\"\n")
	c.answers = []map[string]any{
		planOf(
			map[string]any{"key": "dati", "assignee": "web", "title": "I dati, di nuovo", "body": "Rifai i dati.", "replaces": "p/tasks/k1.md"},
			map[string]any{"key": "sito", "assignee": "web", "title": "Il sito", "body": "Fai il sito."},
			map[string]any{"key": "rev", "assignee": "rev", "title": "La  Revisione", "body": "Rivedi."},
			map[string]any{"key": "altro", "assignee": "web", "title": "Altro", "body": "x", "replaces": "p/tasks/k2.md"},
		),
		{"text": "never"},
	}
	// The developer's tasks need a person here: the plan is what counts.
	// It writes its tasks first, then points those that waited for the
	// failed one to the task in its place: the wait is for that last write.
	eventually(t, func() bool {
		e.TickTasks()
		kids := kidsOf(t, host, "p/tasks/o.md")
		dati := kids["I dati, di nuovo"].Path
		return len(kids) == 5 && dati != "" && slices.Contains(kids["la revisione"].DependsOn, dati)
	}, "the plan's tasks are written and the review waits for the one in the place of the failed task")
	kids := kidsOf(t, host, "p/tasks/o.md")
	dati, sito, altro, rev := kids["I dati, di nuovo"], kids["Il sito"], kids["Altro"], kids["la revisione"]
	if _, twice := kids["La  Revisione"]; twice {
		t.Fatalf("the open review was written again: %v", kids)
	}
	if len(rev.DependsOn) != 1 || rev.DependsOn[0] != dati.Path {
		t.Fatalf("the review waits for %v, not for the task in the place of the failed one (%s)", rev.DependsOn, dati.Path)
	}
	if len(sito.DependsOn) != 1 || sito.DependsOn[0] != dati.Path {
		t.Fatalf("the site, on the same repository, waits for %v", sito.DependsOn)
	}
	if len(altro.DependsOn) != 1 || altro.DependsOn[0] != sito.Path {
		t.Fatalf("the third task on the repository waits for %v (the one before it waits for the first)", altro.DependsOn)
	}
}

// Tasks of the same repository with paths that do not meet work side by
// side.
func TestTaskPlan_PathsApartNeedNoOrder(t *testing.T) {
	e, host, c := flowEngine(t)
	host.notes["p/squadra.md"] = widerTeam
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "submitted", "")
	c.answers = []map[string]any{
		planOf(
			map[string]any{"key": "a", "assignee": "web", "title": "Le pagine", "body": "x", "paths": []any{"web/"}},
			map[string]any{"key": "b", "assignee": "web", "title": "I dati", "body": "x", "paths": []any{"data/"}},
			map[string]any{"key": "c", "assignee": "web", "title": "Una pagina", "body": "x", "paths": []any{"web/index.html"}},
		),
		{"text": "never"},
	}
	eventually(t, func() bool { e.TickTasks(); return len(kidsOf(t, host, "p/tasks/o.md")) == 3 }, "the plan's tasks are written")
	kids := kidsOf(t, host, "p/tasks/o.md")
	if len(kids["I dati"].DependsOn) != 0 || len(kids["Le pagine"].DependsOn) != 0 {
		t.Fatalf("paths apart were put in order: %v %v", kids["Le pagine"].DependsOn, kids["I dati"].DependsOn)
	}
	if d := kids["Una pagina"].DependsOn; len(d) != 1 || d[0] != kids["Le pagine"].Path {
		t.Fatalf("a file under web/ waits for %v", d)
	}
}
