package server

import "net/http"

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	hits, err := s.index.Search(q, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, "search_results.html", map[string]any{
		"Title": "Search: " + q,
		"Query": q,
		"Hits":  hits,
	})
}
