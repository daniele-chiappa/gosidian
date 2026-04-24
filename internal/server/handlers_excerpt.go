package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
)

// handleNoteExcerpt returns a short JSON preview of a note by vault-relative
// path. Used by wikilink-preview.js to show a hover popover without
// navigating away. Skips frontmatter, ATX headings, and heavy markdown
// syntax — the goal is a human-readable ~300-char teaser.
func (s *Server) handleNoteExcerpt(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	body := string(note.Content)
	// Strip frontmatter so the excerpt starts with actual prose.
	if raw := parser.ExtractFrontmatterRaw(note.Content); raw != "" {
		// Find the end of the second --- marker and cut from there.
		if i := strings.Index(body, "---\n"); i == 0 {
			if j := strings.Index(body[i+4:], "---\n"); j >= 0 {
				body = body[i+4+j+4:]
			}
		}
	}
	body = strings.TrimLeft(body, "\n\t ")

	// Collapse heading lines into their text, drop wikilink/tag syntax so the
	// excerpt is readable as-is.
	excerpt := plainTextExcerpt(body, 300)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"path":    note.Path,
		"title":   note.Title,
		"excerpt": excerpt,
	})
}

// plainTextExcerpt returns the first maxRunes runes of a minimally-cleaned
// version of body. Cleans: heading markers, wikilink → alias/target, tag
// prefix, inline code backticks. Paragraphs are separated by a single space
// so the popover flows naturally.
func plainTextExcerpt(body string, maxRunes int) string {
	var sb strings.Builder
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			continue
		}
		// Strip ATX heading prefix (#, ##, ...).
		if strings.HasPrefix(trim, "#") {
			trim = strings.TrimLeft(trim, "# ")
		}
		// Wikilinks: [[target|alias]] → alias (or target).
		trim = stripWikiLinks(trim)
		// Backticks: remove markers to avoid broken monospace in plain view.
		trim = strings.ReplaceAll(trim, "`", "")
		sb.WriteString(trim)
		sb.WriteByte(' ')
		if sb.Len() > maxRunes*2 {
			break
		}
	}
	out := strings.TrimSpace(sb.String())
	runes := []rune(out)
	if len(runes) <= maxRunes {
		return out
	}
	return string(runes[:maxRunes]) + "…"
}

func stripWikiLinks(s string) string {
	// Replace [[x|y]] → y and [[x]] → x, honoring \| escape.
	var out strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '[' && s[i+1] == '[' {
			end := strings.Index(s[i+2:], "]]")
			if end < 0 {
				out.WriteString(s[i:])
				break
			}
			inner := s[i+2 : i+2+end]
			inner = strings.ReplaceAll(inner, `\|`, "|")
			if p := strings.LastIndex(inner, "|"); p >= 0 {
				inner = inner[p+1:]
			}
			out.WriteString(strings.TrimSpace(inner))
			i += 2 + end + 2
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
