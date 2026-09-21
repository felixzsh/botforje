package whatsapp

import (
	"context"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
)

const (
	historyInitialWait = 30 * time.Second
	// historyIdleWait tolerates the gap between chunks of a large transfer;
	// WhatsApp pauses well over a few seconds between full-sync chunks.
	historyIdleWait = 60 * time.Second
	historyPoll     = 250 * time.Millisecond
	// historyFullWait bounds a requested full sync, which can outlast the QR
	// session; historyPlainWait bounds the default window.
	historyFullWait  = 10 * time.Minute
	historyPlainWait = 3 * time.Minute
)

// historyWaiter tracks history activity without blocking the whatsmeow event
// handler: a stream delivering chunks stops after an idle gap, one that never
// starts stops after the initial wait, and the transfer that ends the wait
// stops it at progress 100.
type historyWaiter struct {
	seen         atomic.Bool
	lastActivity atomic.Int64
	// reached is the progress of the transfer that ends this wait.
	reached atomic.Uint32
	// wantsFull selects the chunk type that ends the wait early: the full sync
	// was asked for, or the default recent window was not.
	wantsFull bool
}

func newHistoryWaiter(wantsFull bool) *historyWaiter {
	return &historyWaiter{wantsFull: wantsFull}
}

// progress records one delivered chunk. Only the requested type ends the wait
// early: a bootstrap chunk at 100 says nothing about the transfer after it.
func (w *historyWaiter) progress(
	syncType waHistorySync.HistorySync_HistorySyncType, progress uint32,
) {
	w.lastActivity.Store(time.Now().UnixNano())
	w.seen.Store(true)
	switch {
	case w.wantsFull && syncType == waHistorySync.HistorySync_FULL:
		w.reached.Store(progress)
	case !w.wantsFull && syncType == waHistorySync.HistorySync_RECENT:
		// The recent window is the last transfer of a plain registration; the
		// bootstrap before it must not end the wait early.
		w.reached.Store(progress)
	}
}

func (w *historyWaiter) finished() bool {
	return w.reached.Load() >= 100
}

func (w *historyWaiter) shouldStop(start, max time.Time) bool {
	now := time.Now()
	if now.After(max) || w.finished() {
		return true
	}
	if w.seen.Load() {
		last := time.Unix(0, w.lastActivity.Load())
		return now.Sub(last) >= historyIdleWait
	}
	return now.Sub(start) >= historyInitialWait
}

func (w *historyWaiter) wait(ctx context.Context, maxWait time.Duration) error {
	start := time.Now()
	max := start.Add(maxWait)
	ticker := time.NewTicker(historyPoll)
	defer ticker.Stop()
	for {
		if w.shouldStop(start, max) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
