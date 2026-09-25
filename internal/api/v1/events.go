package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/webauth"
)

// sseHeartbeatInterval governs how often we send a comment-only frame
// to keep proxies (nginx in particular) from idling out the
// connection. EventSource silently reconnects on close so this is
// belt-and-suspenders, but operators run varied stacks behind us.
const sseHeartbeatInterval = 30 * time.Second

// handleEvents implements GET /api/v1/events as a Server-Sent Events
// stream. EventSource cannot ship custom headers so the Bearer token
// rides on the query string (?token=...). The token is validated
// against the SpaTokenStore exactly like a normal /api/v1/* request;
// the only difference is the location.
//
// Topic filter: ?topics=tree,note,sidebar — comma-separated whitelist
// from internal/server/events.Topic. Empty = subscribe to all.
//
// The endpoint never authoritatively replays history. Reconnect
// (Last-Event-ID) is honoured by the browser but the hub returns no
// past events; the SPA refetches the affected resources via REST on
// reconnect, which is the simpler invariant.
func (r *Router) handleEvents(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Events == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "events hub not configured")
		return
	}
	if r.deps.Auth == nil || r.deps.Auth.SpaAuth == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "spa auth not configured")
		return
	}

	token := strings.TrimSpace(req.URL.Query().Get("token"))
	if token == "" {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "token query param required")
		return
	}
	spaTok, err := r.deps.Auth.SpaAuth.Validate(token)
	if err != nil {
		code := CodeAuthTokenInvalid
		if strings.Contains(err.Error(), "expired") {
			code = CodeAuthTokenExpired
		}
		WriteError(w, http.StatusUnauthorized, code, err.Error())
		return
	}
	// Match requireAuth's user-disabled cascade so a revoked SPA
	// session can't keep streaming.
	user, ok := r.deps.Auth.WebAuth.UserByID(spaTok.UserID)
	if !ok || !user.Enabled() {
		_ = r.deps.Auth.SpaAuth.RevokeByHash(spaTok.Hash)
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "user no longer exists or is disabled")
		return
	}
	// Mirror requireAuth's enrolment gate: the SSE stream is not part of the
	// enrolment flow, so a user who still owes a TOTP secret cannot subscribe.
	// See BUG-020.
	if r.deps.Auth.WebAuth.TOTPEnrollmentRequired(user) {
		WriteError(w, http.StatusForbidden, CodeAuthEnrollmentRequired, "two-factor enrolment required before accessing this resource")
		return
	}

	topics := parseTopicList(req.URL.Query().Get("topics"))

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "streaming not supported")
		return
	}

	// SSE headers must be set before the first write, and we want
	// no buffering on the proxy path. Cache-Control no-cache is
	// nginx's gate-opener for SSE; X-Accel-Buffering: no is the
	// nginx-specific override when the operator runs a config that
	// otherwise buffers. Both are cheap.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	// Defense-in-depth headers don't hurt on SSE; CSP is irrelevant
	// for non-HTML content but the rest cost nothing.
	applySecurityHeaders(w)
	w.WriteHeader(http.StatusOK)

	// Initial flush so the client's onopen fires immediately rather
	// than waiting for the first heartbeat.
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	sub := r.deps.Events.Subscribe(topics...)
	defer sub.Unsubscribe()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	ctx := req.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.Ch:
			if !open {
				return
			}
			princ, alive := r.streamPrincipal(spaTok.UserID)
			if !alive {
				return // account disabled or gone: end the stream like requireAuth would
			}
			if !r.mayStream(princ, ev) {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Topic, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// streamPrincipal re-resolves the subscriber's account on every frame, so a
// demotion takes effect on the next event and a disabled account ends the
// stream without waiting for a reconnect. alive=false means "close".
func (r *Router) streamPrincipal(userID string) (authz.Principal, bool) {
	user, ok := r.deps.Auth.WebAuth.UserByID(userID)
	if !ok || !user.Enabled() {
		return authz.Principal{}, false
	}
	return authz.Principal{UserID: user.ID, Role: user.Role}, true
}

// mayStream applies the read predicate that gates every REST read to one SSE
// frame: the hub is a single shared stream, so without this filter a guest or
// a member outside a project would learn the paths and names of private
// notes and projects from the events about them (BUG-054). Frames that name
// a path or a project are gated by canSee on that project; frames that name
// neither (nothing to scope them to) go to the owner only.
func (r *Router) mayStream(p authz.Principal, ev events.Event) bool {
	var ref struct {
		Path    string `json:"path"`
		Project string `json:"project"`
	}
	_ = json.Unmarshal(ev.Data, &ref)
	switch {
	case ref.Path != "":
		return r.canSee(p, ref.Path)
	case ref.Project != "":
		return r.canAccessProject(p, ref.Project)
	default:
		return p.Role == webauth.RoleOwner
	}
}

// parseTopicList tokenises the comma-separated `topics=` query param
// into a typed slice. Unknown topic names pass through as-is — the
// hub treats them as "no match" so they're harmless to advertise; a
// strict validator here would just reject SPA versions that knew
// about new topics before the server.
func parseTopicList(raw string) []events.Topic {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]events.Topic, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, events.Topic(p))
	}
	return out
}
