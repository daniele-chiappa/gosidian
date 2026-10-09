package v1

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
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

// sseRecheckInterval is how often a stream with events to send checks its
// session and account again (streamPrincipal); a quiet one checks at each
// heartbeat. A variable for the tests.
var sseRecheckInterval = 5 * time.Second

// eventsCookieName carries the SPA session token to /api/v1/events
// (IMP-090). EventSource cannot set an Authorization header, and the token
// used to ride on the query string, where every reverse proxy's access log
// kept it. It now travels as an HttpOnly cookie scoped to the events path
// only, like gosidian_files for /vault-files/ (ADR-022): issued at login,
// re-issued by requireAuth, cleared at logout. The path is one read-only
// GET, so the cookie opens no CSRF exposure on the rest of the API.
const eventsCookieName = "gosidian_events"

func eventsCookie(token string, secure bool, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     eventsCookieName,
		Value:    token,
		Path:     "/api/v1/events",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

func clearEventsCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     eventsCookieName,
		Value:    "",
		Path:     "/api/v1/events",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// queryTokenWarn logs once that a client still sends ?token=.
var queryTokenWarn sync.Once

// eventsToken returns the session token of an events request: the cookie,
// else the deprecated ?token= of a SPA tab opened before the cookie
// existed (kept for one release, IMP-090).
func eventsToken(req *http.Request) string {
	if c, err := req.Cookie(eventsCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		return strings.TrimSpace(c.Value)
	}
	token := strings.TrimSpace(req.URL.Query().Get("token"))
	if token != "" {
		queryTokenWarn.Do(func() {
			log.Printf("events: a client sent its session token as ?token= (deprecated, IMP-090): reload the web UI; the query string reaches proxy access logs")
		})
	}
	return token
}

// handleEvents implements GET /api/v1/events as a Server-Sent Events
// stream. EventSource cannot ship custom headers, so the session token
// comes from the gosidian_events cookie (eventsToken). It is validated
// against the SpaTokenStore exactly like a normal /api/v1/* request.
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

	token := eventsToken(req)
	if token == "" {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no session: the gosidian_events cookie is missing (log in again)")
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
	if user.MustChangePassword {
		WriteError(w, http.StatusForbidden, CodeAuthPasswordChangeRequired, "password change required: set your own password first")
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
	princ := authz.Principal{UserID: user.ID, Role: user.Role, Restricted: user.Restricted}
	checked := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.Ch:
			if !open {
				return
			}
			// Checked again every few seconds, not at every frame: a burst
			// of events took the session store's lock once per frame and tab.
			if time.Since(checked) >= sseRecheckInterval {
				var alive bool
				if princ, alive = r.streamPrincipal(token); !alive {
					return // session or account gone: end the stream like requireAuth would
				}
				checked = time.Now()
			}
			data, ok := r.streamData(princ, ev)
			if !ok {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", ev.ID, ev.Topic, data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			// A quiet stream ends here, within a heartbeat of a logout.
			var alive bool
			if princ, alive = r.streamPrincipal(token); !alive {
				return
			}
			checked = time.Now()
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// streamPrincipal re-checks the subscriber's session and account, every few
// seconds of events and at each heartbeat, so a demotion takes effect soon and a
// logout, a revoked or expired session, a disabled account, a password to
// change or a TOTP to enrol end the stream without waiting for a reconnect:
// the token was checked only at the connection, and an open stream kept
// receiving paths and project names (BUG-112, S2-1). alive=false means
// "close".
func (r *Router) streamPrincipal(token string) (authz.Principal, bool) {
	spaTok, err := r.deps.Auth.SpaAuth.Check(token)
	if err != nil {
		return authz.Principal{}, false
	}
	user, ok := r.deps.Auth.WebAuth.UserByID(spaTok.UserID)
	if !ok || !user.Enabled() || user.MustChangePassword || r.deps.Auth.WebAuth.TOTPEnrollmentRequired(user) {
		return authz.Principal{}, false
	}
	return authz.Principal{UserID: user.ID, Role: user.Role, Restricted: user.Restricted}, true
}

// streamData applies the read predicate that gates every REST read to one
// SSE frame, and returns what of it p may receive, or false for nothing: the
// hub is a single shared stream, so without this filter a guest or a member
// outside a project would learn the paths and names of private notes and
// projects from the events about them (BUG-054). A frame naming a path, a
// project or a target ("to", of a rename or a move) goes to who can see each
// of them; a frame that names none (nothing to scope it to) goes to the
// owner only. A project that is gone, deleted or renamed away, has no access
// entry left, and judging it by the default visibility handed its name to
// every member (BUG-112, S2-5): such a frame reaches the others without its
// names. The web UI reloads on any tree or sidebar frame, payload unread.
func (r *Router) streamData(p authz.Principal, ev events.Event) ([]byte, bool) {
	var ref struct {
		Action  string `json:"action"`
		Path    string `json:"path"`
		Project string `json:"project"`
		To      string `json:"to"`
	}
	_ = json.Unmarshal(ev.Data, &ref)
	if ref.Path == "" && ref.Project == "" && ref.To == "" {
		return ev.Data, p.Role == webauth.RoleOwner
	}
	if p.Role == webauth.RoleOwner {
		return ev.Data, true
	}
	gone := false
	for _, ps := range []struct{ project, path string }{
		{projectOf(ref.Path), ref.Path},
		{ref.Project, ""},
	} {
		switch {
		case ps.project == "":
		case !r.deps.Vault.Exists(ps.project): // a stat, at every frame
			gone = true
		case ps.path != "" && !r.canSee(p, ps.path):
			return nil, false
		case ps.path == "" && !r.canAccessProject(p, ps.project):
			return nil, false
		}
	}
	if gone {
		data, _ := json.Marshal(map[string]string{"action": ref.Action})
		return data, true
	}
	// A note moved where the reader cannot follow: the frame still says it
	// left, without saying where to.
	if to := projectOf(ref.To); ref.To != "" && (!r.deps.Vault.Exists(to) || !r.canSee(p, ref.To)) {
		var full map[string]any
		if json.Unmarshal(ev.Data, &full) != nil {
			return nil, false
		}
		delete(full, "to")
		data, _ := json.Marshal(full)
		return data, true
	}
	return ev.Data, true
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
