package i18n

import "testing"

func TestCatalog_LoadAndT(t *testing.T) {
	c, err := Load("it")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has("it") {
		t.Errorf("expected it catalog loaded")
	}
	if !c.Has("en") {
		t.Errorf("expected en catalog loaded")
	}
	cases := []struct {
		lang, key, want string
	}{
		{"it", "tokens.create_button", "Crea token"},
		{"en", "tokens.create_button", "Create token"},
		{"it", "common.revoke", "Revoca"},
		{"en", "common.revoke", "Revoke"},
		{"it", "users.create_invite_button", "Genera invite"},
		{"it", "does.not.exist", "does.not.exist"}, // literal fallback
	}
	for _, c2 := range cases {
		got := c.T(c2.lang, c2.key)
		if got != c2.want {
			t.Errorf("T(%s, %s) = %q, want %q", c2.lang, c2.key, got, c2.want)
		}
	}
}

func TestCatalog_PrimaryTag(t *testing.T) {
	for in, want := range map[string]string{
		"it":               "it",
		"it-IT":            "it",
		"en-US,en;q=0.5":   "en",
		"IT,en;q=0.5":      "it",
		"":                 "",
	} {
		if got := primaryTag(in); got != want {
			t.Errorf("primaryTag(%q)=%q, want %q", in, got, want)
		}
	}
}
