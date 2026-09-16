package mcp

import "testing"

// The allowlist must be a structural match on scheme + host + port + path,
// never a string prefix (BUG-033): every "attack" URL below starts with the
// literal text of an allowlist entry and must still be rejected.
func TestIngestURLAllowed_StructuralMatch(t *testing.T) {
	s, _, _ := newTestServer(t)
	if err := s.SetIngestURLAllowlist([]string{
		"https://api.example.com",       // whole host, no trailing slash
		"http://files.example.com/pub",  // base path
		"https://cdn.example.com:8443/", // explicit non-default port
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://api.example.com/x", true},
		{"https://API.example.com/x", true},
		{"https://api.example.com:443/x", true},
		{"https://api.example.com", true},
		{"https://api.example.com@169.254.169.254/latest/meta-data/", false},
		{"https://api.example.com.evil.com/x", false},
		{"https://api.example.com:8080/x", false},
		{"http://api.example.com/x", false},
		{"http://files.example.com/pub", true},
		{"http://files.example.com/pub/a.pdf", true},
		{"http://files.example.com/public/a.pdf", false},
		{"http://files.example.com/pub/../secret", false},
		{"http://files.example.com/x", false},
		{"https://cdn.example.com:8443/a", true},
		{"https://cdn.example.com/a", false},
		{"ftp://api.example.com/x", false},
		{"://bad", false},
	}
	for _, tc := range cases {
		if got := s.ingestURLAllowed(tc.url); got != tc.want {
			t.Errorf("ingestURLAllowed(%q)=%v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestSetIngestURLAllowlist_RejectsMalformedEntries(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, bad := range []string{"example.com", "ftp://x/", "https://user:pw@host/", "https:///nohost"} {
		if err := s.SetIngestURLAllowlist([]string{bad}); err == nil {
			t.Errorf("entry %q accepted; want error", bad)
		}
	}
	if err := s.SetIngestURLAllowlist(nil); err != nil {
		t.Fatalf("empty list must be accepted (channel off): %v", err)
	}
}
