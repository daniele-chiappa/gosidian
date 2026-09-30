package v1

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

// A limit above the maximum gets the maximum, not the default (BUG-075).
func TestLimitParam(t *testing.T) {
	for raw, want := range map[string]int{
		"": 20, "x": 20, "0": 20, "-1": 20, "7": 7, " 7 ": 7, "200": 200, "201": 200, "100000": 200,
	} {
		req := httptest.NewRequest("GET", "/search?"+url.Values{"limit": {raw}}.Encode(), nil)
		if got := limitParam(req, 20, 200); got != want {
			t.Errorf("limit %q: got %d, want %d", raw, got, want)
		}
	}
}
