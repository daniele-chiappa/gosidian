package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
)

// defaultDownloadTicketTTL bounds how long a minted download ticket stays
// redeemable. Short by design, like an upload ticket: the URL carries no
// bearer, so its window of validity is its whole security budget.
const defaultDownloadTicketTTL = 5 * time.Minute

// maxDownloadTicketsPerToken caps the unredeemed download tickets one token
// may hold: they live in memory until redeemed or expired.
const maxDownloadTicketsPerToken = 16

// downloadTicket is a pending memory_get transfer:"http": one note, readable
// once on the redemption endpoint by whoever holds the id (IMP-142).
type downloadTicket struct {
	Path    string
	TokenID string
	Expires time.Time
}

// mintDownloadTicket handles memory_get transfer:"http". The note has been
// loaded and scope-checked by the caller; the agent gets a single-use URL it
// can GET with no Authorization header, so it never has to read its own
// bearer from the client's configuration (which a cautious client refuses)
// and the body never crosses the model context.
func (s *Server) mintDownloadTicket(ctx context.Context, tok *auth.Token, path, etag string, size int) *mcp.CallToolResult {
	// Same fail-closed shape as /download: a project hidden from MCP is
	// not found.
	if s.pathInHiddenProject(path) {
		return mcp.NewToolResultErrorf("note %q not found", path)
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return mcp.NewToolResultErrorFromErr("mint ticket", err)
	}
	id := hex.EncodeToString(buf)
	tk := &downloadTicket{Path: path, TokenID: tok.ID, Expires: time.Now().Add(defaultDownloadTicketTTL)}

	s.downloadTicketsMu.Lock()
	if s.downloadTickets == nil {
		s.downloadTickets = make(map[string]*downloadTicket)
	}
	now := time.Now()
	pending := 0
	for k, v := range s.downloadTickets {
		if now.After(v.Expires) {
			delete(s.downloadTickets, k)
			continue
		}
		if v.TokenID == tok.ID {
			pending++
		}
	}
	if pending >= maxDownloadTicketsPerToken {
		s.downloadTicketsMu.Unlock()
		return mcp.NewToolResultErrorf("too many pending download tickets for this token (max %d): redeem them or let them expire", maxDownloadTicketsPerToken)
	}
	s.downloadTickets[id] = tk
	s.downloadTicketsMu.Unlock()

	endpoint := basePathFromContext(ctx) + "/download/" + id
	res, _ := mcp.NewToolResultJSON(map[string]any{
		"path":       path,
		"etag":       etag,
		"size":       size,
		"ticket":     id,
		"endpoint":   endpoint,
		"expires":    tk.Expires.UTC().Format(time.RFC3339),
		"method":     "GET",
		"single_use": true,
		"hint":       "GET this endpoint on the SAME host as your MCP URL — no Authorization header needed, the ticket is the credential. Single-use: any attempt consumes it; on failure call memory_get with transfer:\"http\" again. The response carries the note's ETag, to pass as if_match on the write that follows. Example: curl -sf -o note.md <mcp-host>" + endpoint,
	})
	return res
}

// takeDownloadTicket atomically consumes a ticket, like takeIngestTicket: the
// bool reports whether the id existed, a nil ticket with ok=true that it had
// expired, and either way the id is gone afterwards.
func (s *Server) takeDownloadTicket(id string) (*downloadTicket, bool) {
	s.downloadTicketsMu.Lock()
	defer s.downloadTicketsMu.Unlock()
	tk, ok := s.downloadTickets[id]
	if !ok {
		return nil, false
	}
	delete(s.downloadTickets, id)
	if time.Now().After(tk.Expires) {
		return nil, true
	}
	return tk, true
}

// handleDownloadTicketRedeem is the HTTP side of memory_get transfer:"http",
// mounted at <basePath>/download/<ticket>. It authenticates by ticket (no
// bearer), rebinds the minting token, and serves the note through the same
// checks as /download, so a token narrowed or revoked since the mint reads
// nothing more than it may now.
func (s *Server) handleDownloadTicketRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: GET the ticket endpoint")
		return
	}
	i := strings.LastIndex(r.URL.Path, "/download/")
	id := ""
	if i >= 0 {
		id = r.URL.Path[i+len("/download/"):]
	}
	if id == "" || strings.Contains(id, "/") {
		writeJSONError(w, http.StatusNotFound, "unknown download endpoint")
		return
	}
	tk, existed := s.takeDownloadTicket(id)
	if !existed {
		writeJSONError(w, http.StatusNotFound, "unknown or already-consumed download ticket (single-use: mint a new one with memory_get transfer:\"http\")")
		return
	}
	if tk == nil {
		writeJSONError(w, http.StatusGone, "download ticket expired; mint a new one with memory_get transfer:\"http\"")
		return
	}
	tok := s.tokenByID(tk.TokenID)
	if tok == nil {
		writeJSONError(w, http.StatusForbidden, "the token that minted this ticket no longer exists")
		return
	}
	if !tok.HasScope(auth.ScopeRead) {
		writeJSONError(w, http.StatusForbidden, "token lacks read scope")
		return
	}
	s.serveNoteBytes(w, tok, tk.Path)
}

// tokenByID finds the token a ticket was minted with: the admin token in
// open mode (see openMode), nil when it no longer exists or expired since
// the mint.
func (s *Server) tokenByID(id string) *auth.Token {
	if s.openMode() {
		return auth.AdminToken()
	}
	for _, t := range s.tokens.List() {
		if t.ID == id {
			if t.Expired() {
				return nil
			}
			tt := t
			return &tt
		}
	}
	return nil
}
