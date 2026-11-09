package i18n

import "testing"

func TestText(t *testing.T) {
	if got := Text("board", "it", "board.review.closed_on", map[string]string{"date": "2026-11-23"}); got != "Chiusa il 2026-11-23." {
		t.Fatalf("it: %q", got)
	}
	if got := Text("board", "xx", "board.review.title", nil); got != "Review" {
		t.Fatalf("a language with no catalogue gets English: %q", got)
	}
	if got := Text("board", "it", "board.review.nothing", nil); got != "board.review.nothing" {
		t.Fatalf("a missing key: %q", got)
	}
	defer SetLang(Lang())
	SetLang("de")
	SetLang("xx")
	if Lang() != "de" {
		t.Fatalf("an unknown language was set: %s", Lang())
	}
}

func TestDecimal(t *testing.T) {
	for _, c := range []struct {
		lang string
		f    float64
		want string
	}{{"en", 2.5, "2.5"}, {"it", 2.5, "2,5"}, {"it", 3, "3"}, {"fr", 6.44, "6,4"}, {"de", 0.05, "0,1"}} {
		if got := Decimal(c.lang, c.f); got != c.want {
			t.Errorf("Decimal(%s, %v) = %q, want %q", c.lang, c.f, got, c.want)
		}
	}
}
