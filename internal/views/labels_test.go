package views

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/dbschema"
)

// A column carries the schema's labels to the web UI (IMP-220, F-23): the
// field's name in every language or per language, its options' per
// language.
func TestColumn_Labels(t *testing.T) {
	s, err := dbschema.Parse("p/x.md", "type: database\nsource: p/x\nfields:\n"+
		"  status:\n    type: select\n    options: [open]\n    label: {it: Stato}\n    option_labels: {it: {open: aperta}}\n"+
		"  due: {type: date, label: Scadenza}\n")
	if err != nil {
		t.Fatal(err)
	}
	r := &Result{Schema: s}
	b, err := json.Marshal([]Column{r.column("status"), r.column("due"), r.column("path")})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"status","type":"select","options":["open"],"labels":{"it":"Stato"},"option_labels":{"it":{"open":"aperta"}}},` +
		`{"name":"due","type":"date","label":"Scadenza"},{"name":"path"}]`
	if string(b) != want {
		t.Errorf("columns =\n%s\nwant\n%s", b, want)
	}
	f, _ := s.Field("status")
	if c := FieldColumn(f); c.Labels["it"] != "Stato" || c.OptionLabels["it"]["open"] != "aperta" || !strings.EqualFold(c.Type, "select") {
		t.Errorf("FieldColumn = %+v", c)
	}
}
