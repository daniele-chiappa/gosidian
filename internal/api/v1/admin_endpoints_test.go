package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/webauth"
)

// ---- /api/v1/admin/tokens (MCP) ----

func TestAdminTokens_ListEmpty(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/tokens", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"total":0`) {
		t.Errorf("expected empty: %s", w.body)
	}
}

func TestAdminTokens_CreateAndList(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"name":"agent-a","scopes":["read","write"],"project":"alpha","password":"supersecret"}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", body, nil)
	if w.code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	var resp mcpTokenCreatedResponse
	_ = json.Unmarshal([]byte(w.body), &resp)
	if !strings.HasPrefix(resp.Token, "gosidian_") {
		t.Errorf("token shape: %q", resp.Token)
	}
	if resp.Record.Name != "agent-a" || resp.Record.Project != "alpha" {
		t.Errorf("record: %+v", resp.Record)
	}
	if !strings.Contains(resp.UsageHint, "Bearer") {
		t.Errorf("missing usage hint: %s", resp.UsageHint)
	}

	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/tokens", "", nil)
	if !strings.Contains(w2.body, `"name":"agent-a"`) {
		t.Errorf("token not in list: %s", w2.body)
	}
	if strings.Contains(w2.body, `"token":"`) {
		t.Errorf("plaintext leaked into list: %s", w2.body)
	}
}

func TestAdminTokens_CreateRejectsBadScope(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"name":"x","scopes":["god-mode"]}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", body, nil)
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.code)
	}
}

func TestAdminTokens_CreateRejectsMissingName(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"scopes":["read"]}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", body, nil)
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.code)
	}
}

func TestAdminTokens_Revoke(t *testing.T) {
	f := newAdminFixture(t)
	body := `{"name":"target","scopes":["read"],"password":"supersecret"}`
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", body, nil)
	var resp mcpTokenCreatedResponse
	_ = json.Unmarshal([]byte(w.body), &resp)
	id := resp.Record.ID

	w2 := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/tokens/"+id, "", nil)
	if w2.code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w2.code, w2.body)
	}
	w3 := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/tokens", "", nil)
	if !strings.Contains(w3.body, `"total":0`) {
		t.Errorf("token still listed after revoke: %s", w3.body)
	}
}

func TestAdminTokens_RevokeNotFound(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/tokens/deadbeef", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

// IMP-051: PATCH toggles the self-improve opt-in flag on an existing token,
// without recreating it. New tokens default to opt-out.
func TestAdminTokens_OptInToggle(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", `{"name":"enrol-me","scopes":["read"],"password":"supersecret"}`, nil)
	var resp mcpTokenCreatedResponse
	_ = json.Unmarshal([]byte(w.body), &resp)
	id := resp.Record.ID
	if resp.Record.SelfImproveOptIn {
		t.Fatalf("new token should default to opt-out: %+v", resp.Record)
	}

	wp := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/tokens/"+id, `{"self_improve_opt_in":true}`, nil)
	if wp.code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", wp.code, wp.body)
	}
	var updated mcpTokenView
	_ = json.Unmarshal([]byte(wp.body), &updated)
	if !updated.SelfImproveOptIn {
		t.Errorf("expected opt-in after PATCH true: %+v", updated)
	}

	wl := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/tokens", "", nil)
	if !strings.Contains(wl.body, `"self_improve_opt_in":true`) {
		t.Errorf("opt-in not persisted in list: %s", wl.body)
	}

	ww := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/tokens/"+id, `{"self_improve_opt_in":false}`, nil)
	if ww.code != http.StatusOK {
		t.Fatalf("patch off status=%d body=%s", ww.code, ww.body)
	}
	var off mcpTokenView
	_ = json.Unmarshal([]byte(ww.body), &off)
	if off.SelfImproveOptIn {
		t.Errorf("expected opt-out after PATCH false: %+v", off)
	}
}

func TestAdminTokens_OptInRejectsEmptyBody(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", `{"name":"x","scopes":["read"],"password":"supersecret"}`, nil)
	var resp mcpTokenCreatedResponse
	_ = json.Unmarshal([]byte(w.body), &resp)
	if resp.Record.ID == "" {
		t.Fatalf("create: %s", w.body)
	}
	wp := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/tokens/"+resp.Record.ID, `{}`, nil)
	if wp.code != http.StatusBadRequest {
		t.Errorf("empty patch status=%d, want 400", wp.code)
	}
}

func TestAdminTokens_OptInNotFound(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/tokens/deadbeef", `{"self_improve_opt_in":true}`, nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

func TestAdminTokens_MemberForbidden(t *testing.T) {
	f := newAdminFixture(t)
	memberTok := f.memberToken(t)
	w := f.request(http.MethodGet, "/api/v1/admin/tokens", "", map[string]string{
		"Authorization": "Bearer " + memberTok,
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", w.Code)
	}
}

// ---- /api/v1/admin/spa-tokens ----

func TestAdminSpaTokens_ListAndRevoke(t *testing.T) {
	f := newAdminFixture(t)
	// f.bearer was minted in newNotesFixture; query for the owner's
	// active sessions.
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/spa-tokens?user_id="+f.owner.ID, "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, `"user_id":"`+f.owner.ID+`"`) {
		t.Errorf("missing owner session: %s", w.body)
	}

	// Pull the ID out of the response and revoke it.
	var parsed struct {
		Items []spaTokenView `json:"items"`
	}
	if err := json.Unmarshal([]byte(w.body), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Items) == 0 {
		t.Fatal("no spa tokens to revoke")
	}
	id := parsed.Items[0].ID

	wRevoke := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/spa-tokens/"+id, "", nil)
	if wRevoke.code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", wRevoke.code, wRevoke.body)
	}
	// Subsequent calls with the same Bearer must 401 — we just
	// revoked our own session.
	wAfter := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/spa-tokens", "", nil)
	if wAfter.code != http.StatusUnauthorized {
		t.Errorf("revoked session still valid: %d body=%s", wAfter.code, wAfter.body)
	}
}

// ---- /api/v1/admin/users ----

func TestAdminUsers_List(t *testing.T) {
	f := newAdminFixture(t)
	if _, err := f.webauth.AddUser("alice", "alice-pass-123", webauth.RoleMember); err != nil {
		t.Fatal(err)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	for _, u := range []string{"owner", "alice"} {
		if !strings.Contains(w.body, `"username":"`+u+`"`) {
			t.Errorf("missing user %q: %s", u, w.body)
		}
	}
}

func TestAdminUsers_DisableCascadesTokens(t *testing.T) {
	f := newAdminFixture(t)
	user, err := f.webauth.AddUser("carol", "carol-pass-123", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	// Mint an MCP token for carol so we can verify the cascade.
	plain, _, err := f.mcpStore.Create("carol-agent", nil, []string{"read"}, 0, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = plain // we won't use it; we'll inspect the store directly

	if got := len(f.mcpStore.List()); got != 1 {
		t.Fatalf("setup: expected 1 mcp token, got %d", got)
	}

	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/"+user.ID, "", nil)
	if w.code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}

	if got := len(f.mcpStore.List()); got != 0 {
		t.Errorf("mcp token cascade failed: %d tokens remain", got)
	}
	// Re-listing users should show the user as disabled.
	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users", "", nil)
	if !strings.Contains(w2.body, `"disabled_at"`) {
		t.Errorf("disabled_at missing in user list: %s", w2.body)
	}
}

func TestAdminUsers_DisableOwnerForbidden(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/"+f.owner.ID, "", nil)
	if w.code == http.StatusNoContent {
		t.Errorf("owner should not be disable-able")
	}
}

// ---- /api/v1/admin/invites ----

func TestAdminInvites_CreateListRevoke(t *testing.T) {
	f := newAdminFixture(t)

	w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/invites", `{}`, nil)
	if w.code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.code, w.body)
	}
	var inv inviteView
	_ = json.Unmarshal([]byte(w.body), &inv)
	if inv.Token == "" {
		t.Errorf("missing token: %s", w.body)
	}
	if !inv.Pending {
		t.Errorf("invite should be pending: %+v", inv)
	}

	w2 := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/invites", "", nil)
	if !strings.Contains(w2.body, inv.Token) {
		t.Errorf("created invite missing from list: %s", w2.body)
	}

	w3 := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/invites/"+inv.Token, "", nil)
	if w3.code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", w3.code, w3.body)
	}
	w4 := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/invites", "", nil)
	if strings.Contains(w4.body, inv.Token) {
		t.Errorf("invite still listed after revoke: %s", w4.body)
	}
}

func TestAdminInvites_RevokeUnknown(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/invites/unknown", "", nil)
	if w.code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.code)
	}
}

// ---- /api/v1/admin/audit ----

func TestAdminAudit_TailRecent(t *testing.T) {
	f := newAdminFixture(t)
	// Generate some audit entries by exercising the create-token
	// path; these touch ActionTokenCreate.
	body := `{"name":"observed","scopes":["read"],"password":"supersecret"}`
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", body, nil); w.code != http.StatusCreated {
		t.Fatalf("setup token create failed: %s", w.body)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/audit?limit=10", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, string(audit.ActionTokenCreate)) {
		t.Errorf("expected token_create entry: %s", w.body)
	}
}

func TestAdminAudit_FilterByAction(t *testing.T) {
	f := newAdminFixture(t)
	// Two distinct audit-emitting actions.
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens",
		`{"name":"a","scopes":["read"],"password":"supersecret"}`, nil)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/admin/invites", `{}`, nil)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/audit?action=token_create", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d", w.code)
	}
	if !strings.Contains(w.body, "token_create") {
		t.Errorf("missing token_create: %s", w.body)
	}
	if strings.Contains(w.body, `"invite_create"`) {
		t.Errorf("filter leaked invite_create: %s", w.body)
	}
}

func TestAdminAudit_RejectsBadSince(t *testing.T) {
	f := newAdminFixture(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/audit?since=not-a-date", "", nil)
	if w.code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.code)
	}
}

func TestAdminAudit_MemberForbidden(t *testing.T) {
	f := newAdminFixture(t)
	tok := f.memberToken(t)
	w := f.request(http.MethodGet, "/api/v1/admin/audit", "", map[string]string{
		"Authorization": "Bearer " + tok,
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("status=%d, want 403", w.Code)
	}
}
