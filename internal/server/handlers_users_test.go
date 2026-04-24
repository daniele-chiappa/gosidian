package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/webauth"
)

// TestUsers_OwnerGet verifies the owner can render /admin/users and sees the
// create-invite form.
func TestUsers_OwnerGet(t *testing.T) {
	s, _, _, cookie := setupTokensServer(t) // reuse helper: owner admin set up
	w := authedReq(t, s, "GET", "/admin/users", "", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{"Utenti", "Crea invite", `value="create-invite"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestUsers_MemberForbidden verifies a member-role user gets 403 on
// /admin/users.
func TestUsers_MemberForbidden(t *testing.T) {
	s, _, _, _ := setupTokensServer(t)
	// Add a member and create a session for them.
	member, err := s.webauth.AddUser("alice", "alicepass1", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := s.webauth.CreateSession(member.ID, loginSessionTTL)
	memberCookie := &http.Cookie{Name: webauth.SessionCookieName, Value: sid}

	w := authedReq(t, s, "GET", "/admin/users", "", memberCookie)
	if w.Code != http.StatusForbidden {
		t.Errorf("member should get 403, got %d", w.Code)
	}
}

// TestUsers_CreateInviteFlow exercises the full owner → invite → signup loop.
func TestUsers_CreateInviteFlow(t *testing.T) {
	s, _, _, cookie := setupTokensServer(t)

	// Owner creates invite.
	w := authedReq(t, s, "POST", "/admin/users", "action=create-invite", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("create-invite status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "/signup?token=inv_") {
		t.Fatalf("expected signup URL with invite token in body: %s", body)
	}
	// Invites live in the webauth store; grab the pending one directly
	// instead of trying to parse the HTML.
	invs := s.webauth.ListInvites()
	if len(invs) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(invs))
	}
	invToken := invs[0].Token

	// Unauthenticated GET /signup?token=X should succeed (open path).
	r := httptest.NewRequest("GET", "/signup?token="+invToken, nil)
	w2 := httptest.NewRecorder()
	s.ServeHTTP(w2, r)
	if w2.Code != http.StatusOK {
		t.Fatalf("signup GET status = %d, body=%s", w2.Code, w2.Body.String())
	}
	if strings.Contains(w2.Body.String(), "Invite non valido") {
		t.Fatalf("signup GET with token=%q returned 'invalid invite'; pending invites: %+v", invToken, s.webauth.ListInvites())
	}
	if !strings.Contains(w2.Body.String(), "name=\"username\"") {
		t.Errorf("signup form missing username input: %s", w2.Body.String())
	}

	// POST signup with valid credentials.
	form := "token=" + invToken + "&username=bob&password=bobpass12&password_confirm=bobpass12"
	rp := httptest.NewRequest("POST", "/signup", strings.NewReader(form))
	rp.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wp := httptest.NewRecorder()
	s.ServeHTTP(wp, rp)
	if wp.Code != http.StatusSeeOther {
		t.Fatalf("signup POST status = %d, body=%s", wp.Code, wp.Body.String())
	}

	// Verify bob exists as a member.
	users := s.webauth.ListUsers()
	var bob *webauth.User
	for i := range users {
		if users[i].Username == "bob" {
			bob = &users[i]
			break
		}
	}
	if bob == nil {
		t.Fatal("bob not created")
	}
	if bob.Role != webauth.RoleMember {
		t.Errorf("bob.Role = %q", bob.Role)
	}

	// Re-using the same invite must fail.
	r2 := httptest.NewRequest("GET", "/signup?token="+invToken, nil)
	w3 := httptest.NewRecorder()
	s.ServeHTTP(w3, r2)
	if !strings.Contains(w3.Body.String(), "non valido o scaduto") {
		t.Errorf("expected 'invite consumed/invalid' error, got: %s", w3.Body.String())
	}
}

// TestUsers_DisableRevokesTokens verifies the cascade is wired when the
// server's webauth store has OnUserDisabled pointing at the token store.
func TestUsers_DisableRevokesTokens(t *testing.T) {
	s, tokStore, _, cookie := setupTokensServer(t)
	// Wire the cascade explicitly (main.go does this at boot; tests must
	// mirror that wiring).
	s.webauth.SetOnUserDisabled(func(uid string) {
		tokStore.RevokeByOwner(uid)
	})

	// Add member, create a token owned by them.
	member, err := s.webauth.AddUser("alice", "alicepass1", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	_, tok, err := tokStore.Create("alice-token", "", []string{"read"}, 0, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokStore.List()) == 0 {
		t.Fatal("token not created")
	}

	// Owner disables alice.
	form := "action=disable-user&id=" + member.ID
	w := authedReq(t, s, "POST", "/admin/users", form, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body=%s", w.Code, w.Body.String())
	}

	// alice-token must be gone.
	for _, t2 := range tokStore.List() {
		if t2.ID == tok.ID {
			t.Errorf("token %s should have been revoked after disable", tok.ID)
		}
	}
}

// TestTokens_MemberSeesOnlyOwn verifies the ownership filter on /admin/tokens.
func TestTokens_MemberSeesOnlyOwn(t *testing.T) {
	s, tokStore, _, _ := setupTokensServer(t)
	// Two members with one token each.
	alice, _ := s.webauth.AddUser("alice", "alicepass1", webauth.RoleMember)
	bob, _ := s.webauth.AddUser("bob", "bobpass1234", webauth.RoleMember)
	_, aliceTok, _ := tokStore.Create("alice-tok", "", []string{"read"}, 0, alice.ID)
	_, bobTok, _ := tokStore.Create("bob-tok", "", []string{"read"}, 0, bob.ID)

	// Alice's session.
	sid, _ := s.webauth.CreateSession(alice.ID, loginSessionTTL)
	aliceCookie := &http.Cookie{Name: webauth.SessionCookieName, Value: sid}

	w := authedReq(t, s, "GET", "/admin/tokens", "", aliceCookie)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, aliceTok.ID) {
		t.Errorf("alice should see her token: %s", body)
	}
	if strings.Contains(body, bobTok.ID) {
		t.Errorf("alice should NOT see bob's token")
	}
}
