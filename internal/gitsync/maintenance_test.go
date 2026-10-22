package gitsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
)

func TestParseMaintenance(t *testing.T) {
	for in, want := range map[string]maintenance{
		"":      {hour: 3, minute: 30},
		"03:30": {hour: 3, minute: 30},
		"23:05": {hour: 23, minute: 5},
		"auto":  {auto: true},
		"off":   {off: true},
	} {
		got, err := parseMaintenance(in)
		if err != nil || got != want {
			t.Errorf("parseMaintenance(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"3", "25:00", "03:60", "nightly", "3:30pm"} {
		if _, err := parseMaintenance(bad); err == nil {
			t.Errorf("parseMaintenance(%q): no error", bad)
		}
	}
}

func TestMaintenance_Next(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip(err)
	}
	m := maintenance{hour: 3, minute: 30}
	for _, c := range []struct{ now, want string }{
		{"2026-10-21T23:00:00", "2026-10-22T03:30:00"},
		{"2026-10-22T03:00:00", "2026-10-22T03:30:00"},
		{"2026-10-22T03:30:00", "2026-10-23T03:30:00"},
		// The night the clocks go back: 03:30 comes once, an hour later in UTC.
		{"2026-10-24T04:00:00", "2026-10-25T03:30:00"},
	} {
		now, _ := time.ParseInLocation("2006-01-02T15:04:05", c.now, rome)
		want, _ := time.ParseInLocation("2006-01-02T15:04:05", c.want, rome)
		if got := m.next(now); !got.Equal(want) {
			t.Errorf("next(%s) = %s, want %s", c.now, got, want)
		}
	}
}

// Every allowed attachment format is either left without deltas or known to
// be text: a new format has to take a side.
func TestBinaryExts_CoverTheAttachmentFormats(t *testing.T) {
	text := map[string]bool{"svg": true, "csv": true, "json": true, "txt": true}
	binary := map[string]bool{}
	for _, e := range binaryExts {
		binary[e] = true
	}
	for ext := range attach.AllowedExt {
		e := strings.TrimPrefix(ext, ".")
		if binary[e] == text[e] {
			t.Errorf("attachment format %s: in binaryExts %v, text %v; it must be one of the two", e, binary[e], text[e])
		}
	}
}

func checkAttr(t *testing.T, dir, path string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "check-attr", "delta", "--", path).Output()
	if err != nil {
		t.Fatalf("check-attr %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

func gitConfig(t *testing.T, dir, key string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "config", "--local", "--get", key).Output()
	return strings.TrimSpace(string(out))
}

// Start writes the managed block of info/attributes, -delta on the images
// in any case and not on the notes, keeps the operator's lines, and turns
// git's own maintenance off for the nightly one (IMP-209).
func TestSync_AttributesAndMaintenance(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "--initial-branch=main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	attrs := filepath.Join(dir, ".git", "info", "attributes")
	if err := os.WriteFile(attrs, []byte("*.mp4 -delta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(dir, testCfg())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	for path, want := range map[string]string{
		"p/attachments/a.png": "unset",
		"p/IN/Scan.PDF":       "unset",
		"p/a.md":              "unspecified",
		"p/a.svg":             "unspecified",
		"p/v.mp4":             "unset",
	} {
		if got := checkAttr(t, dir, path); got != path+": delta: "+want {
			t.Errorf("check-attr: %q, want delta %s", got, want)
		}
	}
	first, _ := os.ReadFile(attrs)
	if err := s.refreshAttributes(); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(attrs); string(again) != string(first) {
		t.Errorf("a second refresh changed info/attributes:\n%s\nthen\n%s", first, again)
	}
	if strings.Count(string(first), gitignoreManagedBegin) != 1 {
		t.Errorf("info/attributes has %d managed blocks:\n%s", strings.Count(string(first), gitignoreManagedBegin), first)
	}
	if got := gitConfig(t, dir, "gc.auto"); got != "0" {
		t.Errorf("gc.auto = %q, want 0", got)
	}
	if got := gitConfig(t, dir, "maintenance.auto"); got != "false" {
		t.Errorf("maintenance.auto = %q, want false", got)
	}
	// auto gives the maintenance back to git, and leaves a value of the
	// operator's own.
	if out, err := exec.Command("git", "-C", dir, "config", "--local", "gc.auto", "100").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if err := s.ensureMaintenance(maintenance{auto: true}); err != nil {
		t.Fatal(err)
	}
	if got := gitConfig(t, dir, "gc.auto"); got != "100" {
		t.Errorf("auto: gc.auto = %q, want the operator's 100", got)
	}
	if got := gitConfig(t, dir, "maintenance.auto"); got != "" {
		t.Errorf("auto: maintenance.auto = %q, want unset", got)
	}
}

// A [git] maintenance that is no time stops git sync at start, as a bad
// branch does.
func TestSync_BadMaintenanceFailsStart(t *testing.T) {
	requireGit(t)
	cfg := testCfg()
	cfg.Maintenance = "nightly"
	s := New(t.TempDir(), cfg)
	if err := s.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "nightly") {
		t.Fatalf("start: %v, want the maintenance error", err)
	}
	if s.Status().Healthy {
		t.Error("status healthy after a bad maintenance value")
	}
}

// The maintenance packs the loose objects the commits left, and waits for a
// commit that is running.
func TestSync_RunMaintenance(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(dir, testCfg())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, "n"+string(rune('a'+i))+".md"), []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		s.TriggerCommit()
		s.Flush()
	}
	loose := func() string {
		out, _ := exec.Command("git", "-C", dir, "count-objects").Output()
		return strings.TrimSpace(string(out))
	}
	if strings.HasPrefix(loose(), "0 objects") {
		t.Fatalf("no loose objects before the maintenance: %s", loose())
	}

	s.workMu.Lock() // a commit running
	done := make(chan struct{})
	go func() { s.runMaintenance(); close(done) }()
	select {
	case <-done:
		t.Fatal("the maintenance ran during a commit")
	case <-time.After(100 * time.Millisecond):
	}
	s.workMu.Unlock()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the maintenance did not end")
	}
	if got := loose(); !strings.HasPrefix(got, "0 objects") {
		t.Errorf("after the maintenance: %s, want no loose objects", got)
	}
}
