package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A search whose hits are all partial counts apart from one with full hits:
// the rate of searches with no note holding every word stays the evidence
// ADR-007 asks for.
func TestCountSearch_Results(t *testing.T) {
	for _, c := range []struct {
		hits, partial int
		want          string
	}{
		{0, 0, "zero"},
		{3, 0, "hits"},
		{3, 2, "hits"},
		{2, 2, "partial"},
	} {
		before := testutil.ToFloat64(SearchQueries.WithLabelValues("test", c.want))
		CountSearch("test", c.hits, c.partial)
		if got := testutil.ToFloat64(SearchQueries.WithLabelValues("test", c.want)) - before; got != 1 {
			t.Errorf("%d hits, %d partial: result %q counted %v times", c.hits, c.partial, c.want, got)
		}
	}
}
