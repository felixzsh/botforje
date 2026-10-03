package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// TestOutboxDiscardStopsAfterTheInFlightSend: an unlink lets a send already
// in progress finish but never sends later replies from the next account.
func TestOutboxDiscardStopsAfterTheInFlightSend(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	seedReply(store, 2)
	entered := make(chan struct{})
	release := make(chan struct{})
	sender := &recordingSend{}
	cfg := workerConfig(store, func(
		ctx context.Context, msg botflota.OutgoingMessage,
	) (string, error) {
		if sender.attempts() == 0 {
			close(entered)
			<-release
		}
		return sender.send(ctx, msg)
	})
	queue := newOutbox(cfg)
	defer func() {
		queue.Discard()
		select {
		case <-release:
		default:
			close(release)
		}
		queue.Wait()
	}()
	queue.Start()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first send did not start")
	}

	queue.Discard()
	select {
	case <-queue.done:
		t.Fatal("discard stopped before the in-flight send returned")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	queue.Wait()

	if attempts := sender.attempts(); attempts != 1 {
		t.Fatalf("attempts = %d, want only the in-flight send", attempts)
	}
	depth, err := store.Depth(context.Background(), "dev", "alpha")
	if err != nil || depth != 1 {
		t.Fatalf("depth = %d, %v, want the later reply pending", depth, err)
	}
}

// TestOutboxCloseBeforeStartKeepsRepliesPending: when a channel fails its
// initial connection, cleanup closes the unstarted worker without spending
// retries on the disconnected channel.
func TestOutboxCloseBeforeStartKeepsRepliesPending(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	sender := &recordingSend{}
	queue := newOutbox(workerConfig(store, sender.send))
	queue.Close()
	queue.Wait()

	if attempts := sender.attempts(); attempts != 0 {
		t.Fatalf("attempts = %d, want no send before Start", attempts)
	}
	depth, err := store.Depth(context.Background(), "dev", "alpha")
	if err != nil || depth != 1 {
		t.Fatalf("depth = %d, %v, want the reply left pending", depth, err)
	}
}
