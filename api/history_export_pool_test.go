package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// stallingHistory keeps one history read open until the test releases it,
// which is what a slow export client does to the connection its read holds.
type stallingHistory struct {
	fakeHistory
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (b *stallingHistory) ReadHistory(
	ctx context.Context, _, _ string, _ botflota.HistorySink,
) error {
	n := b.active.Add(1)
	defer b.active.Add(-1)
	for {
		peak := b.peak.Load()
		if n <= peak || b.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	b.started <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil
}

// TestStalledExportsNeverHoldTheWholePool proves a slow export client cannot
// open snapshot after snapshot. Every export holds one pooled connection until
// the client finishes receiving the archive, so unbounded concurrency let a
// handful of stalled responses exhaust the connection pool and fail unrelated
// requests. One export at a time leaves the fleet's writers their connections.
func TestStalledExportsNeverHoldTheWholePool(t *testing.T) {
	history := &stallingHistory{
		started: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
	server := newHistoryServer(t, history)
	var once sync.Once
	release := func() { once.Do(func() { close(history.release) }) }
	defer release()

	const stalled = 8
	var wg sync.WaitGroup
	for i := 0; i < stalled; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := httptest.NewRequest(http.MethodGet,
				"/api/v1/scopes/support/bots/demo/history/export", nil)
			request.Header.Set("Authorization", "Bearer "+testOwnerToken)
			server.Handler().ServeHTTP(httptest.NewRecorder(), request)
		}()
	}

	<-history.started
	select {
	case <-history.started:
		t.Fatalf("a second export opened while the first was stalled "+
			"(concurrent reads = %d)", history.peak.Load())
	case <-time.After(200 * time.Millisecond):
	}
	if got := history.peak.Load(); got != 1 {
		t.Fatalf("concurrent history reads = %d, want 1", got)
	}
	release()
	wg.Wait()
}
