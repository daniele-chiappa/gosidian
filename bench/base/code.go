package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// branchPackages are the packages that belong to the branches of the
// roadmap (pipelines, plugins, the IN folder, chat bots, agent teams), not
// to the base. Parts of the branches also live in internal/mcp and
// internal/api, which count as base here: the split is by package only.
var branchPackages = map[string]bool{
	"internal/pipeline": true,
	"internal/infolder": true,
	"internal/plugins":  true,
	"internal/chatbot":  true,
	"internal/fleet":    true,
}

// codeAt counts the code of the repository at ref, from git, without a
// checkout: Go lines by package, the web UI's sources, the dependencies.
func codeAt(repo, ref string) (*codeSize, error) {
	cmd := exec.Command("git", "-C", repo, "archive", "--format=tar", ref, "--", "internal", "cmd", "web/src", "go.mod", "web/package.json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git archive %s: %w: %.200s", ref, err, stderr.String())
	}
	cs := &codeSize{Ref: ref, Packages: map[string]int{}}
	tr := tar.NewReader(bytes.NewReader(out))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		cs.add(h.Name, body)
	}
	return cs, nil
}

func (cs *codeSize) add(name string, body []byte) {
	lines := bytes.Count(body, []byte("\n"))
	switch {
	case name == "go.mod":
		cs.GoDirect, cs.GoIndirect = goModDeps(body)
	case name == "web/package.json":
		var p struct {
			Deps    map[string]string `json:"dependencies"`
			DevDeps map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(body, &p) == nil {
			cs.NPM, cs.NPMDev = len(p.Deps), len(p.DevDeps)
		}
	case strings.HasPrefix(name, "web/src/"):
		if isWebSource(name) {
			cs.WebLines += lines
		}
	case strings.HasSuffix(name, ".go"):
		if strings.HasSuffix(name, "_test.go") {
			cs.GoTestLines += lines
			return
		}
		cs.GoLines += lines
		pkg := packageOf(name)
		cs.Packages[pkg] += lines
		if branchPackages[pkg] {
			cs.BranchLines += lines
		} else {
			cs.BaseLines += lines
		}
	}
}

// packageOf is internal/<name> or cmd/<name> for a Go file.
func packageOf(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) >= 3 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func isWebSource(name string) bool {
	if strings.Contains(name, ".spec.") || strings.Contains(name, ".test.") {
		return false
	}
	for _, ext := range []string{".ts", ".vue", ".js", ".css"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// goModDeps counts the require lines of a go.mod: direct, and // indirect.
func goModDeps(mod []byte) (direct, indirect int) {
	sc := bufio.NewScanner(bytes.NewReader(mod))
	inBlock := false
	count := func(line string) {
		if strings.Contains(line, "// indirect") {
			indirect++
		} else {
			direct++
		}
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "require (":
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "" && !strings.HasPrefix(line, "//"):
			count(line)
		case strings.HasPrefix(line, "require ") && !strings.HasSuffix(line, "("):
			count(line)
		}
	}
	return direct, indirect
}
