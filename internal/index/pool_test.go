package index

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A burst of concurrent reads stays within maxOpenConns connections: without
// the bound each query opened its own, and 14,792 watcher queries at once
// took 27 GB (BUG-123).
func TestIndex_ConcurrentQueriesShareABoundedPool(t *testing.T) {
	idx := openTest(t)
	for n := range 50 {
		upsert(t, idx, fmt.Sprintf("p/n%02d.md", n), "N", "# N")
	}

	var peak atomic.Int64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			if open := int64(idx.db.Stats().OpenConnections); open > peak.Load() {
				peak.Store(open)
			}
			select {
			case <-stop:
				return
			case <-time.After(100 * time.Microsecond):
			}
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for n := range 400 {
		wg.Go(func() {
			if _, err := idx.NotesByPrefix(fmt.Sprintf("p/gone%03d.png", n)); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(stop)
	<-sampled
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := idx.db.Stats().MaxOpenConnections; got != maxOpenConns {
		t.Errorf("MaxOpenConnections = %d, want %d", got, maxOpenConns)
	}
	if p := peak.Load(); p > maxOpenConns {
		t.Errorf("%d connections open at once, want at most %d", p, maxOpenConns)
	}
}
