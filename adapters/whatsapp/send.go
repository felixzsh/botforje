package whatsapp

import (
	"context"
	"errors"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
)

// Send maps the plain recipient phone number to the JID the message arrived
// from, or to the phone-number JID when that is unknown (ADR 0020).
func (a *Adapter) Send(ctx context.Context, msg botflota.OutgoingMessage) (string, error) {
	a.mu.Lock()
	client := a.client
	last := a.last[msg.RecipientPhone]
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return "", errors.New("whatsapp: not connected")
	}
	if msg.RecipientPhone == "" {
		return "", errors.New("whatsapp: message has no recipient")
	}
	to := last.chat
	if to.IsEmpty() {
		to = types.NewJID(msg.RecipientPhone, types.DefaultUserServer)
	}
	readDelay := a.jittered(a.settings.ReadReceiptDelay)
	replyDelay := a.jittered(a.settings.ReplyDelay)
	if a.settings.ReadReceipts && !last.chat.IsEmpty() {
		if err := a.sleepFor(ctx, readDelay); err != nil {
			return "", err
		}
		if err := client.MarkRead(ctx, []types.MessageID{last.id}, a.now(),
			last.chat, last.sender); err != nil {
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
	response, err := client.SendMessage(ctx, to, content)
	if composing {
		_ = client.SendChatPresence(ctx, to, types.ChatPresencePaused,
			types.ChatPresenceMediaText)
	}
	if err != nil {
		return "", err
	}
	return string(response.ID), nil
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
