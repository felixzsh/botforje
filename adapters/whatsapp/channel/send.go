package channel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// Send addresses the exact conversation target the reply carries, recovering
// the JID the message arrived from when it is known and falling back to the
// target's phone-number thread otherwise (ADR 0020, ADR 0051).
func (a *Adapter) Send(ctx context.Context, msg botflota.OutgoingMessage) (string, error) {
	thread, err := a.thread(msg.Target)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	client := a.client
	last := a.last[thread]
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return "", errors.New("whatsapp: not connected")
	}
	to := last.chat
	if to.IsEmpty() {
		to = types.NewJID(thread, types.DefaultUserServer)
	}
	readDelay := a.jittered(a.settings.ReadReceiptDelay)
	replyDelay := a.jittered(a.settings.ReplyDelay)
	if a.settings.ReadReceipts && !last.chat.IsEmpty() {
		if err := a.sleepFor(ctx, readDelay); err != nil {
			return "", err
		}
		if err := client.MarkRead(ctx, []types.MessageID{last.id}, a.now(),
			last.chat, last.from); err != nil {
			a.log.Warn("read receipt failed", "error", err)
		}
	}
	content, err := a.contentFor(ctx, msg)
	if err != nil {
		return "", err
	}
	a.log.Debug("reply pacing", "read_receipt_delay", readDelay,
		"reply_delay", replyDelay, "jitter", a.settings.Jitter)
	composing := false
	if a.settings.SimulateTyping {
		if err := client.SendChatPresence(ctx, to, types.ChatPresenceComposing,
			types.ChatPresenceMediaText); err != nil {
			a.log.Warn("composing presence failed", "error", err)
		} else {
			composing = true
		}
	}
	if err := a.sleepFor(ctx, replyDelay); err != nil {
		return "", err
	}
	// The id is chosen before the send, so the reply is marked as ours before
	// its echo could arrive and be mistaken for an operator reply (ADR 0043).
	messageID := client.GenerateMessageID()
	a.markSent(string(messageID))
	response, err := client.SendMessage(ctx, to, content,
		whatsmeow.SendRequestExtra{ID: messageID})
	if composing {
		_ = client.SendChatPresence(ctx, to, types.ChatPresencePaused,
			types.ChatPresenceMediaText)
	}
	if err != nil {
		return "", err
	}
	return string(response.ID), nil
}

// thread validates an outbound target and returns the phone-number thread this
// adapter addresses. A shared conversation and a topic are refused rather than
// sent to a different conversation than the one the target names (ADR 0051).
func (a *Adapter) thread(target botflota.ChatTarget) (string, error) {
	if target.Provider != ingest.Provider {
		return "", fmt.Errorf(
			"whatsapp: target provider %q is not whatsapp", target.Provider)
	}
	if target.Kind != botflota.ChatDirect {
		return "", fmt.Errorf("%w: shared conversations", botflota.ErrUnsupportedKind)
	}
	if target.Thread == "" {
		return "", errors.New("whatsapp: message has no target thread")
	}
	if target.Topic != "" {
		// WhatsApp has no topics: sending the thread alone would deliver to a
		// different conversation than the target names.
		return "", fmt.Errorf("whatsapp: direct target carries topic %q", target.Topic)
	}
	return target.Thread, nil
}

// jittered adds a random value in [0, jitter] to one delay. Each call draws
// again, so consecutive replies never share the same pacing.
func (a *Adapter) jittered(delay time.Duration) time.Duration {
	if a.settings.Jitter <= 0 || a.rnd == nil {
		return delay
	}
	return delay + time.Duration(a.rnd.Int63n(int64(a.settings.Jitter)+1))
}

func (a *Adapter) sleepFor(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if a.sleep != nil {
		return a.sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
