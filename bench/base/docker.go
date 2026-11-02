package main

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The server runs in a throwaway container with fixed limits, the same for
// every version, so that the numbers compare: a small server, not the host.
const (
	serverCPUs   = "2"
	serverMemory = "2g"
	helperImage  = "alpine:3.20"
	imageUser    = "65532:65532" // the user of gosidian's image since v1
)

// docker runs the docker CLI and returns its trimmed output.
func docker(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("docker %s: %w: %.300s", strings.Join(args[:min(2, len(args))], " "), err, s)
	}
	return s, nil
}

// volume is a docker volume the bench owns and removes.
type volume string

// newVolume creates an empty volume the image's user can write; with src, a
// copy of that directory. The copy goes through a helper container, so the
// files belong to the image's user and the host keeps no trace of them.
// The files are dated at the copy, not as the checkout has them: the
// bootstrap reads their age (hot.md's, the stale notes), and a checkout a
// month old made it 112 bytes longer than a fresh one (2026-10-29).
func newVolume(name, src string) (volume, error) {
	if _, err := docker("volume", "create", name); err != nil {
		return "", err
	}
	script := "chown " + imageUser + " /v"
	args := []string{"run", "--rm", "--cpus=1", "-v", name + ":/v"}
	if src != "" {
		args = append(args, "-v", src+":/src:ro")
		script = "cp -R /src/. /v/ && chown -R " + imageUser + " /v"
	}
	if _, err := docker(append(args, helperImage, "sh", "-c", script)...); err != nil {
		removeVolume(volume(name))
		return "", err
	}
	return volume(name), nil
}

func removeVolume(v volume) {
	if v != "" {
		docker("volume", "rm", "-f", string(v))
	}
}

// server is one gosidian container on a loopback port.
type server struct {
	name  string
	image string
	port  int
	vault volume
	state volume
}

// freePort asks the kernel for a free loopback port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// create makes the container without starting it, so that the start can be
// timed on its own.
func (s *server) create(env []string) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	s.port = port
	args := []string{"create", "--name", s.name, "--cpus=" + serverCPUs, "--memory=" + serverMemory,
		"-p", fmt.Sprintf("127.0.0.1:%d:8080", port),
		"-e", "GOSIDIAN_STATE_DIR=/data", "-v", string(s.vault) + ":/vault", "-v", string(s.state) + ":/data"}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	_, err = docker(append(args, s.image)...)
	return err
}

// start starts the container and returns the time until /healthz answers
// 200: the listener is up and the boot scan of the vault is over (older
// versions open the port after the scan, newer ones answer 503 during it).
func (s *server) start() (time.Duration, error) {
	t0 := time.Now()
	if _, err := docker("start", s.name); err != nil {
		return 0, err
	}
	return s.waitReady(t0, 10*time.Minute)
}

func (s *server) waitReady(t0 time.Time, limit time.Duration) (time.Duration, error) {
	c := &http.Client{Timeout: 2 * time.Second}
	for time.Since(t0) < limit {
		if resp, err := c.Get(s.url() + "/healthz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return time.Since(t0), nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs, _ := docker("logs", "--tail", "20", s.name)
	return 0, fmt.Errorf("%s not ready after %s: %.500s", s.name, limit, logs)
}

func (s *server) stop() error {
	_, err := docker("stop", "-t", "30", s.name)
	return err
}

func (s *server) remove() {
	docker("rm", "-f", s.name)
}

func (s *server) url() string { return fmt.Sprintf("http://127.0.0.1:%d", s.port) }

// exec runs a shell command in the container (the image has busybox).
func (s *server) exec(script string) (string, error) {
	return docker("exec", s.name, "sh", "-c", script)
}

// token creates an MCP token with the image's own CLI and returns it. The
// CLI prints it twice (the token and the client command); the first is it.
func (s *server) token(scopes, profile string) (string, error) {
	args := []string{"exec", s.name, "/gosidian", "token", "create", "--vault", "/vault", "--state-dir", "/data",
		"--name", "bench-" + strings.ReplaceAll(scopes, ",", "-") + "-" + profile, "--scopes", scopes}
	if profile != "" && profile != "full" {
		args = append(args, "--tool-profile", profile)
	}
	out, err := docker(args...)
	if err != nil {
		return "", fmt.Errorf("token create failed")
	}
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "gosidian_") {
			return strings.Trim(f, `"'`), nil
		}
	}
	return "", fmt.Errorf("token create printed no token")
}

// cgroup reads the container's own cgroup v2 counters.
type cgroup struct {
	Anon    int64 // anonymous memory: the heap and stacks of the process
	File    int64 // page cache charged to the container: the vault and index it read
	Current int64 // memory.current: both, and the kernel's
	Peak    int64 // memory.peak since the container started
	CPUUsec int64 // cpu.stat usage_usec since the container started
}

func (s *server) cgroup() (cgroup, error) {
	out, err := s.exec("cat /sys/fs/cgroup/memory.stat; echo current $(cat /sys/fs/cgroup/memory.current); " +
		"echo peak $(cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo 0); grep usage_usec /sys/fs/cgroup/cpu.stat")
	if err != nil {
		return cgroup{}, err
	}
	var c cgroup
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		n, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "anon":
			c.Anon = n
		case "file":
			c.File = n
		case "current":
			c.Current = n
		case "peak":
			c.Peak = n
		case "usage_usec":
			c.CPUUsec = n
		}
	}
	return c, nil
}

// sizeOf returns the bytes of a path in the container: a file, or a
// directory's files together.
func (s *server) sizeOf(path string) (int64, error) {
	out, err := s.exec("du -sk " + path + " | cut -f1")
	if err != nil {
		return 0, err
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return kb * 1024, err
}

func (s *server) fileSize(path string) (int64, error) {
	out, err := s.exec("stat -c %s " + path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

func imageSize(image string) (int64, error) {
	out, err := docker("image", "inspect", "--format", "{{.Size}}", image)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(out, 10, 64)
}
