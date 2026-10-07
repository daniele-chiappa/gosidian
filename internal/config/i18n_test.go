package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/i18n"
)

func TestI18n_DefaultEveryLanguage(t *testing.T) {
	c := Default()
	if c.I18n.DefaultLang != "en" || !slices.Equal(c.I18n.EnabledLangs, i18n.Languages()) {
		t.Errorf("default i18n = %+v, want en and every language (%v)", c.I18n, i18n.Languages())
	}
}

func TestI18n_EnvEnabledLangs(t *testing.T) {
	t.Setenv("GOSIDIAN_I18N_ENABLED_LANGS", " IT, en ,it,")
	c := Default()
	if err := c.ApplyEnv(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.I18n.EnabledLangs, ","); got != "it,en" {
		t.Errorf("enabled_langs from env = %s, want it,en", got)
	}
}

func TestI18n_Effective(t *testing.T) {
	all := strings.Join(i18n.Languages(), ",")
	for _, c := range []struct {
		name          string
		in            I18nConfig
		lang, enabled string
		warns         int
	}{
		{"kept as is", I18nConfig{DefaultLang: "it", EnabledLangs: []string{"it", "en"}}, "it", "it,en", 0},
		{"normalized", I18nConfig{DefaultLang: " EN ", EnabledLangs: []string{"En", " it", "en"}}, "en", "en,it", 0},
		{"unknown code dropped", I18nConfig{DefaultLang: "en", EnabledLangs: []string{"en", "xx"}}, "en", "en", 1},
		{"empty means every language", I18nConfig{DefaultLang: "en"}, "en", all, 0},
		{"only unknown codes", I18nConfig{DefaultLang: "en", EnabledLangs: []string{"xx"}}, "en", all, 1},
		{"default added to the list", I18nConfig{DefaultLang: "de", EnabledLangs: []string{"it", "en"}}, "de", "de,it,en", 1},
		{"unknown default", I18nConfig{DefaultLang: "xx", EnabledLangs: []string{"it", "en"}}, "en", "it,en", 1},
	} {
		got, warns := c.in.Effective()
		if got.DefaultLang != c.lang || strings.Join(got.EnabledLangs, ",") != c.enabled || len(warns) != c.warns {
			t.Errorf("%s: Effective() = %+v, %q; want %s, %s, %d warnings", c.name, got, warns, c.lang, c.enabled, c.warns)
		}
	}
}

func TestI18n_Validate(t *testing.T) {
	for _, c := range []struct {
		in   I18nConfig
		want string // substring of the error, "" for none
	}{
		{I18nConfig{DefaultLang: "it", EnabledLangs: []string{"it", "en"}}, ""},
		{I18nConfig{DefaultLang: "it"}, "cannot be empty"},
		{I18nConfig{DefaultLang: "it", EnabledLangs: []string{"it", "xx"}}, `no catalogue for "xx"`},
		{I18nConfig{DefaultLang: "xx", EnabledLangs: []string{"it"}}, `i18n.default_lang: no catalogue for "xx"`},
		{I18nConfig{DefaultLang: "de", EnabledLangs: []string{"it", "en"}}, "not among the enabled languages"},
	} {
		err := c.in.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%+v: unexpected error %v", c.in, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%+v: error %v, want one containing %q", c.in, err, c.want)
		}
	}
}
