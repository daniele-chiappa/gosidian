package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func withTrustedProxies(t *testing.T, cidrs ...string) {
	t.Helper()
	if err := SetTrustedProxies(cidrs); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	t.Cleanup(func() { _ = SetTrustedProxies(nil) })
}

// Without a trusted-proxy list X-Forwarded-For is attacker-controlled and
// must be ignored: the limiter keys on the peer address only.
func TestClientIP_IgnoresXFFWithoutTrustedProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/login", nil)
	r.RemoteAddr = "203.0.113.9:4242"
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP=%q, want peer address", got)
	}
}

func TestClientIP_TrustedProxyWalksFromTheRight(t *testing.T) {
	withTrustedProxies(t, "192.0.2.0/24", "10.0.0.0/8")
	cases := []struct {
		name, remote, xff, want string
	}{
		{"single hop", "192.0.2.1:1234", "198.51.100.7", "198.51.100.7"},
		{"client then internal hop", "192.0.2.1:1234", "198.51.100.7, 10.1.1.1", "198.51.100.7"},
		{"spoofed prefix is ignored", "192.0.2.1:1234", "1.2.3.4, 198.51.100.7", "198.51.100.7"},
		{"all hops trusted", "192.0.2.1:1234", "10.9.9.9, 10.1.1.1", "10.9.9.9"},
		{"peer not trusted", "203.0.113.9:4242", "198.51.100.7", "203.0.113.9"},
		{"no header", "192.0.2.1:1234", "", "192.0.2.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/login", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetTrustedProxies_RejectsGarbage(t *testing.T) {
	if err := SetTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error for a non-IP entry")
	}
	t.Cleanup(func() { _ = SetTrustedProxies(nil) })
	if err := SetTrustedProxies([]string{"10.0.0.1", "::1", " 192.0.2.0/24 "}); err != nil {
		t.Fatalf("bare IPs and CIDRs must parse: %v", err)
	}
}

// allowed() is consulted before the body is even decoded, so a stream of
// malformed login POSTs must not leave one map entry per source address.
func TestLoginLimiter_AllowedDoesNotCreateEntries(t *testing.T) {
	l := newLoginLimiter()
	for i := 0; i < 1000; i++ {
		if !l.allowed("10.0." + string(rune('0'+i%10)) + "." + string(rune('0'+i/100))) {
			t.Fatal("fresh IP must be allowed")
		}
	}
	if n := len(l.attempts); n != 0 {
		t.Fatalf("allowed() created %d entries; want 0", n)
	}
	l.registerFail("10.0.0.1")
	if n := len(l.attempts); n != 1 {
		t.Fatalf("registerFail must create exactly one entry, got %d", n)
	}
}

// Entries whose failures all fell out of the window are dropped on the next
// allowed() call even when no further failure ever triggers registerFail's
// periodic sweep.
func TestLoginLimiter_ExpiredEntriesAreDropped(t *testing.T) {
	prev := LoginWindow
	LoginWindow = 50 * time.Millisecond
	t.Cleanup(func() { LoginWindow = prev })
	l := newLoginLimiter()
	l.registerFail("10.0.0.1")
	l.attempts["10.0.0.1"] = []time.Time{time.Now().Add(-time.Second)}
	if !l.allowed("10.0.0.1") {
		t.Fatal("expired failure must not count")
	}
	if _, ok := l.attempts["10.0.0.1"]; ok {
		t.Fatal("expired entry must be deleted, not kept empty")
	}
}
