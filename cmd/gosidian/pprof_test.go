package main

import "testing"

func TestLoopbackAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:6060", "localhost:6060", "[::1]:6060", "127.0.0.2:6060"} {
		if err := loopbackAddr(addr); err != nil {
			t.Errorf("%s: %v, want accepted", addr, err)
		}
	}
	for _, addr := range []string{":6060", "0.0.0.0:6060", "[::]:6060", "192.0.2.10:6060", "example.com:6060", "127.0.0.1"} {
		if err := loopbackAddr(addr); err == nil {
			t.Errorf("%s: accepted, want refused", addr)
		}
	}
}
