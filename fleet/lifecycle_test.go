package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

func buildFleetWithChannel(
	t *testing.T, store *memStore, channel botflota.MessageChannel,
) *Fleet {
	t.Helper()
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Contacts: store, Messages: store,
		Turn: store, Outbox: store,
		Skip: func(_, bot string) bool { return bot == "beta" },
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) {
			return channel, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

// TestOutboxStartsAfterChannelConnect keeps durable replies from spending
// attempts while a channel is still connecting (ADR 0040).
func TestOutboxStartsAfterChannelConnect(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	channel := newFakeChannel()
	channel.connectEntered = make(chan struct{})
	channel.connectRelease = make(chan struct{})
	channel.entered = make(chan struct{})
	runner := buildFleetWithChannel(t, store, channel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-channel.connectEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the channel did not start connecting")
	}
	select {
	case <-channel.entered:
		t.Fatal("the outbox sent before Connect returned")
	case <-time.After(30 * time.Millisecond):
	}

	close(channel.connectRelease)
	waitConnected(t, runner)
	if reply := receive(t, channel.sent); botflota.ContentText(reply.Content) != "reply 1" {
		t.Fatalf("reply = %q, want the recovered outbox message",
			botflota.ContentText(reply.Content))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestConnectFailureClosesUnstartedWorkers leaves durable replies pending
// and closes the channel's workers even when its initial Connect fails.
func TestConnectFailureClosesUnstartedWorkers(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	channel := newFakeChannel()
	channel.connectEntered = make(chan struct{})
	channel.connectErr = errors.New("connect failed")
	runner := buildFleetWithChannel(t, store, channel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-channel.connectEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the channel did not start connecting")
	}

	bot := runner.bots["dev/alpha"]
	select {
	case <-bot.done:
	case <-time.After(2 * time.Second):
		t.Fatal("bot did not finish cleanup after Connect failed")
	}
	if !channel.wasDisconnected() {
		t.Fatal("channel workers were not disconnected after Connect failed")
	}
	select {
	case <-bot.outbox.done:
	default:
		t.Fatal("the unstarted outbox worker was not closed")
	}
	if attempts := channel.sendCount(); attempts != 0 {
		t.Fatalf("send attempts = %d, want none before connection", attempts)
	}
	if depth, err := store.Depth(context.Background(), "dev", "alpha"); err != nil || depth != 1 {
		t.Fatalf("outbox depth = %d, %v, want the reply pending", depth, err)
	}

	cancel()
	if err := <-done; err == nil {
		t.Fatal("Run returned nil after the bot's connection failed")
	}
}

// TestStopBotForUnpairDiscardsTheRemainingQueue lets only the current send
// finish. Later replies stay pending for the unlink operation to fail before
// another account is paired (ADR 0040).
func TestStopBotForUnpairDiscardsTheRemainingQueue(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "alpha", nil)
	seedReply(store, 1)
	seedReply(store, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]
	select {
	case <-alpha.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first queued send did not start")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- runner.StopBotForUnpair("dev", "alpha") }()
	deadline := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for !runner.bots["dev/alpha"].outbox.discard.Load() {
		select {
		case <-deadline.C:
			t.Fatal("StopBotForUnpair did not discard the queue")
		case <-ticker.C:
		}
	}
	if alpha.wasDisconnected() {
		t.Fatal("the channel disconnected before the in-flight send finished")
	}
	select {
	case err := <-stopped:
		t.Fatalf("StopBotForUnpair returned during the send: %v", err)
	default:
	}

	close(alpha.release)
	if err := <-stopped; err != nil {
		t.Fatalf("StopBotForUnpair: %v", err)
	}
	if reply := receive(t, alpha.sent); botflota.ContentText(reply.Content) != "reply 1" {
		t.Fatalf("sent reply = %q, want only the in-flight head",
			botflota.ContentText(reply.Content))
	}
	if attempts := alpha.sendCount(); attempts != 1 {
		t.Fatalf("send attempts = %d, want only the in-flight send", attempts)
	}
	if depth, err := store.Depth(context.Background(), "dev", "alpha"); err != nil ||
		depth != 1 {
		t.Fatalf("pending depth = %d, %v, want the later reply left pending", depth, err)
	}
	if _, err := store.FailPending(context.Background(), "dev", "alpha",
		"account unlinked", t0); err != nil {
		t.Fatalf("FailPending: %v", err)
	}
	if depth, err := store.Depth(context.Background(), "dev", "alpha"); err != nil ||
		depth != 0 {
		t.Fatalf("depth after unlink = %d, %v, want no pending replies", depth, err)
	}

	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
