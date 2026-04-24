package server

import (
	"html/template"
	"net/http"
	"path"
	"strings"
)

func funcMap() template.FuncMap {
	return template.FuncMap{
		"basename": func(p string) string {
			b := path.Base(p)
			return strings.TrimSuffix(b, path.Ext(b))
		},
		"dirname": func(p string) string {
			d := path.Dir(p)
			if d == "." {
				return ""
			}
			return d
		},
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"lower": strings.ToLower,
		"splitPath": func(p string) []map[string]any {
			parts := strings.Split(p, "/")
			out := make([]map[string]any, 0, len(parts))
			for i, seg := range parts {
				out = append(out, map[string]any{
					"Name":    seg,
					"IsFirst": i == 0,
					"IsLast":  i == len(parts)-1,
				})
			}
			return out
		},
		"kindIcon": func(k string) string {
			switch k {
			case "folder":
				return "folder"
			case "plan":
				return "clipboard-list"
			case "skill":
				return "wrench"
			case "memory":
				return "book-open"
			case "agent":
				return "drama"
			case "doc":
				return "file-text"
			case "index":
				return "house"
			default:
				return "file"
			}
		},
		"icon": func(name string) template.HTML {
			b, err := staticFS.ReadFile("static/icons/" + name + ".svg")
			if err != nil {
				return template.HTML("")
			}
			return template.HTML(b)
		},
	}
}

// renderPage renders a page template wrapped in layout.html, or just the page
// body when the request is an HTMX partial request. Also injects the `T`
// translation function and `Lang` tag into the data map so templates can do
// `{{ call .T "scope.key" args... }}`.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data any) {
	data = s.injectI18n(r, data)
	if r.Header.Get("HX-Request") == "true" {
		s.renderPartial(w, name, data)
		return
	}
	tpl, ok := s.tpls[name]
	if !ok {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// injectI18n adds the translation helper and current language tag to the
// template data. It accepts a map[string]any (already the canonical shape
// across gosidian handlers — see BUG-003) and returns it enriched. When the
// incoming data is not a map it is returned untouched; callers are expected
// to pass maps per project convention.
func (s *Server) injectI18n(r *http.Request, data any) any {
	m, ok := data.(map[string]any)
	if !ok {
		return data
	}
	lang := s.userLang(r)
	m["Lang"] = lang
	if s.catalog != nil {
		m["T"] = func(key string, args ...any) string {
			return s.catalog.T(lang, key, args...)
		}
	} else {
		m["T"] = func(key string, args ...any) string { return key }
	}
	return m
}

// renderPartial executes a single template without layout wrapping.
func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	tpl, ok := s.tpls[name]
	if !ok {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

func basenameMatch(notePath, target string) bool {
	b := path.Base(notePath)
	b = strings.TrimSuffix(b, path.Ext(b))
	return strings.EqualFold(b, target)
}
