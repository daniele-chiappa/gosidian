package v1

import (
	"net/http"
	"time"

	"github.com/gosidian/gosidian/internal/plugins"
)

// GET /api/v1/plugins: the plugins of the pipelines (IMP-168), their
// status and node types, for every signed-in account while the pipelines
// are on. Only the owner sees their addresses and errors, which name
// hosts of the network.

type pluginView struct {
	Name    string             `json:"name"`
	Status  string             `json:"status"`
	Version string             `json:"version,omitempty"`
	SHA     string             `json:"sha,omitempty"`
	Nodes   []plugins.NodeInfo `json:"nodes"`
	// Sources are the accepted manifest's (IMP-172 phase 2).
	Sources  []plugins.SourceSpec `json:"sources"`
	Accepted *acceptedPluginView  `json:"accepted,omitempty"`
	Checked  *time.Time           `json:"checked,omitempty"`
	Error    string               `json:"error,omitempty"`
	URL      string               `json:"url,omitempty"`
}

type acceptedPluginView struct {
	Version string    `json:"version"`
	SHA     string    `json:"sha"`
	At      time.Time `json:"at"`
	By      string    `json:"by,omitempty"`
}

func (r *Router) handlePlugins(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.pipelinesOff(w) {
		return
	}
	owner := r.seesAllProjects(principalFromContext(req))
	out := []pluginView{}
	if r.deps.Plugins != nil {
		for _, s := range r.deps.Plugins.States() {
			out = append(out, pluginViewOf(s, owner))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// pluginAppView is an app of an accepted manifest (IMP-226), for the
// window that opens it: origin is the one its messages must come from, the
// origin of url or of the one of alt_urls the window opens.
type pluginAppView struct {
	Plugin      string   `json:"plugin"`
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url"`
	AltURLs     []string `json:"alt_urls,omitempty"`
	Origin      string   `json:"origin"`
	NoteType    string   `json:"note_type"`
	Bridge      string   `json:"bridge"`
	Engine      string   `json:"engine,omitempty"`
	Status      string   `json:"status"`
}

// handlePluginApps answers GET /api/v1/plugins/apps: the apps of the
// accepted manifests, to every signed-in account while the pipelines are
// on. The page reaches the vault only through the window, with the
// rights of whoever opened it.
func (r *Router) handlePluginApps(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.pipelinesOff(w) {
		return
	}
	out := []pluginAppView{}
	if r.deps.Plugins != nil {
		for _, a := range r.deps.Plugins.Apps() {
			st, _ := r.deps.Plugins.State(a.Plugin)
			out = append(out, pluginAppView{Plugin: a.Plugin, Name: a.Spec.Name, Title: a.Spec.Title, Description: a.Spec.Description,
				URL: a.Spec.URL, AltURLs: a.Spec.AltURLs, Origin: a.Spec.Origin(), NoteType: a.Spec.NoteType, Bridge: a.Spec.Bridge, Engine: a.Spec.Engine, Status: st.Status})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func pluginViewOf(s plugins.State, owner bool) pluginView {
	v := pluginView{Name: s.Name, Status: s.Status, Version: s.Version, SHA: short(s.SHA), Nodes: s.Nodes}
	if v.Nodes == nil {
		v.Nodes = []plugins.NodeInfo{}
	}
	v.Sources = []plugins.SourceSpec{}
	if s.Accepted != nil && len(s.Accepted.Manifest.Sources) > 0 {
		v.Sources = s.Accepted.Manifest.Sources
	}
	if !s.Checked.IsZero() {
		c := s.Checked
		v.Checked = &c
	}
	if a := s.Accepted; a != nil {
		v.Accepted = &acceptedPluginView{Version: a.Manifest.Version, SHA: short(a.SHA), At: a.At, By: a.By}
	}
	if owner {
		v.URL, v.Error = s.URL, s.Error
	}
	return v
}
