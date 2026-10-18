package fleet

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// replayChannel is a hand-written replay-capable fake: it confirms each
// delivered entry only through ConfirmReceive, after the fleet settles
// the agreed processing outcome. Nothing in the handoff path confirms.
// The sequence fixes the source's cursor order, so advanced reports the
// longest contiguous confirmed prefix: a cumulative cursor that never
// passes a missing (failed) entry.
type replayChannel struct {
	incoming chan botflota.IncomingMessage

	mu       sync.Mutex
	receipts []botflota.ReceiveReceipt
	sequence []string
}

func newReplayChannel(sequence ...string) *replayChannel {
	return &replayChannel{
		incoming: make(chan botflota.IncomingMessage, 16),
		sequence: sequence,
	}
}

func (c *replayChannel) Connect(context.Context) error { return nil }

func (c *replayChannel) Disconnect(context.Context) error { return nil }

func (c *replayChannel) Send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	return "replay-" + botflota.ContentText(msg.Content), nil
}

func (c *replayChannel) Incoming() <-chan botflota.IncomingMessage {
	return c.incoming
}

func (c *replayChannel) ConfirmReceive(
	_ context.Context, receipt botflota.ReceiveReceipt,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.receipts = append(c.receipts, receipt)
	return nil
}

func (c *replayChannel) confirmed(cursor string) (botflota.ReceiveOutcome, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, receipt := range c.receipts {
		if receipt.Cursor == cursor {
			return receipt.Outcome, true
		}
	}
	return "", false
}

func (c *replayChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.receipts)
}

// advanced reports the longest contiguous confirmed prefix of the
// sequence: a later success never advances it past a failed entry whose
// confirmation is still missing.
func (c *replayChannel) advanced() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	confirmed := map[string]bool{}
	for _, receipt := range c.receipts {
		confirmed[receipt.Cursor] = true
	}
	advanced := ""
	for _, cursor := range c.sequence {
		if !confirmed[cursor] {
			break
		}
		advanced = cursor
	}
	return advanced
}

// flakyTurn fails the turn commit or the held record on demand, so a
// failure stays replayable until the test heals it.
type flakyTurn struct {
	botflota.TurnStore
	commitErr error
	recordErr error
}

func (f *flakyTurn) CommitTurn(
	ctx context.Context, turn botflota.TurnRecord,
) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	return f.TurnStore.CommitTurn(ctx, turn)
}

func (f *flakyTurn) Record(
	ctx context.Context, message *botflota.Message, at time.Time,
) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	return f.TurnStore.Record(ctx, message, at)
}

func buildReplayFleet(
	t *testing.T, turn botflota.TurnStore,
) (*Fleet, map[string]*replayChannel, *memStore) {
	t.Helper()
	channels := map[string]*replayChannel{}
	store := newStore()
	if turn == nil {
		turn = store
	}
	runner, err := New(loadFleetConfig(t), Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: turn, Outbox: store,
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Catalog: fakeCatalog(
			func(_, bot string, _ any) (botflota.MessageChannel, error) {
				channel := newReplayChannel("upd-1", "upd-2", "upd-3")
				channels[bot] = channel
				return channel, nil
			}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, channels, store
}

func replayBot(t *testing.T, runner *Fleet, bot string) *Bot {
	t.Helper()
	found, ok := runner.bots[botflota.BotRef{Scope: "dev", Bot: bot}]
	if !ok {
		t.Fatalf("no bot dev/%s", bot)
	}
	return found
}

func replayMsg(
	bot, sender, cursor, id string,
) botflota.IncomingMessage {
	return botflota.IncomingMessage{
		Bot: bot, Sender: waKey(sender), Target: waTarget(sender),
		Content:           botflota.TextContent{Body: "hi"},
		ProviderMessageID: id, ReceiveCursor: cursor, At: t0,
	}
}

func storedCopies(store *memStore, providerID string) int {
	copies := 0
	for _, message := range store.messages {
		if message.ProviderMessageID == providerID {
			copies++
		}
	}
	return copies
}

// TestReplayConfirmsOnlyAfterTheAgreedOutcome covers the receive
// completion contract end to end (ADR 0004, ADR 0040): a committed turn,
// a persisted duplicate, and a deliberate discard each confirm with
// their own outcome, while a failed persistence confirms nothing and
// stays replayable.
func TestReplayConfirmsOnlyAfterTheAgreedOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		bot  string
		// sender and id identify the delivered entry; redeliver repeats
		// the same provider id under a fresh cursor, the way a source
		// replays an unconfirmed entry.
		sender string
		id     string
		hold   bool
		// failFirst fails the first delivery's commit, then heals the
		// store before the redelivery.
		failFirst bool
		redeliver bool
		want      []botflota.ReceiveReceipt
		// wantStored counts the provider id's stored copies afterwards.
		wantStored int
		// wantAdvanced is the fake's cumulative cursor afterwards.
		wantAdvanced string
	}{
		{
			name: "committed turn", bot: "alpha",
			sender: "replay-a", id: "m-a",
			want: []botflota.ReceiveReceipt{
				{Cursor: "upd-1", Outcome: botflota.ReceiveCommitted},
			},
			wantStored: 1, wantAdvanced: "upd-1",
		},
		{
			name: "recorded duplicate", bot: "alpha",
			sender: "replay-d", id: "m-d", redeliver: true,
			want: []botflota.ReceiveReceipt{
				{Cursor: "upd-1", Outcome: botflota.ReceiveCommitted},
				{Cursor: "upd-2", Outcome: botflota.ReceiveAlreadyRecorded},
			},
			wantStored: 1, wantAdvanced: "upd-2",
		},
		{
			name: "held inbound", bot: "alpha",
			sender: "replay-h", id: "m-h", hold: true,
			want: []botflota.ReceiveReceipt{
				{Cursor: "upd-1", Outcome: botflota.ReceiveDiscarded},
			},
			wantStored: 1, wantAdvanced: "upd-1",
		},
		{
			name: "filtered message", bot: "beta",
			sender: "999", id: "m-f",
			want: []botflota.ReceiveReceipt{
				{Cursor: "upd-1", Outcome: botflota.ReceiveDiscarded},
			},
			wantStored: 0, wantAdvanced: "upd-1",
		},
		{
			name: "failed persistence", bot: "alpha",
			sender: "replay-x", id: "m-x", failFirst: true, redeliver: true,
			want: []botflota.ReceiveReceipt{
				{Cursor: "upd-2", Outcome: botflota.ReceiveCommitted},
			},
			wantStored: 1, wantAdvanced: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			flaky := &flakyTurn{}
			var store *memStore
			var runner *Fleet
			var channels map[string]*replayChannel
			if tc.failFirst {
				flaky.commitErr = errors.New("replay: commit fails")
				runner, channels, store = buildReplayFleet(t, flaky)
				flaky.TurnStore = store
			} else {
				runner, channels, store = buildReplayFleet(t, nil)
			}
			bot := replayBot(t, runner, tc.bot)
			if tc.hold {
				bot.setHuman(waTarget(tc.sender), true)
			}
			deliver := func(cursor string) {
				bot.handleMessage(ctx, replayMsg(tc.bot, tc.sender, cursor, tc.id))
			}
			deliver("upd-1")
			if tc.failFirst {
				if n := channels[tc.bot].count(); n != 0 {
					t.Fatalf("receipts after a failed commit = %d, "+
						"want none", n)
				}
				if n := storedCopies(store, tc.id); n != 0 {
					t.Fatalf("stored copies after a failed commit = %d, "+
						"want none", n)
				}
				flaky.commitErr = nil
			}
			if tc.redeliver {
				deliver("upd-2")
			}
			channel := channels[tc.bot]
			if n := channel.count(); n != len(tc.want) {
				t.Fatalf("receipts = %d, want %d", n, len(tc.want))
			}
			for i, want := range tc.want {
				outcome, ok := channel.confirmed(want.Cursor)
				if !ok {
					t.Fatalf("cursor %q was never confirmed", want.Cursor)
				}
				if outcome == botflota.ReceiveFailed {
					t.Fatalf("cursor %q confirmed as failed; a failure "+
						"never confirms", want.Cursor)
				}
				if outcome != want.Outcome {
					t.Fatalf("receipt %d = %q, want %q", i, outcome,
						want.Outcome)
				}
			}
			if n := storedCopies(store, tc.id); n != tc.wantStored {
				t.Fatalf("stored copies of %s = %d, want %d", tc.id, n,
					tc.wantStored)
			}
			if advanced := channel.advanced(); advanced != tc.wantAdvanced {
				t.Fatalf("cumulative cursor = %q, want %q", advanced,
					tc.wantAdvanced)
			}
			if tc.hold && len(store.sessions) != 0 {
				t.Fatal("a held inbound started a session")
			}
			if tc.bot == "beta" && tc.sender == "999" &&
				len(store.peers) != 0 {
				t.Fatal("a filtered message recorded a peer")
			}
		})
	}
}

// TestCumulativeCursorNeverPassesAFailedEntry replays three entries with
// the middle one failing: the later success confirms but the cumulative
// cursor holds until the failed entry is redelivered and committed.
func TestCumulativeCursorNeverPassesAFailedEntry(t *testing.T) {
	ctx := context.Background()
	flaky := &flakyTurn{}
	runner, channels, store := buildReplayFleet(t, flaky)
	flaky.TurnStore = store
	bot := replayBot(t, runner, "alpha")
	channel := channels["alpha"]

	bot.handleMessage(ctx, replayMsg("alpha", "cum-1", "upd-1", "m-1"))
	flaky.commitErr = errors.New("replay: commit fails")
	bot.handleMessage(ctx, replayMsg("alpha", "cum-2", "upd-2", "m-2"))
	flaky.commitErr = nil
	bot.handleMessage(ctx, replayMsg("alpha", "cum-3", "upd-3", "m-3"))

	if outcome, ok := channel.confirmed("upd-1"); !ok ||
		outcome != botflota.ReceiveCommitted {
		t.Fatalf("upd-1 = %q, %v; want committed", outcome, ok)
	}
	if _, ok := channel.confirmed("upd-2"); ok {
		t.Fatal("upd-2 was confirmed despite its failed commit")
	}
	if outcome, ok := channel.confirmed("upd-3"); !ok ||
		outcome != botflota.ReceiveCommitted {
		t.Fatalf("upd-3 = %q, %v; want committed", outcome, ok)
	}
	if advanced := channel.advanced(); advanced != "upd-1" {
		t.Fatalf("cumulative cursor = %q, want upd-1", advanced)
	}

	bot.handleMessage(ctx, replayMsg("alpha", "cum-2", "upd-2", "m-2"))
	if outcome, ok := channel.confirmed("upd-2"); !ok ||
		outcome != botflota.ReceiveCommitted {
		t.Fatalf("upd-2 after redelivery = %q, %v; want committed",
			outcome, ok)
	}
	if advanced := channel.advanced(); advanced != "upd-3" {
		t.Fatalf("cumulative cursor = %q, want upd-3", advanced)
	}
}

// TestReceiveCursorStaysOutOfMessageIdentity proves the opaque source
// cursor never leaks into message identity or the dedup key: the same
// provider id under a fresh cursor is still a duplicate, and the cursor
// itself never reads as a stored id (ADR 0026, ADR 0051).
func TestReceiveCursorStaysOutOfMessageIdentity(t *testing.T) {
	ctx := context.Background()
	runner, channels, store := buildReplayFleet(t, nil)
	bot := replayBot(t, runner, "alpha")

	bot.handleMessage(ctx, replayMsg("alpha", "id-1", "upd-9", "m-9"))
	bot.handleMessage(ctx, replayMsg("alpha", "id-1", "upd-10", "m-9"))

	if outcome, _ := channels["alpha"].confirmed("upd-10"); outcome !=
		botflota.ReceiveAlreadyRecorded {
		t.Fatalf("redelivery outcome = %q, want already_recorded", outcome)
	}
	if n := storedCopies(store, "m-9"); n != 1 {
		t.Fatalf("stored copies of m-9 = %d, want 1", n)
	}
	for _, cursor := range []string{"upd-9", "upd-10"} {
		seen, err := store.HasProviderID(ctx, "dev", "alpha", "", waTarget("id-1"),
			cursor)
		if err != nil {
			t.Fatalf("HasProviderID(%s): %v", cursor, err)
		}
		if seen {
			t.Fatalf("cursor %s reads as a stored provider id", cursor)
		}
	}
	if peer, err := store.Get(ctx, "dev", waKey("id-1")); err != nil ||
		peer.Key.Subject != "id-1" {
		t.Fatalf("peer = %+v, %v; want the sender subject", peer, err)
	}
	chat, err := store.ForTarget(ctx, "dev", "alpha", waTarget("id-1"))
	if err != nil || chat.Target != waTarget("id-1") {
		t.Fatalf("chat = %+v, %v; want the inbound target", chat, err)
	}
}

// TestSourceWithoutConfirmerKeepsBehavior proves a channel without the
// optional port is never called for confirmation and otherwise behaves
// exactly as before: the turn commits and its reply queues.
func TestSourceWithoutConfirmerKeepsBehavior(t *testing.T) {
	ctx := context.Background()
	runner, _, store := buildFleetWithStore(t, "", nil)
	bot := replayBot(t, runner, "alpha")

	bot.handleMessage(ctx, replayMsg("alpha", "plain-1", "upd-1", "m-plain"))

	if n := storedCopies(store, "m-plain"); n != 1 {
		t.Fatalf("stored copies = %d, want 1", n)
	}
	depth, err := store.Depth(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("Depth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("outbox depth = %d, want the queued reply", depth)
	}
}

// TestReceiveConfirmationBoundaries proves the two negative boundaries:
// an entry without a cursor settles nothing, and the real channel handoff
// alone settles nothing — only the agreed processing outcome does. Both
// entries run through Bot.run, which consumes the channel's inbound queue,
// and the commit stays gated so the assertion is not vacuous.
func TestReceiveConfirmationBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := &gatedTurn{
		bot: "alpha", gate: make(chan struct{}), entered: make(chan struct{}),
	}
	runner, channels, store := buildReplayFleet(t, gate)
	gate.memStore = store
	channel := channels["alpha"]

	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()

	channel.incoming <- replayMsg("alpha", "bare-2", "upd-1", "m-handoff")
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the run loop never handed the message to processing")
	}
	if n := channel.count(); n != 0 {
		t.Fatalf("receipts after handoff alone = %d, want none", n)
	}
	close(gate.gate)
	waitConfirmed(t, channel, "upd-1")

	// The cursorless entry settles nothing either: it is processed before the
	// next cursor-bearing entry, whose confirmation proves the order, and it
	// never produces a receipt of its own.
	channel.incoming <- replayMsg("alpha", "bare-1", "", "m-bare")
	channel.incoming <- replayMsg("alpha", "bare-3", "upd-2", "m-after")
	waitConfirmed(t, channel, "upd-2")
	if n := channel.count(); n != 2 {
		t.Fatalf("receipts = %d, want only the two cursor-bearing entries", n)
	}
	if _, ok := channel.confirmed(""); ok {
		t.Fatal("a cursorless entry was confirmed")
	}

	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// waitConfirmed polls until the channel confirms cursor, failing if it never
// reports the committed outcome within the deadline.
func waitConfirmed(t *testing.T, channel *replayChannel, cursor string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if outcome, ok := channel.confirmed(cursor); ok {
			if outcome != botflota.ReceiveCommitted {
				t.Fatalf("%s after processing = %q, want committed", cursor,
					outcome)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never confirmed after processing settled", cursor)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
