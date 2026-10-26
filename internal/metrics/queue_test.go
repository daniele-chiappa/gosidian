package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestQueueGauges: the gauges of the queue come from its source at each
// scrape, every step under "all"; no source, no gauges.
func TestQueueGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(queueCollector{})
	SetQueueSource(func() []QueueSample {
		return []QueueSample{{Queued: 3, Running: 2, OldestWait: 40}, {Resource: "plugin:ocr", Queued: 2, Running: 1, OldestWait: 40}}
	})
	t.Cleanup(func() { SetQueueSource(nil) })
	want := `
# HELP gosidian_pipeline_steps_queued Steps of the pipelines queued whose time has come, by resource (plugin:<name>, llm:<provider>, res:<name>; all: every step).
# TYPE gosidian_pipeline_steps_queued gauge
gosidian_pipeline_steps_queued{resource="all"} 3
gosidian_pipeline_steps_queued{resource="plugin:ocr"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "gosidian_pipeline_steps_queued"); err != nil {
		t.Fatal(err)
	}
	if n := testutil.CollectAndCount(queueCollector{}); n != 6 {
		t.Fatalf("%d gauges", n)
	}
	SetQueueSource(nil)
	if n := testutil.CollectAndCount(queueCollector{}); n != 0 {
		t.Fatalf("%d gauges without a source", n)
	}
}
