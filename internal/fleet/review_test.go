package fleet

import "testing"

func TestReviewOf(t *testing.T) {
	for _, c := range []struct {
		name, report string
		want         Verdict
	}{
		{"none", "Looks fine to me.", Verdict{}},
		{"passed", "## Giudizio\n\nTutto a posto.\n\n- [minor] riga 12: una virgola\n\nVERDICT: passed\n", Verdict{"passed", 0, 1}},
		{"to correct", "1. [serious] settimana 5: livello e serie insieme\n* **[Minor]** titolo\n- [serious] fonte sbagliata\n\n**VERDICT:** to correct", Verdict{"to correct", 2, 1}},
		{"the last verdict counts", "VERDICT: passed\n\nOn second thought:\n- [serious] x\nVERDICT: to correct\n", Verdict{"to correct", 1, 0}},
		{"in a sentence is no line", "I would say VERDICT: passed here, and [serious] is a word.", Verdict{}},
		{"bold and quoted", "> **VERDICT: Passed**", Verdict{"passed", 0, 0}},
	} {
		if got := ReviewOf(c.report); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestParse_ReviewAndReopenOn(t *testing.T) {
	block := "coordinator: capo\nreopen_on: any\nroles:\n  capo:\n    run: x.run\n    delegates_to: [rev]\n  rev:\n    run: x.run\n    review: true\n"
	team, problems := Parse(block, "p/squadra.md")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if r, _ := team.Role("rev"); !r.Review || team.ReopenOn != ReopenAny {
		t.Fatalf("%+v %q", r, team.ReopenOn)
	}
	team, problems = Parse("roles:\n  a:\n    run: x.run\n", "p/squadra.md")
	if len(problems) > 0 || team.ReopenOn != ReopenSerious {
		t.Fatalf("the default: %q %v", team.ReopenOn, problems)
	}
	if _, problems = Parse("reopen_on: never\nroles:\n  a:\n    run: x.run\n", "p/squadra.md"); len(problems) != 1 {
		t.Fatalf("reopen_on: never: %v", problems)
	}
}
