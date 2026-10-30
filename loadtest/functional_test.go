package loadtest

// Multi-bot integration scenarios. Each assertion is correlated with the
// exact input that produced it; no test accepts an aggregate count alone.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentBotsReplyToTheirOwnConversations drives several bots at once
// and requires exactly one reply per input, in the channel that owns the
// conversation. It runs below the write-contention regime the capacity run
// explores, so exactly-once here is a correctness check, not a promise at
// saturation.
func TestConcurrentBotsReplyToTheirOwnConversations(t *testing.T) {
	l := layout{Scopes: 2, Bots: 12, Chats: 3}
	h := newHarness(t, l)
	defer h.stop()

	const turns = 2
	inputs := l.Bots * l.Chats * turns
	expected := make([]map[string]int, l.Bots)
	for i := range expected {
		expected[i] = map[string]int{}
		for chat := 0; chat < l.Chats; chat++ {
			for turn := 0; turn < turns; turn++ {
				expected[i][inputID(l, i, chat, turn)]++
			}
		}
	}

	var wg sync.WaitGroup
	failures := make(chan error, l.Bots)
	for i := 0; i < l.Bots; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for chat := 0; chat < l.Chats; chat++ {
				for turn := 0; turn < turns; turn++ {
					ctx, cancel := context.WithTimeout(
						context.Background(), h.deadline())
					err := h.injectCtx(ctx, i, chat, inputID(l, i, chat, turn))
					cancel()
					if err != nil {
						failures <- fmt.Errorf("%s chat %d: %w", l.bot(i), chat, err)
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("drive the fleet: %v", err)
	}

	h.waitFor("every input to settle", func() bool {
		return h.settled() && h.totalSent() >= inputs
	})
	if h.errorCount() != 0 {
		t.Fatalf("small fleet reported %d failures: %v", h.errorCount(), h.errors())
	}
	for i := 0; i < l.Bots; i++ {
		got := countBodies(sentBodies(h.channel(i)))
		if len(got) != len(expected[i]) {
			t.Fatalf("%s answered %d distinct inputs, want %d", l.bot(i),
				len(got), len(expected[i]))
		}
		for id, want := range expected[i] {
			if got[id] != want {
				t.Fatalf("%s replied %d times to %s, want %d", l.bot(i),
					got[id], id, want)
			}
		}
	}
}

// TestBurstKeepsTheDeliveryContract drives more bots than the database can
// commit concurrently and asserts the contract the runtime actually offers:
// every reply belongs to the bot that received the input, nothing is lost
// without a reported failure, and a duplicate resend only follows such a
// failure. A turn whose reply was delivered but whose provider id could not be
// recorded is retried, so at-least-once is the honest expectation here.
func TestBurstKeepsTheDeliveryContract(t *testing.T) {
	l := layout{Scopes: 2, Bots: 48, Chats: 3}
	h := newHarness(t, l)
	defer h.stop()

	const turns = 2
	inputs := l.Bots * l.Chats * turns
	owned := make([]map[string]bool, l.Bots)
	for i := range owned {
		owned[i] = map[string]bool{}
		for chat := 0; chat < l.Chats; chat++ {
			for turn := 0; turn < turns; turn++ {
				owned[i][inputID(l, i, chat, turn)] = true
			}
		}
	}
	for i := 0; i < l.Bots; i++ {
		for chat := 0; chat < l.Chats; chat++ {
			for turn := 0; turn < turns; turn++ {
				h.inject(i, chat, inputID(l, i, chat, turn))
			}
		}
	}

	h.waitFor("the burst to settle", func() bool {
		return h.settled() && h.totalSent()+h.errorCount() >= inputs
	})

	duplicates := 0
	for i := 0; i < l.Bots; i++ {
		for id, times := range countBodies(sentBodies(h.channel(i))) {
			if !owned[i][id] {
				t.Fatalf("%s replied to %s, which it never received", l.bot(i), id)
			}
			if times > 1 {
				duplicates += times - 1
			}
		}
	}
	if settled := h.totalSent() + h.errorCount(); settled < inputs {
		t.Fatalf("accounted for %d of %d inputs: work was lost silently",
			settled, inputs)
	}
	if duplicates > 0 && h.errorCount() == 0 {
		t.Fatalf("%d duplicate replies with no reported failure", duplicates)
	}
}

// TestRedeliveryRepliesOnce proves a repeated provider message id is processed
// as a duplicate turn: its outcome is recorded and no second reply is queued.
func TestRedeliveryRepliesOnce(t *testing.T) {
	l := layout{Scopes: 1, Bots: 2, Chats: 1}
	h := newHarness(t, l)
	defer h.stop()

	const id = "dup-1"
	h.inject(0, 0, id)
	channel := h.channel(0)
	h.waitFor("the first reply", func() bool { return channel.SentCount() == 1 })

	before := channel.ReceiptCount()
	h.inject(0, 0, id)
	h.waitFor("the duplicate's processing outcome", func() bool {
		return channel.ReceiptCount() > before
	})
	if got := channel.SentCount(); got != 1 {
		t.Fatalf("replies after redelivery = %d, want only the first", got)
	}
}

// TestTwoBotsShareOneMessageSpaceWithoutColliding proves identity and dedup
// stay per bot even when the adapter declares one shared message space.
func TestTwoBotsShareOneMessageSpaceWithoutColliding(t *testing.T) {
	l := layout{Scopes: 1, Bots: 2, Chats: 1, Shared: true}
	h := newHarness(t, l)
	defer h.stop()

	const id = "shared-id"
	h.inject(0, 0, id)
	h.inject(1, 0, id)
	h.waitFor("both replies", func() bool {
		return h.channel(0).SentCount() == 1 && h.channel(1).SentCount() == 1
	})
	for i := 0; i < l.Bots; i++ {
		if got := sentBodies(h.channel(i)); len(got) != 1 || got[0] != id {
			t.Fatalf("%s replies = %q, want the one it received", l.bot(i), got)
		}
	}
}

// TestShutdownUnderLoadStopsTheFleet cancels a fleet that is still receiving
// traffic and requires a bounded shutdown with no sender left behind.
func TestShutdownUnderLoadStopsTheFleet(t *testing.T) {
	l := layout{Scopes: 1, Bots: 16, Chats: 1}
	h := newHarness(t, l)

	injectCtx, cancelInjection := context.WithCancel(context.Background())
	driven := make(chan error, 1)
	go func() {
		defer close(driven)
		for n := 0; ; n++ {
			if injectCtx.Err() != nil {
				return
			}
			if err := h.injectCtx(injectCtx, 0, 0, fmt.Sprintf("load-%d", n)); err != nil {
				if injectCtx.Err() == nil {
					driven <- err
				}
				return
			}
		}
	}()

	h.waitFor("work to start flowing", func() bool {
		return h.channel(0).SentCount() > 0
	})
	cancelInjection()
	h.stop()
	select {
	case err := <-driven:
		if err != nil {
			t.Fatalf("drive the fleet: %v", err)
		}
	default:
		t.Fatal("the injector did not exit when the fleet stopped")
	}
	for _, status := range h.fleet.Status() {
		if status.State != "stopped" {
			t.Fatalf("%s/%s state after shutdown = %q, want stopped",
				status.Scope, status.Bot, status.State)
		}
	}
}

// TestRestartKeepsEveryBotsConversation proves committed work survives a
// process restart for every bot, not only the first, and a restarted fleet
// still answers.
func TestRestartKeepsEveryBotsConversation(t *testing.T) {
	l := layout{Scopes: 2, Bots: 6, Chats: 2}
	path := filepath.Join(t.TempDir(), "load.db")

	first := newHarnessAt(t, l, path)
	for i := 0; i < l.Bots; i++ {
		for chat := 0; chat < l.Chats; chat++ {
			first.inject(i, chat, inputID(l, i, chat, 0))
		}
	}
	first.waitFor("the first run's replies", func() bool {
		for i := 0; i < l.Bots; i++ {
			if first.channel(i).SentCount() < l.Chats {
				return false
			}
		}
		return true
	})
	first.stop()
	if err := first.db.Close(); err != nil {
		t.Fatalf("close the first database: %v", err)
	}

	second := newHarnessAt(t, l, path)
	defer second.stop()
	ctx := context.Background()
	for i := 0; i < l.Bots; i++ {
		for chat := 0; chat < l.Chats; chat++ {
			if _, err := second.db.Chats().ForTarget(ctx,
				l.scopeName(l.scopeOf(i)), l.bot(i), l.target(chat)); err != nil {
				t.Fatalf("%s chat %d lost across restart: %v", l.bot(i), chat, err)
			}
		}
	}
	const after = "after-restart"
	second.inject(0, 0, after)
	second.waitFor("the restarted fleet's reply", func() bool {
		return second.channel(0).SentCount() >= 1
	})
	if got := sentBodies(second.channel(0)); got[len(got)-1] != after {
		t.Fatalf("restarted replies = %q, want %q last", got, after)
	}
}

func inputID(l layout, i, chat, turn int) string {
	return fmt.Sprintf("%s-c%d-t%d", l.bot(i), chat, turn)
}
