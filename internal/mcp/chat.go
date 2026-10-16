package mcp

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/chatbot"
	"github.com/gosidian/gosidian/internal/index"
)

// SetChat gives the server the bots' service; nil while the pipelines
// are off.
func (s *Server) SetChat(c *chatbot.Service) { s.chat = c }

// Chat is the bots' service, nil when the pipelines are off.
func (s *Server) Chat() *chatbot.Service { return s.chat }

// ChatHost is the server as the bots read it (IMP-191).
func (s *Server) ChatHost() chatbot.Host { return chatHost{s} }

type chatHost struct{ s *Server }

func (h chatHost) ChatNotes() ([]chatbot.Note, error) {
	var out []chatbot.Note
	for offset := 0; ; offset += index.QueryMaxLimit {
		hits, total, err := h.s.index.Query(index.QueryOptions{
			Where:  []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{chatbot.NoteType}}},
			Sort:   "path",
			Limit:  index.QueryMaxLimit,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			if n, err := h.Note(hit.Path); err == nil {
				out = append(out, n)
			}
		}
		if len(hits) == 0 || offset+len(hits) >= total {
			return out, nil
		}
	}
}

func (h chatHost) Note(rel string) (chatbot.Note, error) {
	note, err := h.s.vault.Load(rel)
	if err != nil {
		return chatbot.Note{}, err
	}
	project, _, _ := strings.Cut(rel, "/")
	return chatbot.Note{Path: rel, Project: project, Content: note.Content}, nil
}

func (h chatHost) PipelineOn(rel string) (string, error) {
	p, err := pipelineHost(h).Pipeline(rel)
	switch {
	case err != nil:
		return "", err
	case p.Err != nil:
		return "", p.Err
	}
	return p.Spec.Trigger.On, nil
}

// NotesBefore pages through the folder's notes, the oldest first, until
// one changed at t or later.
func (h chatHost) NotesBefore(folder string, t time.Time) ([]string, error) {
	folder = strings.Trim(folder, "/")
	if !strings.Contains(folder, "/") {
		return nil, errors.New("the conversations' folder is a folder of a project")
	}
	var out []string
	for offset := 0; ; offset += index.QueryMaxLimit {
		hits, total, err := h.s.index.Query(index.QueryOptions{
			Folders: []string{folder + "/**"},
			Sort:    "modified",
			Limit:   index.QueryMaxLimit,
			Offset:  offset,
		})
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			if hit.ModTime >= t.Unix() {
				return out, nil
			}
			out = append(out, hit.Path)
		}
		if len(hits) == 0 || offset+len(hits) >= total {
			return out, nil
		}
	}
}

func (h chatHost) Discard(rel, actor string) error {
	if !h.s.vault.IsNoteFile(rel) {
		return fmt.Errorf("%s is not a note", rel)
	}
	return h.s.discardNote(rel, audit.SourceAutomation, actor)
}
