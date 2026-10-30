package loadtest

// Open-loop traffic generation and correlated completion accounting for the
// opt-in capacity run. The generator never waits for a reply before offering
// the next input, so a slow runtime cannot quietly reduce the requested load.

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// traffic is the driving state of one run: per-bot schedules, pending inputs,
// and completed latencies. Only pending inputs are retained, so memory tracks
// in-flight work, not the whole history of a long run.
type traffic struct {
	params  params
	bots    []*botTraffic
	payload string
	epoch   int
}

type botTraffic struct {
	mu        sync.Mutex
	inFlight  map[string]time.Time
	nextChat  int
	offered   int
	completed int
	duplicate int
	refused   int
	skipped   int
	latencies []time.Duration
}

func newTraffic(l layout, p params) *traffic {
	tr := &traffic{params: p, payload: strings.Repeat("x", p.Payload)}
	for i := 0; i < l.Bots; i++ {
		tr.bots = append(tr.bots, &botTraffic{inFlight: map[string]time.Time{}})
	}
	return tr
}

// run offers traffic at the requested rate until duration elapses. Every bot
// gets its own schedule, so one slow bot never throttles its peers.
func (tr *traffic) run(ctx context.Context, h *harness, duration time.Duration) {
	tr.epoch++
	end := time.Now().Add(duration)
	var wg sync.WaitGroup
	for i := range tr.bots {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tr.drive(ctx, h, i, end)
		}(i)
	}
	wg.Wait()
}

// drive is one bot's open-loop schedule: it sleeps while ahead, counts the
// slots it could not offer when behind, and respects a bounded pending set.
func (tr *traffic) drive(ctx context.Context, h *harness, i int, end time.Time) {
	b := tr.bots[i]
	if tr.params.Rate <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / tr.params.Rate)
	if interval < time.Microsecond {
		interval = time.Microsecond
	}
	r := rand.New(rand.NewSource(tr.params.Seed + int64(i)))
	next := time.Now().Add(time.Duration(r.Int63n(int64(interval))))
	seq := 0
	for !time.Now().After(end) {
		now := time.Now()
		if wait := next.Sub(now); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			now = time.Now()
			if !now.Before(end) {
				return
			}
		}
		if behind := now.Sub(next); behind >= interval {
			lost := int(behind / interval)
			b.skippedSlots(lost)
			next = next.Add(time.Duration(lost) * interval)
		}
		next = next.Add(interval)
		id := fmt.Sprintf("%s-e%d-%d", h.layout.bot(i), tr.epoch, seq)
		seq++
		if !b.offer(id, tr.params.MaxPending) {
			continue
		}
		offerCtx, cancel := context.WithTimeout(ctx, tr.params.InjectTimeout)
		body := id + "|" + tr.payload
		err := h.injectCtx(offerCtx, i, b.takeChat(tr.params.Chats), body)
		cancel()
		if err != nil {
			b.refuse(id)
		}
	}
}

// observed records one delivered reply: it completes the pending input with the
// same id, and counts a second delivery of the same id as a duplicate.
func (tr *traffic) observed(bot int, body string) {
	id := body
	if before, _, found := strings.Cut(body, "|"); found {
		id = before
	}
	tr.bots[bot].complete(id)
}

// drain waits until no input is pending and the fleet's queues are empty, or
// the deadline expires; it returns how many inputs never completed.
func (tr *traffic) drain(h *harness, wait time.Duration) int {
	end := time.Now().Add(wait)
	for {
		if tr.pending() == 0 && h.settled() {
			return 0
		}
		if time.Now().After(end) {
			return tr.pending()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// reset starts a fresh measurement window; a warm-up phase is not part of the
// reported result.
func (tr *traffic) reset() {
	for _, b := range tr.bots {
		b.mu.Lock()
		b.inFlight = map[string]time.Time{}
		b.offered, b.completed, b.duplicate = 0, 0, 0
		b.refused, b.skipped, b.latencies = 0, 0, nil
		b.mu.Unlock()
	}
}

func (tr *traffic) pending() int {
	pending := 0
	for _, b := range tr.bots {
		pending += b.pending()
	}
	return pending
}

func (tr *traffic) latencies() []time.Duration {
	var all []time.Duration
	for _, b := range tr.bots {
		b.mu.Lock()
		all = append(all, b.latencies...)
		b.mu.Unlock()
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all
}

func (tr *traffic) totals() counters {
	var c counters
	for _, b := range tr.bots {
		b.mu.Lock()
		c.Offered += b.offered
		c.Completed += b.completed
		c.Duplicates += b.duplicate
		c.Refused += b.refused
		c.Skipped += b.skipped
		b.mu.Unlock()
	}
	c.Unfinished = tr.pending()
	return c
}

func (b *botTraffic) pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.inFlight)
}

func (b *botTraffic) offer(id string, maxPending int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.inFlight) >= maxPending {
		b.refused++
		return false
	}
	b.inFlight[id] = time.Now()
	b.offered++
	return true
}

func (b *botTraffic) complete(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	offeredAt, ok := b.inFlight[id]
	if !ok {
		b.duplicate++
		return
	}
	delete(b.inFlight, id)
	b.completed++
	b.latencies = append(b.latencies, time.Since(offeredAt))
}

func (b *botTraffic) refuse(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inFlight, id)
	b.refused++
}

func (b *botTraffic) skippedSlots(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.skipped += n
}

func (b *botTraffic) takeChat(chats int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	chat := b.nextChat % chats
	b.nextChat++
	return chat
}
