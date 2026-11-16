package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Git sync is measured on a server of its own, pushing to a bare
// repository in a volume: no network, the same for every version. The
// debounce is the shortest wait that still tells a round from the write
// before it, and is taken off the times.
const (
	syncDebounce = 100 * time.Millisecond
	syncRemote   = "/remote/vault.git"
	nSyncRounds  = 10
	syncSettle   = 300 * time.Millisecond // between rounds: a flush still on its way is not the next round's
	syncTimeout  = 60 * time.Second
	syncMetric   = `gosidian_git_sync_commits_total{outcome="success"}`
)

// oneSync measures git sync's rounds: the first, which commits and pushes
// the whole vault, and then nSyncRounds of one note each. A round runs
// from the write's reply to the server counting a commit, less the
// debounce.
func (b *bench) oneSync(i int) error {
	tag := fmt.Sprintf("gbb-%d-%d-sync", os.Getpid(), i)
	vault, err := newVolume(tag+"-vault", b.vaultDir)
	if err != nil {
		return err
	}
	defer removeVolume(vault)
	state, err := newVolume(tag+"-state", "")
	if err != nil {
		return err
	}
	defer removeVolume(state)
	remote, err := newVolume(tag+"-remote", "")
	if err != nil {
		return err
	}
	defer removeVolume(remote)
	// The image's own git makes the remote, as its user.
	if _, err := docker("run", "--rm", "--cpus=1", "--user", imageUser, "--entrypoint", "git",
		"-v", string(remote)+":/remote", b.image, "init", "-q", "--bare", syncRemote); err != nil {
		return err
	}
	s := &server{name: tag, image: b.image, vault: vault, state: state, mounts: []string{string(remote) + ":/remote"}}
	env := append(append([]string(nil), writesEnv...),
		"GOSIDIAN_GIT_ENABLED=true", "GOSIDIAN_GIT_REMOTE="+syncRemote, "GOSIDIAN_GIT_PUSH=true",
		"GOSIDIAN_GIT_DEBOUNCE="+syncDebounce.String())
	if err := s.create(append(env, b.env...)); err != nil {
		return err
	}
	defer s.remove()
	if _, err := s.start(); err != nil {
		return err
	}
	tok, err := s.token("read,write", "full")
	if err != nil {
		return err
	}
	c, err := newMCPClient(s.url(), tok)
	if err != nil {
		return err
	}
	round := func(k int) (time.Duration, error) {
		before, err := syncCommits(s.url())
		if err != nil {
			return 0, err
		}
		path := fmt.Sprintf("%s/bench-sync/run%d-note%02d.md", benchProj, i, k)
		if _, _, err := c.call("memory_create", map[string]any{"path": path, "content": fmt.Sprintf("---\ntitle: Sync note %d\n---\n\n# Sync note %d\n", k, k)}); err != nil {
			return 0, err
		}
		t0 := time.Now()
		for time.Since(t0) < syncTimeout {
			if n, err := syncCommits(s.url()); err == nil && n > before {
				return max(time.Since(t0)-syncDebounce, 0), nil
			}
			time.Sleep(2 * time.Millisecond)
		}
		return 0, fmt.Errorf("no commit %s after a write", syncTimeout)
	}

	first, err := round(0)
	if err != nil {
		return err
	}
	rounds := make([]float64, 0, nSyncRounds)
	for k := 1; k <= nSyncRounds; k++ {
		time.Sleep(syncSettle)
		d, err := round(k)
		if err != nil {
			return err
		}
		rounds = append(rounds, ms(d))
	}
	time.Sleep(syncSettle)
	// The counter also counts a flush that found nothing to commit: the
	// rounds are rounds only if every note of theirs is in the remote. (A
	// round may leave more than one commit: one per writer, one for the
	// rest.)
	if got := s.pushed(benchProj + "/bench-sync/"); got != 1+nSyncRounds {
		return fmt.Errorf("%d rounds, %d of their notes in the remote", 1+nSyncRounds, got)
	}
	if b.res.Sync == nil {
		b.res.Sync = &syncTimes{DebounceMS: ms(syncDebounce), Rounds: nSyncRounds}
	}
	b.res.Sync.FirstMS = append(b.res.Sync.FirstMS, ms(first))
	b.res.Sync.RoundMS = append(b.res.Sync.RoundMS, median(rounds))
	b.log("  git sync: first round %.0f ms, a round %.0f ms", ms(first), median(rounds))
	return nil
}

// pushed counts the files under dir that the remote's commits hold.
func (s *server) pushed(dir string) int {
	out, err := s.exec("git --git-dir " + syncRemote + " log --all --name-only --format= 2>/dev/null | grep -F '" + dir + "' | sort -u | wc -l")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// syncCommits reads the server's count of git sync's rounds that went
// well from /metrics; 0 before the first.
func syncCommits(base string) (float64, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(base + "/metrics")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("/metrics: %s", resp.Status)
	}
	return parseSyncCommits(resp.Body), nil
}

func parseSyncCommits(r io.Reader) float64 {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), syncMetric+" "); ok {
			n, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
			return n
		}
	}
	return 0
}
