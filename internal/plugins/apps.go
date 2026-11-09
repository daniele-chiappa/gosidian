package plugins

// PluginApp is an app of an accepted manifest (IMP-226): a page the SPA
// opens in a window of its own for the notes of its type.
type PluginApp struct {
	Plugin string
	Spec   AppSpec
}

// Apps lists the apps of the accepted manifests, in the order of the
// config. A manifest served and not accepted yet has none: its page is
// neither shown nor framed until the owner read it.
func (r *Registry) Apps() []PluginApp {
	acc, _ := r.store.all()
	var out []PluginApp
	for _, name := range r.order {
		a, ok := acc[name]
		if !ok {
			continue
		}
		for _, app := range a.Manifest.Apps {
			out = append(out, PluginApp{Plugin: name, Spec: app})
		}
	}
	return out
}

// AppOrigins are the origins of the accepted apps: the SPA's CSP admits
// them in frame-src.
func (r *Registry) AppOrigins() []string {
	acc, _ := r.store.all()
	var out []string
	for _, name := range r.order {
		if a, ok := acc[name]; ok {
			out = append(out, a.Manifest.AppOrigins()...)
		}
	}
	return out
}

// Origin is the origin of the app's address, "" when the address is none
// (a manifest that passed its check always has one).
func (a AppSpec) Origin() string {
	o, _ := appOrigin(a.URL)
	return o
}
