package projects

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTeams_CRUDAndGrants(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	devs, err := s.CreateTeam("  devs ", "backend crew")
	if err != nil || devs.ID == "" || devs.Name != "devs" || devs.Description != "backend crew" {
		t.Fatalf("create = %+v, %v", devs, err)
	}
	if _, err := s.CreateTeam("DEVS", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate name (case-insensitive) must be rejected, got %v", err)
	}
	if _, err := s.CreateTeam("", ""); err == nil {
		t.Error("empty name must be rejected")
	}
	if _, err := s.CreateTeam("a/b", ""); err == nil {
		t.Error("slash in name must be rejected")
	}
	ops, _ := s.CreateTeam("ops", "")
	if got := s.Teams(); len(got) != 2 || got[0].Name != "devs" || got[1].Name != "ops" {
		t.Fatalf("Teams() = %+v", got)
	}

	// Users and grants.
	if err := s.AddTeamUser(devs.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	_ = s.AddTeamUser(devs.ID, "u1") // idempotent
	_ = s.AddTeamUser(devs.ID, "u2")
	if err := s.SetTeamGrant(devs.ID, "alpha", LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTeamGrant(devs.ID, "beta", "root"); err == nil {
		t.Error("invalid level must be rejected")
	}
	_ = s.SetTeamGrant(ops.ID, "alpha", LevelRead)
	got, _ := s.Team(devs.ID)
	if len(got.Users) != 2 || got.Grants["alpha"] != LevelWrite {
		t.Errorf("team = %+v", got)
	}
	if n := s.TeamsCount("alpha"); n != 2 {
		t.Errorf("TeamsCount(alpha) = %d want 2", n)
	}
	if tg := s.TeamGrantsOn("alpha"); len(tg) != 2 || tg[0].TeamName != "devs" || tg[1].Level != LevelRead {
		t.Errorf("TeamGrantsOn = %+v", tg)
	}
	if lv := s.TeamLevelsFor("u1", "alpha"); len(lv) != 1 || lv[0].Level != LevelWrite || lv[0].TeamName != "devs" {
		t.Errorf("TeamLevelsFor(u1) = %+v", lv)
	}
	if lv := s.TeamLevelsFor("u9", "alpha"); len(lv) != 0 {
		t.Errorf("stranger must inherit nothing: %+v", lv)
	}
	if mine := s.TeamsOf("u2"); len(mine) != 1 || mine[0].ID != devs.ID {
		t.Errorf("TeamsOf(u2) = %+v", mine)
	}

	// Mutating a returned copy must not touch the store.
	got.Users = append(got.Users, "u3")
	got.Grants["gamma"] = LevelAdmin
	if again, _ := s.Team(devs.ID); len(again.Users) != 2 || again.Grants["gamma"] != "" {
		t.Error("Team() must return a deep copy")
	}

	// Rename + description; persistence across reopen.
	name, desc := "developers", "renamed"
	if _, err := s.UpdateTeam(devs.ID, &name, &desc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTeam(ops.ID, &name, nil); err == nil {
		t.Error("renaming onto an existing name must be rejected")
	}
	s2, _ := Open(path)
	if again, ok := s2.Team(devs.ID); !ok || again.Name != "developers" || again.Description != "renamed" || again.Grants["alpha"] != LevelWrite {
		t.Errorf("persisted team = %+v (%v)", again, ok)
	}

	// Remove user / grant / team.
	if err := s.RemoveTeamUser(devs.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	if lv := s.TeamLevelsFor("u1", "alpha"); len(lv) != 0 {
		t.Error("removed user must inherit nothing")
	}
	if err := s.RemoveTeamGrant(devs.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	if n := s.TeamsCount("alpha"); n != 1 {
		t.Errorf("after grant removal TeamsCount = %d want 1", n)
	}
	if err := s.DeleteTeam(ops.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Team(ops.ID); ok || s.TeamsCount("alpha") != 0 {
		t.Error("deleted team must vanish with its grants")
	}
	if err := s.DeleteTeam("nope"); err == nil {
		t.Error("deleting an unknown team must fail")
	}
}

// Project rename/delete and user disable keep team grants and rosters in sync.
func TestTeams_Cascades(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	team, _ := s.CreateTeam("devs", "")
	_ = s.AddTeamUser(team.ID, "u1")
	_ = s.AddTeamUser(team.ID, "u2")
	_ = s.SetTeamGrant(team.ID, "old", LevelWrite)
	_ = s.SetTeamGrant(team.ID, "gone", LevelRead)

	if err := s.Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Team(team.ID); got.Grants["old"] != "" || got.Grants["new"] != LevelWrite {
		t.Errorf("rename must move team grants: %+v", got.Grants)
	}
	if err := s.Delete("gone"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Team(team.ID); got.Grants["gone"] != "" {
		t.Errorf("delete must drop team grants: %+v", got.Grants)
	}
	if err := s.RemoveUserEverywhere("u1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Team(team.ID); len(got.Users) != 1 || got.Users[0] != "u2" {
		t.Errorf("disable must strip the user from teams: %+v", got.Users)
	}
}

// The access config reports team grants as their own source, highest wins.
func TestAccessConfig_TeamGrants(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	team, _ := s.CreateTeam("devs", "")
	_ = s.AddTeamUser(team.ID, "u1")
	_ = s.SetTeamGrant(team.ID, "p", LevelWrite)
	_ = s.SetMember("p", "u1", LevelRead)
	cfg := s.AccessConfig()
	src := cfg.Grants("u1", "p")
	if len(src) != 2 || src[0].Via != "grant:read" || src[1].Via != "team:devs:write" {
		t.Fatalf("sources = %+v", src)
	}
	if cfg.Grants("u9", "p") != nil {
		t.Error("stranger must have no sources")
	}
}
