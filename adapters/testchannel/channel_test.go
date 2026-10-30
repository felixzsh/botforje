package testchannel_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/channels"
)

// TestInjectContextGivesUpWhenTheInboundBufferIsFull proves a bounded run's
// sender can stop: a full stream ends the attempt with ctx's error and leaves
// the buffered messages untouched.
func TestInjectContextGivesUpWhenTheInboundBufferIsFull(t *testing.T) {
	channel := newTestChannel(t)
	capacity := cap(channel.Incoming())
	for i := 0; i < capacity; i++ {
		channel.Inject(inbound(i))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := channel.InjectContext(ctx, inbound(capacity)); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("InjectContext on a full buffer = %v, want context.Canceled", err)
	}
	if got := len(channel.Incoming()); got != capacity {
		t.Fatalf("buffered messages = %d, want the %d already queued", got, capacity)
	}
	for i := 0; i < capacity; i++ {
		if got := <-channel.Incoming(); got.ProviderMessageID != inbound(i).ProviderMessageID {
			t.Fatalf("buffered[%d] = %q, want %q", i, got.ProviderMessageID,
				inbound(i).ProviderMessageID)
		}
	}
}

// TestInjectContextFillsTheAdapterOwnedFields proves the cancellable form is
// the same delivery path as Inject, not a second contract.
func TestInjectContextFillsTheAdapterOwnedFields(t *testing.T) {
	channel := newTestChannel(t)
	if err := channel.InjectContext(context.Background(), botflota.IncomingMessage{
		ProviderMessageID: "ctx-1",
	}); err != nil {
		t.Fatalf("InjectContext: %v", err)
	}
	got := <-channel.Incoming()
	if got.Bot != "bot" || got.MessageSpace != "profile-a" ||
		got.ReceiveCursor != "cursor-ctx-1" {
		t.Fatalf("adapter-owned fields = %+v", got)
	}
}

// TestRetainKeepsNewestSamplesAndExactCounts proves a long run can bound what a
// channel remembers without losing the totals it reports.
func TestRetainKeepsNewestSamplesAndExactCounts(t *testing.T) {
	channel := newTestChannel(t)
	channel.Retain(2)
	for i := 0; i < 5; i++ {
		if _, err := channel.Send(context.Background(), outbound(i)); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if got := channel.SentCount(); got != 5 {
		t.Fatalf("SentCount = %d, want 5", got)
	}
	sent := channel.Sent()
	if len(sent) != 2 {
		t.Fatalf("retained sends = %d, want 2", len(sent))
	}
	if got := botflota.ContentText(sent[0].Content); got != "m3" {
		t.Fatalf("oldest retained body = %q, want m3", got)
	}
	if got := botflota.ContentText(sent[1].Content); got != "m4" {
		t.Fatalf("newest retained body = %q, want m4", got)
	}

	for i := 0; i < 3; i++ {
		if err := channel.ConfirmReceive(context.Background(), botflota.ReceiveReceipt{
			Cursor:  "c",
			Outcome: botflota.ReceiveCommitted,
		}); err != nil {
			t.Fatalf("ConfirmReceive %d: %v", i, err)
		}
	}
	if got := channel.ReceiptCount(); got != 3 {
		t.Fatalf("ReceiptCount = %d, want 3", got)
	}
	if got := len(channel.Receipts()); got != 2 {
		t.Fatalf("retained receipts = %d, want 2", got)
	}
}

// TestOnSendObservesEveryDelivery proves the live observer reports what the
// retained sample intentionally drops, which is how a run measures each reply.
func TestOnSendObservesEveryDelivery(t *testing.T) {
	channel := newTestChannel(t)
	channel.Retain(1)
	var mu sync.Mutex
	var bodies []string
	channel.OnSend(func(msg botflota.OutgoingMessage) {
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, botflota.ContentText(msg.Content))
	})
	for i := 0; i < 3; i++ {
		if _, err := channel.Send(context.Background(), outbound(i)); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("observed = %v, want every one of the three deliveries", bodies)
	}
	for i, body := range bodies {
		if want := botflota.ContentText(outbound(i).Content); body != want {
			t.Fatalf("observed[%d] = %q, want %q", i, body, want)
		}
	}
}

func newTestChannel(t *testing.T) *testchannel.Channel {
	t.Helper()
	build := testchannel.Builder(testchannel.Deps{Backend: testchannel.NewBackend()})
	built, err := build(channels.Bind{
		Scope: "conf", Bot: "bot",
		Options: testchannel.BotOptions{Namespace: "profile-a"},
	})
	if err != nil {
		t.Fatalf("build channel: %v", err)
	}
	channel, ok := built.(*testchannel.Channel)
	if !ok {
		t.Fatalf("builder returned %T, want a testchannel channel", built)
	}
	return channel
}

func inbound(i int) botflota.IncomingMessage {
	return botflota.IncomingMessage{
		ProviderMessageID: "in-" + strconv.Itoa(i),
		Content:           botflota.TextContent{Body: "hello"},
	}
}

func outbound(i int) botflota.OutgoingMessage {
	return botflota.OutgoingMessage{
		Content: botflota.TextContent{Body: "m" + strconv.Itoa(i)},
	}
}
