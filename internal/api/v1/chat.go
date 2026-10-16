package v1

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/chatbot"
)

// The bots of the notes type: chat (IMP-191):
//
//	GET  /api/v1/chat/status?note=<path>  the bot of a note: its block as
//	                                      read, its problems, where it
//	                                      stands in its plugin
//	POST /api/v1/chat/token {note}        a token for the caller to chat
//	                                      with it, signed by gosidian
//	POST /api/v1/chat/configure {note}    hand it to its plugin now (a
//	                                      writer of the project)
//
// The note's project must be one the caller reads.

func (r *Router) chatOff(w http.ResponseWriter) bool {
	if r.deps.Chat != nil {
		return false
	}
	WriteError(w, http.StatusNotFound, CodeNotFound, "the bots need the pipelines ([pipelines] enabled)")
	return true
}

// chatNote is the note a chat request names, when the caller sees it.
func (r *Router) chatNote(w http.ResponseWriter, req *http.Request, fromBody bool) (string, bool) {
	note := req.URL.Query().Get("note")
	if fromBody && req.ContentLength != 0 {
		var body struct {
			Note string `json:"note"`
		}
		if err := DecodeJSON(req, &body); err != nil && !errors.Is(err, io.EOF) {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return "", false
		}
		if body.Note != "" {
			note = body.Note
		}
	}
	note = strings.Trim(strings.TrimSpace(note), "/")
	if note == "" || !strings.HasSuffix(note, ".md") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "note names the bot's note, a path ending in .md")
		return "", false
	}
	if !r.canSee(principalFromContext(req), note) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return "", false
	}
	return note, true
}

func (r *Router) handleChatStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.chatOff(w) {
		return
	}
	note, ok := r.chatNote(w, req, false)
	if !ok {
		return
	}
	n, err := r.deps.Vault.Load(note)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	spec, problems, found := chatbot.FromNote(note, n.Content)
	if !found {
		WriteError(w, http.StatusNotFound, CodeNotFound, "the note has no ```chat block")
		return
	}
	keys := make([]string, 0, len(spec.Access.Keys))
	for _, k := range spec.Access.Keys {
		keys = append(keys, k.Name)
	}
	out := map[string]any{
		"bot": spec.Name, "plugin": spec.Plugin, "pipeline": spec.Pipeline, "title": spec.Title,
		"access":   map[string]any{"users": spec.Access.Users, "keys": keys, "signed": spec.Access.Signed != "", "open": spec.Access.Open},
		"problems": problems,
	}
	if st, ok := r.deps.Chat.Status(note); ok {
		out["state"] = st
	}
	WriteJSON(w, http.StatusOK, out)
}

func (r *Router) handleChatToken(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.chatOff(w) {
		return
	}
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "sign in to chat")
		return
	}
	note, ok := r.chatNote(w, req, true)
	if !ok {
		return
	}
	token, st, expires, err := r.deps.Chat.UserToken(note, user.Username)
	if err != nil {
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"token": token, "bot": st.Bot, "embed_url": st.EmbedURL, "expires": expires})
}

func (r *Router) handleChatConfigure(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.chatOff(w) {
		return
	}
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "sign in")
		return
	}
	note, ok := r.chatNote(w, req, true)
	if !ok {
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(note)) {
		return
	}
	st, err := r.deps.Chat.ConfigureNote(req.Context(), note)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionPluginCall, Path: note, To: st.Plugin + "/" + st.Bot})
	WriteJSON(w, http.StatusOK, map[string]any{"state": st})
}
