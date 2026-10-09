package main

import "testing"

// The healthcheck asks the port the server listens on (IMP-163).
func TestHealthURL(t *testing.T) {
	for addr, want := range map[string]string{
		"":                 "http://127.0.0.1:8080/healthz",
		":9090":            "http://127.0.0.1:9090/healthz",
		"0.0.0.0:9090":     "http://127.0.0.1:9090/healthz",
		"[::]:9090":        "http://[::1]:9090/healthz",
		"127.0.0.1:8080": "http://127.0.0.1:8080/healthz",
		"bogus":            "http://127.0.0.1:8080/healthz",
	} {
		if got := healthURL(addr); got != want {
			t.Errorf("healthURL(%q) = %s, want %s", addr, got, want)
		}
	}
}
