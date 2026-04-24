package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Server) handleGraphPage(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "graph.html", map[string]any{"Title": "Graph"})
}

func (s *Server) handleGraphJSON(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	includeCross, _ := strconv.ParseBool(r.URL.Query().Get("include_cross_project"))

	nodes, edges, err := s.index.GraphData(project, includeCross)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	projects, err := s.index.Projects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type cyData map[string]any
	type cyElement struct {
		Data cyData `json:"data"`
	}

	elements := make([]cyElement, 0, len(nodes)+len(edges))
	maxDegree := 0
	for _, n := range nodes {
		if n.Degree > maxDegree {
			maxDegree = n.Degree
		}
		elements = append(elements, cyElement{Data: cyData{
			"id":      n.Path,
			"label":   n.Title,
			"degree":  n.Degree,
			"project": n.Project,
		}})
	}
	maxCount := 0
	for _, e := range edges {
		if e.Count > maxCount {
			maxCount = e.Count
		}
		ed := cyData{
			"id":     e.From + "|" + e.To,
			"source": e.From,
			"target": e.To,
			"count":  e.Count,
		}
		if e.CrossProject {
			ed["cross_project"] = true
		}
		elements = append(elements, cyElement{Data: ed})
	}
	if maxDegree < 1 {
		maxDegree = 1
	}
	if maxCount < 1 {
		maxCount = 1
	}

	payload := map[string]any{
		"elements":  elements,
		"projects":  projects,
		"selected":  project,
		"maxDegree": maxDegree,
		"maxCount":  maxCount,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
