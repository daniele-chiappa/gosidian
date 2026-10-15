package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"

	"github.com/gosidian/gosidian/internal/infolder"
)

// The sources of the plugins (IMP-172 phase 2): a plugin's manifest lists
// the archives it reads, the owner binds one to a project's IN folder
// (gosidian plugin bind), and the IN folders read it through a session
// (infolder.Sources).

// Bind binds a plugin's source to a project's IN folder, as the folder
// b.Folder (the source's name when empty). The plugin's accepted manifest
// must offer the source; a project holds a source once, and a folder once.
func (r *Registry) Bind(b infolder.Binding, by string) (Bound, error) {
	if _, ok := r.plugins[b.Plugin]; !ok {
		return Bound{}, fmt.Errorf("no plugin %q in [[pipelines.plugins]]", b.Plugin)
	}
	acc, err := r.store.all()
	if err != nil {
		return Bound{}, err
	}
	a, ok := acc[b.Plugin]
	if !ok {
		return Bound{}, fmt.Errorf("the manifest of plugin %s is not accepted yet (gosidian plugin show %s, then accept)", b.Plugin, b.Plugin)
	}
	if _, ok := a.Manifest.Source(b.Source); !ok {
		return Bound{}, fmt.Errorf("plugin %s offers no source %q (%s)", b.Plugin, b.Source, sourceNames(a.Manifest))
	}
	if b.Folder == "" {
		b.Folder = b.Source
	}
	if !infolder.ValidFolder(b.Folder) {
		return Bound{}, fmt.Errorf("folder %q: one plain name, not hidden", b.Folder)
	}
	if b.Every < 0 {
		return Bound{}, errors.New("every: the seconds between two checks, or 0 for the default")
	}
	bound := Bound{Binding: b, At: time.Now().UTC(), By: by}
	err = r.store.update(func(f *acceptedFile) error {
		for _, x := range f.Bindings {
			if x.Project != b.Project {
				continue
			}
			if x.Folder == b.Folder {
				return fmt.Errorf("the IN folder of %s has a source in %s already (%s): gosidian plugin unbind %s %s first", b.Project, b.Folder, x.ID(), b.Project, b.Folder)
			}
			if x.ID() == b.ID() {
				return fmt.Errorf("%s is bound to %s already, in IN/%s", b.ID(), b.Project, x.Folder)
			}
		}
		f.Bindings = append(f.Bindings, bound)
		return nil
	})
	return bound, err
}

// Unbind drops the binding of a project's folder. The source keeps its
// files; the IN folder forgets them at its next check.
func (r *Registry) Unbind(project, folder string) (Bound, error) {
	var gone Bound
	err := r.store.update(func(f *acceptedFile) error {
		for i, x := range f.Bindings {
			if x.Project == project && x.Folder == folder {
				gone = x
				f.Bindings = append(f.Bindings[:i], f.Bindings[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("the IN folder of %s has no source in %s", project, folder)
	})
	return gone, err
}

// ProjectRenamed follows a project's rename in the bindings and the
// exposed plugins.
func (r *Registry) ProjectRenamed(from, to string) error {
	return r.store.update(func(f *acceptedFile) error {
		for i := range f.Bindings {
			if f.Bindings[i].Project == from {
				f.Bindings[i].Project = to
			}
		}
		for i := range f.Exposed {
			if f.Exposed[i].Project == from {
				f.Exposed[i].Project = to
			}
		}
		return nil
	})
}

// ProjectRemoved drops the bindings and the exposed plugins of a project
// deleted (BUG-119): a later project of the name binds its own.
func (r *Registry) ProjectRemoved(project string) error {
	return r.store.update(func(f *acceptedFile) error {
		kept := f.Bindings[:0]
		for _, b := range f.Bindings {
			if b.Project != project {
				kept = append(kept, b)
			}
		}
		f.Bindings = kept
		exposed := f.Exposed[:0]
		for _, e := range f.Exposed {
			if e.Project != project {
				exposed = append(exposed, e)
			}
		}
		f.Exposed = exposed
		return nil
	})
}

// Bound lists the bindings, by project and folder.
func (r *Registry) Bound() ([]Bound, error) {
	list, err := r.store.bindings()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Project != list[j].Project {
			return list[i].Project < list[j].Project
		}
		return list[i].Folder < list[j].Folder
	})
	return list, err
}

// Bindings lists the bindings (infolder.Sources).
func (r *Registry) Bindings() []infolder.Binding {
	list, err := r.store.bindings()
	if err != nil {
		r.log.Warn("plugins: the bindings of the sources", "err", err)
	}
	out := make([]infolder.Binding, 0, len(list))
	for _, b := range list {
		out = append(out, b.Binding)
	}
	return out
}

func sourceNames(m Manifest) string {
	if len(m.Sources) == 0 {
		return "it offers none"
	}
	names := make([]string, 0, len(m.Sources))
	for _, s := range m.Sources {
		names = append(names, s.Name)
	}
	return "it offers " + strings.Join(names, ", ")
}

// Session opens a session with a bound source (infolder.Sources): the
// plugin must serve the accepted manifest, which must offer the source.
func (r *Registry) Session(ctx context.Context, b infolder.Binding) (infolder.SourceSession, error) {
	c, ok := r.plugins[b.Plugin]
	if !ok {
		return nil, fmt.Errorf("no plugin %q in [[pipelines.plugins]]", b.Plugin)
	}
	acc, err := r.store.all()
	if err != nil {
		return nil, err
	}
	a, ok := acc[b.Plugin]
	if !ok {
		return nil, fmt.Errorf("the manifest of plugin %s is not accepted", b.Plugin)
	}
	spec, ok := a.Manifest.Source(b.Source)
	if !ok {
		return nil, fmt.Errorf("the accepted manifest of plugin %s offers no source %q", b.Plugin, b.Source)
	}
	sess, err := r.connect(ctx, c)
	if err != nil {
		return nil, err
	}
	live, err := fetchManifest(ctx, sess, c.Name)
	if err != nil {
		sess.Close()
		return nil, err
	}
	if sha := live.SHA(); sha != a.SHA {
		sess.Close()
		return nil, fmt.Errorf("plugin %s serves another manifest (version %s, %s) than the accepted one (version %s, %s): gosidian plugin show %s, then accept",
			c.Name, live.Version, sha[:12], a.Manifest.Version, a.SHA[:12], c.Name)
	}
	pu, _ := url.Parse(c.URL)
	return &sourceSession{c: c, sess: sess, spec: spec, plugin: pu, hosts: a.Manifest.Permissions.SourceHosts}, nil
}

// sourceSession is a session with one source of a plugin.
type sourceSession struct {
	c      Config
	sess   *client.Client
	spec   SourceSpec
	plugin *url.URL
	hosts  []string
}

func (s *sourceSession) Info() infolder.SourceInfo {
	return infolder.SourceInfo{Kind: s.spec.Kind, Description: s.spec.Description, Changes: s.spec.Changes, Move: s.spec.Move}
}

func (s *sourceSession) Close() error { return s.sess.Close() }

// call calls a source tool, its result read into out.
func (s *sourceSession) call(ctx context.Context, tool string, args map[string]any, out any) error {
	if s.c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.c.Timeout)
		defer cancel()
	}
	args["source"] = s.spec.Name
	res, err := callTool(ctx, s.sess, tool, args)
	if err != nil {
		return fmt.Errorf("plugin %s, %s: %w", s.c.Name, tool, err)
	}
	if res.IsError {
		return fmt.Errorf("plugin %s, %s: %s", s.c.Name, tool, strings.TrimSpace(resultText(res)))
	}
	if err := json.Unmarshal(ResultJSON(res), out); err != nil {
		return fmt.Errorf("plugin %s, %s: the result is not what a source gives: %w", s.c.Name, tool, err)
	}
	return nil
}

func (s *sourceSession) List(ctx context.Context, cursor string, limit int) (infolder.RemotePage, error) {
	var page infolder.RemotePage
	err := s.call(ctx, SourceListTool, map[string]any{"cursor": cursor, "limit": limit}, &page)
	return page, err
}

func (s *sourceSession) Changes(ctx context.Context, cursor string) (infolder.RemoteChanges, error) {
	var ch infolder.RemoteChanges
	if !s.spec.Changes {
		return ch, fmt.Errorf("the source %s/%s gives no changes", s.c.Name, s.spec.Name)
	}
	err := s.call(ctx, SourceChangesTool, map[string]any{"cursor": cursor}, &ch)
	return ch, err
}

func (s *sourceSession) Open(ctx context.Context, p string) (infolder.RemoteURL, error) {
	var u infolder.RemoteURL
	if err := s.call(ctx, SourceOpenTool, map[string]any{"path": p}, &u); err != nil {
		return u, err
	}
	if err := s.allowed(u.URL); err != nil {
		return infolder.RemoteURL{}, fmt.Errorf("plugin %s, %s: %w", s.c.Name, SourceOpenTool, err)
	}
	return u, nil
}

func (s *sourceSession) Move(ctx context.Context, from, to string) (infolder.RemoteFile, error) {
	var f infolder.RemoteFile
	if !s.spec.Move {
		return f, fmt.Errorf("the source %s/%s cannot move its files", s.c.Name, s.spec.Name)
	}
	err := s.call(ctx, SourceMoveTool, map[string]any{"from": from, "to": to}, &f)
	return f, err
}

// allowed checks a URL a source gives: http(s), without credentials, to
// the plugin's host or one its accepted manifest names in source_hosts.
func (s *sourceSession) allowed(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("the url %q is not an http(s) address", raw)
	}
	if u.User != nil {
		return errors.New("the url carries credentials")
	}
	if HostAllowed(u, s.plugin, s.hosts) {
		return nil
	}
	return fmt.Errorf("the url points at %s, which is neither the plugin's host nor in its permissions.source_hosts", u.Host)
}

// HostAllowed says whether u is on the plugin's host or on a host of
// hosts: a host with a port allows that port only, one without any.
func HostAllowed(u, plugin *url.URL, hosts []string) bool {
	if plugin != nil && strings.EqualFold(u.Hostname(), plugin.Hostname()) && portOf(u) == portOf(plugin) {
		return true
	}
	for _, h := range hosts {
		a, err := url.Parse("http://" + h)
		if err != nil {
			continue
		}
		if !strings.EqualFold(a.Hostname(), u.Hostname()) {
			continue
		}
		if a.Port() == "" || a.Port() == portOf(u) {
			return true
		}
	}
	return false
}

// portOf is a URL's port, its scheme's when it names none.
func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
