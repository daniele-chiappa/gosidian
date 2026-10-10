package main

import (
	"flag"
	"slices"
	"strings"
	"testing"
)

func TestParseArgs_PositionalsAnywhere(t *testing.T) {
	for _, args := range [][]string{
		{"fake", "--vault", "/vault"},
		{"--vault", "/vault", "fake"},
	} {
		fs := flag.NewFlagSet("secret set", flag.ContinueOnError)
		vault := fs.String("vault", "", "")
		if pos := parseArgs(fs, args); !slices.Equal(pos, []string{"fake"}) || *vault != "/vault" {
			t.Errorf("%q: positionals %q, vault %q", args, pos, *vault)
		}
	}
}

func TestReadSecretValue(t *testing.T) {
	for in, want := range map[string]string{"key\n": "key", "key\r\nmore\n": "key", "key": "key"} {
		if got, err := readSecretValue(strings.NewReader(in)); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := readSecretValue(strings.NewReader("\n")); err == nil {
		t.Error("an empty value is accepted")
	}
}
