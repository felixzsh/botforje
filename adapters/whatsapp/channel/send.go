package channel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
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
	// A vanished local device means the binding lost its authorization:
	// the delivery stays pending while its bot stops, never retried here.
	if a.devices != nil && a.bindings != nil {
		authorized, err := a.authorized(ctx)
		if err != nil {
			return "", botflota.TransientSendFailure(err, 0)
		}
		if !authorized {
			return "", botflota.AuthorizationLost(
				errors.New("whatsapp: binding authorization lost"))
		}
	}
	a.mu.Lock()
	client := a.client
	last := a.last[thread]
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return "", botflota.TransientSendFailure(
			errors.New("whatsapp: not connected"), 0)
	}
	to := last.chat
	if to.IsEmpty() {
		to = types.NewJID(thread, types.DefaultUserServer)
	}
	readDelay := a.jittered(a.settings.ReadReceiptDelay)
	replyDelay := a.jittered(a.settings.ReplyDelay)
	if a.settings.ReadReceipts && !last.chat.IsEmpty() {
		if err := a.sleepFor(ctx, readDelay); err != nil {
			return "", botflota.TransientSendFailure(err, 0)
		}
		if err := client.MarkRead(ctx, []types.MessageID{last.id}, a.now(),
			last.chat, last.from); err != nil {
			a.log.Warn("read receipt failed", "error", err)
		}
	}
	content, err := a.contentFor(ctx, msg)
	if err != nil {
		return "", classifyContentError(err)
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
		return "", botflota.TransientSendFailure(err, 0)
	}
	// The id is owned by the durable delivery and reused across every
	// attempt of it; only a send queued before the key existed mints one
	// here. It is marked as ours before the send, so its echo is never
	// mistaken for an operator reply (ADR 0043, ADR 0052).
	messageID := msg.DeliveryKey
	if messageID == "" {
		messageID = string(client.GenerateMessageID())
	}
	a.markSent(messageID)
	response, err := client.SendMessage(ctx, to, content,
		whatsmeow.SendRequestExtra{ID: types.MessageID(messageID)})
	if composing {
		_ = client.SendChatPresence(ctx, to, types.ChatPresencePaused,
			types.ChatPresenceMediaText)
	}
	if err != nil {
		// The provider may have accepted the message before the error:
		// uncertain, never a proven rejection (ADR 0052).
		return "", botflota.UncertainSendFailure(fmt.Errorf("whatsapp: send: %w", err))
	}
	return string(response.ID), nil
}

// thread validates an outbound target and returns the phone-number thread this
// adapter addresses. A shared conversation and a topic are refused rather than
// sent to a different conversation than the one the target names (ADR 0051).
// Every refusal is permanent: retrying invalid input never delivers it.
func (a *Adapter) thread(target botflota.ChatTarget) (string, error) {
	if target.Provider != ingest.Provider {
		return "", botflota.PermanentSendFailure(fmt.Errorf(
			"whatsapp: target provider %q is not whatsapp", target.Provider))
	}
	if target.Kind != botflota.ChatDirect {
		return "", fmt.Errorf("%w: shared conversations", botflota.ErrUnsupportedKind)
	}
	if target.Thread == "" {
		return "", botflota.PermanentSendFailure(
			errors.New("whatsapp: message has no target thread"))
	}
	if target.Topic != "" {
		// WhatsApp has no topics: sending the thread alone would deliver to a
		// different conversation than the target names.
		return "", botflota.PermanentSendFailure(fmt.Errorf(
			"whatsapp: direct target carries topic %q", target.Topic))
	}
	return target.Thread, nil
}

// authorized reports whether the committed binding still has its local
// device. A missing device means the authorization is gone, not that the
// transport is down (ADR 0053).
func (a *Adapter) authorized(ctx context.Context) (bool, error) {
	account, err := a.bindings.Get(ctx, a.scope, a.bot)
	if err != nil {
		return false, err
	}
	return whatsapp.HasLocalDevice(ctx, a.devices, account)
}

// classifyContentError keeps the adapter's own typed outcomes and marks any
// other content-building refusal permanent: it is invalid input, never a
// reason to retry. Media transport failures already carry their transient
// type from the builder below.
func classifyContentError(err error) error {
	if _, ok := botflota.AsSendFailure(err); ok {
		return err
	}
	return botflota.PermanentSendFailure(err)
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
