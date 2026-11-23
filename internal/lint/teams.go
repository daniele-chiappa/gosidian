package lint

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/parser"
)

// checkTeams reports the ```team blocks (IMP-195) that do not read as a
// team, on the line of their problem, the notes type: team without exactly
// one block, a role whose agent is no note of the vault, and a guide or a
// skill of a role that is no note of the project, or a skill that is no
// note type: skill (IMP-233); a profile note that does not read, and a
// role whose profile does not.
func checkTeams(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	const hint = "the format: docs/agents.md; the problem's line is in the note"
	paths := map[string]bool{}
	skills := map[string]bool{}
	content := map[string][]byte{}
	for _, n := range notes {
		paths[n.Path] = true
		content[n.Path] = n.Content
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), fleet.SkillType) {
			skills[n.Path] = true
		}
	}
	profiles := fleet.ProfilesFrom(func(p string) ([]byte, error) {
		c, ok := content[p]
		if !ok {
			return nil, fmt.Errorf("no note %s", p)
		}
		return c, nil
	})
	// isNote tells a note of the project, or of another project by the index.
	isNote := func(p string) (bool, error) {
		if paths[p] {
			return true, nil
		}
		row, err := l.index.Note(p)
		return row != nil, err
	}
	var issues []Issue
	// The teams that read, by path, and where each block starts: their
	// rooms are checked once all are read (IMP-233).
	type read struct {
		team fleet.Team
		line int
	}
	readTeams := map[string]read{}
	for _, n := range notes {
		if !strings.HasSuffix(strings.ToLower(n.Path), ".md") {
			continue
		}
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), fleet.ProfileType) {
			issues = append(issues, profileIssues(n, hint)...)
			continue
		}
		blocks := fleet.Find(n.Content)
		if noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), fleet.TeamType) && len(blocks) != 1 {
			issues = append(issues, Issue{
				Severity: SeverityWarning, File: n.Path, Rule: "team-invalid",
				Message: fmt.Sprintf("a note type: team holds one ```team block, this one %d", len(blocks)),
				FixHint: hint,
			})
		}
		for _, b := range blocks {
			team, problems := fleet.ParseWith(b.Src, n.Path, profiles)
			if len(problems) == 0 && len(blocks) == 1 {
				readTeams[n.Path] = read{team, b.Line}
			}
			for _, p := range problems {
				line := b.Line
				if p.Line > 0 {
					line += p.Line
				}
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: line, Rule: "team-invalid",
					Message: fmt.Sprintf("the team at line %d: %s", b.Line, p.Msg),
					FixHint: hint,
				})
			}
			if len(problems) > 0 {
				continue
			}
			// noteProblem says what is wrong with a guide or a skill.
			noteProblem := func(p string, skill bool) string {
				switch {
				case p == "":
				case !paths[p] && skill:
					return "skill " + p + " is no note of the project"
				case !paths[p]:
					return "guide " + p + " is no note of the project"
				case skill && !skills[p]:
					return "skill " + p + " is no note type: skill"
				}
				return ""
			}
			teamIssue := func(who, msg string) {
				issues = append(issues, Issue{
					Severity: SeverityWarning, File: n.Path, Line: b.Line, Rule: "team-invalid",
					Message: fmt.Sprintf("the team at line %d: %s%s", b.Line, who, msg),
					FixHint: hint,
				})
			}
			if msg := noteProblem(team.Guide, false); msg != "" {
				teamIssue("", msg)
			}
			for _, r := range team.Roles {
				if msg := noteProblem(r.Guide, false); msg != "" {
					teamIssue("role "+r.Name+": ", msg)
				}
				for _, sk := range r.Skills {
					if msg := noteProblem(sk, true); msg != "" {
						teamIssue("role "+r.Name+": ", msg)
					}
				}
				if r.Agent == "" {
					continue
				}
				ok, err := isNote(r.Agent)
				if err != nil {
					return nil, err
				}
				if !ok {
					issues = append(issues, Issue{
						Severity: SeverityWarning, File: n.Path, Line: b.Line, Rule: "team-invalid",
						Message: fmt.Sprintf("the team at line %d: role %s: agent %s is no note of the vault", b.Line, r.Name, r.Agent),
						FixHint: hint,
					})
				}
			}
		}
	}
	// A room is a team that reads, with a coordinator to receive the
	// objectives and no rooms of its own, the room of one team only.
	parents := map[string]string{}
	for _, p := range slices.Sorted(maps.Keys(readTeams)) {
		t := readTeams[p]
		for _, r := range t.team.Rooms {
			var msg string
			rt, ok := readTeams[r.Path]
			switch {
			case !paths[r.Path]:
				msg = "is no note of the project"
			case !ok:
				msg = "is no team that reads (its own issues say why)"
			case rt.team.Coordinator == "":
				msg = "has no coordinator to receive the objectives"
			case len(rt.team.Rooms) > 0:
				msg = "has rooms of its own: a room has none"
			case parents[r.Path] != "":
				msg = "is a room of " + parents[r.Path] + " too: a room is one team's"
			}
			if parents[r.Path] == "" {
				parents[r.Path] = p
			}
			if msg != "" {
				issues = append(issues, Issue{Severity: SeverityWarning, File: p, Line: t.line, Rule: "team-invalid",
					Message: fmt.Sprintf("the team at line %d: room %s: %s %s", t.line, r.Name, r.Path, msg), FixHint: hint})
			}
		}
	}
	return issues, nil
}

// profileIssues reports a profile note (IMP-233) without exactly one
// ```profile block, or whose block does not read as a role's fields.
func profileIssues(n projectNote, hint string) []Issue {
	blocks := fleet.FindProfiles(n.Content)
	if len(blocks) != 1 {
		return []Issue{{Severity: SeverityWarning, File: n.Path, Rule: "team-invalid",
			Message: fmt.Sprintf("a note type: profile holds one ```profile block, this one %d", len(blocks)), FixHint: hint}}
	}
	var out []Issue
	for _, p := range fleet.CheckProfile(blocks[0].Src, n.Path) {
		out = append(out, Issue{Severity: SeverityWarning, File: n.Path, Line: blocks[0].Line, Rule: "team-invalid",
			Message: fmt.Sprintf("the profile at line %d: %s", blocks[0].Line, p.Msg), FixHint: hint})
	}
	return out
}
