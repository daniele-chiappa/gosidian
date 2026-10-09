package oauth

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func cimdDoc(u string) []byte {
	return []byte(`{"client_id":"` + u + `","client_name":"C","redirect_uris":["https://app.example.com/cb"],"token_endpoint_auth_method":"none"}`)
}

// A fetch the request itself cut short is not remembered: cached for a
// minute, it kept a legitimate client out (IMP-159, S1-12). The fetch's
// own timeout, the request still alive, is remembered as any failure.
func TestCIMDCache_NoCachedCancellation(t *testing.T) {
	c := newCIMDCache()
	docURL := "https://app.example.com/client.json"
	calls := 0
	c.fetch = func(ctx context.Context, u string) ([]byte, http.Header, error) {
		calls++
		switch {
		case ctx.Err() != nil:
			return nil, nil, ctx.Err()
		case calls == 2:
			return nil, nil, context.DeadlineExceeded // the client's 10 s timeout
		}
		return cimdDoc(u), nil, nil
	}
	now := time.Now()
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.resolve(gone, docURL, now); err == nil {
		t.Fatal("the cancelled fetch should fail")
	}
	if _, err := c.resolve(context.Background(), docURL, now.Add(time.Second)); err == nil {
		t.Fatal("the fetch timeout should fail")
	}
	if _, err := c.resolve(context.Background(), docURL, now.Add(2*time.Second)); err == nil || calls != 2 {
		t.Fatalf("the timeout was not remembered: %v, %d fetches", err, calls)
	}
}

// At the cap, one entry makes room: the whole cache went, so a burst of
// bogus ids flushed every legitimate client (IMP-159, S1-12).
func TestCIMDCache_EvictsOneAtTheCap(t *testing.T) {
	c := newCIMDCache()
	c.fetch = func(_ context.Context, u string) ([]byte, http.Header, error) { return cimdDoc(u), nil, nil }
	now := time.Now()
	legit := "https://legit.example.com/client.json"
	if _, err := c.resolve(context.Background(), legit, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cimdMaxEntries+5; i++ {
		if _, err := c.resolve(context.Background(), fmt.Sprintf("https://bogus.example.com/%d.json", i), now); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) > cimdMaxEntries {
		t.Fatalf("%d entries over the cap", len(c.entries))
	}
	if _, ok := c.entries[legit]; !ok {
		t.Error("the legitimate client, the last to expire, was flushed")
	}
}
