package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
)

// defaultIngestTicketTTL bounds how long a minted upload ticket stays
// redeemable. Short by design: a ticket URL carries no bearer, so its window
// of validity is its whole security budget.
const defaultIngestTicketTTL = 5 * time.Minute

// maxIngestTicketsPerToken caps the unredeemed tickets one token may hold.
// Tickets live in memory until redeemed or expired, so without a cap a
// write-scoped token minting in a loop grows the map at whatever rate the
// transport allows.
const maxIngestTicketsPerToken = 16

// ingestTicket is a pending memory_ingest transfer:http intent, waiting for
// its bytes to arrive on the redemption endpoint. In-memory and single-use:
// the first redemption attempt consumes it, success or not.
type ingestTicket struct {
	Intent  ingestIntent
	TokenID string
	Expires time.Time
}

// mintIngestTicket handles memory_ingest transfer:"http": it validates the
// intent as far as possible without the bytes (project scope, `as` value),
// then parks it under an unguessable single-use id. The response tells the
// agent where to POST the file.
func (s *Server) mintIngestTicket(ctx context.Context, project, as string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	switch as {
	case "", "auto", "table", "media", "note", "attachment":
	default:
		return mcp.NewToolResultError("as must be one of: auto, table, media, note, attachment"), nil
	}
	tok, errRes := s.authorizeWrite(ctx, project+"/ingest-probe.md")
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(tok, 0); errRes != nil {
		return errRes, nil
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return mcp.NewToolResultErrorFromErr("mint ticket", err), nil
	}
	id := hex.EncodeToString(buf)
	ttl := s.ingestTicketTTL
	if ttl <= 0 {
		ttl = defaultIngestTicketTTL
	}
	tk := &ingestTicket{
		Intent: ingestIntent{
			Project:   project,
			As:        as,
			NotePath:  strings.TrimSpace(req.GetString("note_path", "")),
			Title:     strings.TrimSpace(req.GetString("title", "")),
			Caption:   req.GetString("caption", ""),
			Filename:  strings.TrimSpace(req.GetString("filename", "")),
			Overwrite: req.GetBool("overwrite", false),
			IfMatch:   req.GetString("if_match", ""),
		},
		TokenID: tok.ID,
		Expires: time.Now().Add(ttl),
	}

	s.ingestTicketsMu.Lock()
	if s.ingestTickets == nil {
		s.ingestTickets = make(map[string]*ingestTicket)
	}
	now := time.Now()
	pending := 0
	for k, v := range s.ingestTickets {
		if now.After(v.Expires) {
			delete(s.ingestTickets, k)
			continue
		}
		if v.TokenID == tok.ID {
			pending++
		}
	}
	if pending >= maxIngestTicketsPerToken {
		s.ingestTicketsMu.Unlock()
		return mcp.NewToolResultErrorf("too many pending ingest tickets for this token (max %d): redeem them or let them expire", maxIngestTicketsPerToken), nil
	}
	s.ingestTickets[id] = tk
	s.ingestTicketsMu.Unlock()

	endpoint := basePathFromContext(ctx) + "/ingest/" + id
	return mcp.NewToolResultJSON(map[string]any{
		"ticket":     id,
		"endpoint":   endpoint,
		"expires":    tk.Expires.UTC().Format(time.RFC3339),
		"method":     "POST",
		"field":      "file",
		"single_use": true,
		"hint":       "POST the file as multipart field 'file' to this endpoint on the SAME host as your MCP /sse URL — no Authorization header needed, the ticket is the credential. Single-use: any attempt consumes it; on failure mint a new one. Example: curl -sf -F file=@report.html <mcp-host>" + endpoint,
	})
}

// takeIngestTicket atomically consumes a ticket. The bool reports whether the
// id existed at all; a nil ticket with ok=true means it existed but had
// expired. Either way the id is gone afterwards — single-use includes failed
// and late attempts.
func (s *Server) takeIngestTicket(id string) (*ingestTicket, bool) {
	s.ingestTicketsMu.Lock()
	defer s.ingestTicketsMu.Unlock()
	tk, ok := s.ingestTickets[id]
	if !ok {
		return nil, false
	}
	delete(s.ingestTickets, id)
	if time.Now().After(tk.Expires) {
		return nil, true
	}
	return tk, true
}

// handleIngestTicketRedeem is the HTTP side of transfer:http, mounted at
// <basePath>/ingest/<ticket>. It authenticates by ticket (no bearer), rebinds
// the minting token, and executes the parked intent against the uploaded
// bytes via the same ingestRaw core as the url source.
func (s *Server) handleIngestTicketRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: POST a multipart file in the 'file' field")
		return
	}
	i := strings.LastIndex(r.URL.Path, "/ingest/")
	if i < 0 {
		writeJSONError(w, http.StatusNotFound, "unknown ingest endpoint")
		return
	}
	id := r.URL.Path[i+len("/ingest/"):]
	if id == "" || strings.Contains(id, "/") {
		writeJSONError(w, http.StatusNotFound, "unknown ingest endpoint")
		return
	}

	tk, existed := s.takeIngestTicket(id)
	if !existed {
		writeJSONError(w, http.StatusNotFound, "unknown or already-consumed ingest ticket (single-use: mint a new one with memory_ingest transfer:\"http\")")
		return
	}
	if tk == nil {
		writeJSONError(w, http.StatusGone, "ingest ticket expired; mint a new one with memory_ingest transfer:\"http\"")
		return
	}

	// Rebind the minting token so scope, audit, and rate limits apply as if
	// the bytes had arrived through the MCP call itself.
	var tok *auth.Token
	if s.tokens == nil || s.tokens.Empty() {
		tok = auth.AdminToken()
	} else {
		for _, t := range s.tokens.List() {
			if t.ID == tk.TokenID {
				tt := t
				tok = &tt
				break
			}
		}
		if tok == nil {
			writeJSONError(w, http.StatusForbidden, "the token that minted this ticket no longer exists")
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, multipartBodyCap)
	if err := r.ParseMultipartForm(multipartBodyCap); err != nil {
		writeMultipartError(w, err)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing 'file' field (multipart/form-data)")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, attach.MaxBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read upload: "+err.Error())
		return
	}
	if len(data) > attach.MaxBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "file too large (max 10 MiB)")
		return
	}
	if msg := s.writeLimitViolation(tok, len(data)); msg != "" {
		writeJSONError(w, http.StatusTooManyRequests, msg)
		return
	}

	intent := tk.Intent
	if intent.Filename == "" {
		intent.Filename = hdr.Filename
	}

	ctx := context.WithValue(r.Context(), tokenCtxKey, tok)
	res, _ := s.ingestRaw(ctx, intent, data)
	if res == nil {
		writeJSONError(w, http.StatusInternalServerError, "ingest produced no result")
		return
	}
	body := callToolResultText(res)
	if res.IsError {
		writeJSONError(w, http.StatusUnprocessableEntity, body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// callToolResultText flattens a CallToolResult's text content — for JSON
// results (NewToolResultJSON) this is the JSON payload itself.
func callToolResultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
