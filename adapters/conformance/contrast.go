package conformance

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// contrastBot is one bot the contrast fixture builds over the subject's
// definition: its authored options and its committed binding.
type contrastBot struct {
	name    string
	options spec.ChannelOptions
	binding botflota.Binding
}

// receiptChannel is the test-side view of a confirming channel: the
// processing outcomes the fleet settled through botflota.ReceiveConfirmer.
type receiptChannel interface {
	Receipts() []botflota.ReceiveReceipt
}

// RunContrast is the P6 channel-neutral contrast (docs/channels/phases.md
// P6 item 1). It runs one fixture graph through the subject's real adapter —
// always — and adds the two shared-runtime cases: two bindings over one
// backend, and a source that confirms each entry's agreed processing outcome.
// Everything traverses the real config, stores, fleet, outbox, engine, and
// API; a subject supplies only its own adapter code.
func RunContrast(t *testing.T, s Subject) {
	t.Helper()
	requireSubject(t, s)
	t.Run("graph", func(t *testing.T) { contrastGraph(t, s) })
	if s.Peer != nil {
		t.Run("two-bindings", func(t *testing.T) { contrastTwoBindings(t, s) })
	}
	t.Run("update-source", func(t *testing.T) { contrastUpdateSource(t, s) })
}

// contrastGraph runs the one fixture graph end to end: an inbound mapped the
// subject's way, the engine's reply through the real outbox, its provider id
// stored under the adapter's declared message space, and the conversation read
// back through the real API.
func contrastGraph(t *testing.T, s Subject) {
	f := newFixture(t, s, nil)
	defer f.stop()
	target := supportedTarget(s)
	channel := f.channel()
	f.inject(target, "contrast-1", target.Thread)
	waitFor(t, "the contrast reply", func() bool {
		return len(channel.Sent()) >= 1
	})
	delivered := channel.Sent()[0]
	if got := botflota.ContentText(delivered.Content); got != "hello" {
		t.Fatalf("delivered body = %q, want hello", got)
	}
	if delivered.Target != target {
		t.Fatalf("delivered target = %+v, want %+v", delivered.Target, target)
	}

	providerID := storedProviderID(f, target)
	if providerID == "" {
		t.Fatal("the contrast reply stored no provider id")
	}
	space := messageSpace(s, f)
	stored, err := f.db.Messages().HasProviderID(
		context.Background(), scopeName, botName, space, target, providerID)
	if err != nil {
		t.Fatalf("HasProviderID: %v", err)
	}
	if !stored {
		t.Fatalf("provider id %q is not stored under space %q", providerID, space)
	}
	checkContrastReads(t, s, f)
}

// checkContrastReads proves the live turn is readable through the real API,
// which is the shared read path every adapter must feed.
func checkContrastReads(t *testing.T, s Subject, f *fixture) {
	t.Helper()
	api := newAPIFixture(t, s, f)
	recorder := api.get(t, "/api/v1/scopes/conf/bots/bot/chats")
	if recorder.Code != 200 {
		t.Fatalf("chats status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	var chats struct {
		Chats []struct {
			ID int64 `json:"id"`
		} `json:"chats"`
	}
	decodeJSON(t, recorder.Body.Bytes(), &chats)
	if len(chats.Chats) != 1 {
		t.Fatalf("chats = %d, want the one conversation", len(chats.Chats))
	}
	messages := api.get(t, "/api/v1/scopes/conf/messages?scope=conf&bot=bot"+
		"&chat_id="+strconv.FormatInt(chats.Chats[0].ID, 10))
	if messages.Code != 200 {
		t.Fatalf("messages status = %d, want 200: %s",
			messages.Code, messages.Body.String())
	}
	var body struct {
		Messages []struct {
			Direction string `json:"direction"`
		} `json:"messages"`
	}
	decodeJSON(t, messages.Body.Bytes(), &body)
	if len(body.Messages) != 2 {
		t.Fatalf("messages = %d, want the inbound and its reply", len(body.Messages))
	}
}

// contrastTwoBindings proves one backend serving two committed bindings keeps
// their identities and message spaces apart, and that releasing one view
// leaves its peer usable (ADR 0051, ADR 0052).
func contrastTwoBindings(t *testing.T, s Subject) {
	if s.Peer == nil || s.Peer.Binding == nil {
		t.Fatal("the two-binding contrast needs Subject.Peer.Binding")
	}
	peer := peerBot(s)
	bots := []contrastBot{
		{
			name:    botName,
			options: optionsFor(s, scopeName, botName),
			binding: s.Binding(scopeName, botName),
		},
		{
			name:    peer,
			options: s.Peer.Options,
			binding: s.Peer.Binding(scopeName, peer),
		},
	}
	f := newFixtureBots(
		t, s, filepath.Join(t.TempDir(), "botflota.db"), bots, nil)
	defer f.stop()

	firstBinding := readBinding(t, f, botName)
	secondBinding := readBinding(t, f, peer)
	if firstBinding.Identity == secondBinding.Identity {
		t.Fatalf("both bindings share identity %+v", firstBinding.Identity)
	}
	firstSpace := s.declaration().MessageSpace(firstBinding)
	secondSpace := s.declaration().MessageSpace(secondBinding)
	if firstSpace == secondSpace {
		t.Fatalf("both bindings share message space %q", firstSpace)
	}

	firstTarget := supportedTarget(s)
	secondTarget := firstTarget
	secondTarget.Thread = firstTarget.Thread + "-peer"
	first := f.channelFor(botName)
	second := f.channelFor(peer)
	f.injectFor(botName, firstTarget, "two-1", firstTarget.Thread)
	f.injectFor(peer, secondTarget, "two-2", secondTarget.Thread)
	waitFor(t, "both bindings' replies", func() bool {
		return len(first.Sent()) >= 1 && len(second.Sent()) >= 1
	})
	assertSingleDelivery(t, "first", first, firstTarget)
	assertSingleDelivery(t, "peer", second, secondTarget)

	if err := f.fleet.StopBot(scopeName, botName); err != nil {
		t.Fatalf("StopBot: %v", err)
	}
	peerTarget := secondTarget
	peerTarget.Thread += "-after-release"
	f.injectFor(peer, peerTarget, "two-3", peerTarget.Thread)
	waitFor(t, "the peer reply after release", func() bool {
		return len(second.Sent()) >= 2
	})
	if got := len(first.Sent()); got != 1 {
		t.Fatalf("the released view received %d deliveries, want 1", got)
	}
	if _, err := f.db.Bindings().Get(
		context.Background(), scopeName, peer,
	); err != nil {
		t.Fatalf("the peer binding was lost: %v", err)
	}
}

func readBinding(t *testing.T, f *fixture, bot string) *botflota.Binding {
	t.Helper()
	binding, err := f.db.Bindings().Get(context.Background(), scopeName, bot)
	if err != nil {
		t.Fatalf("read binding for %s: %v", bot, err)
	}
	return binding
}

func assertSingleDelivery(
	t *testing.T, label string, channel Channel, target botflota.ChatTarget,
) {
	t.Helper()
	sent := channel.Sent()
	if len(sent) != 1 {
		t.Fatalf("%s view deliveries = %d, want 1", label, len(sent))
	}
	if sent[0].Target != target {
		t.Fatalf("%s target = %+v, want %+v", label, sent[0].Target, target)
	}
}

// contrastUpdateSource drives a source that confirms each entry's agreed
// processing outcome (ADR 0004, ADR 0040): a committed turn, a persisted
// duplicate, a deliberate discard, and a failure that never confirms. It
// skips a subject whose channel cannot confirm, rather than faking one. The
// fixture's action is emptied so the confirmation path — not the outbox — is
// the one under test here; the graph sub-test covers the reply.
func contrastUpdateSource(t *testing.T, s Subject) {
	f := newFixture(t, s, func(cfg *config.Config) {
		scope := cfg.Scopes[scopeName]
		scope.Actions[actionName] = spec.Action{}
		caller := scope.Bots[botName]
		caller.Settings = &spec.BotSettings{IgnoredPeers: []string{droppedSender}}
		scope.Bots[botName] = caller
		cfg.Scopes[scopeName] = scope
	})
	defer f.stop()
	channel := f.channel()
	recorder, ok := channel.(receiptChannel)
	if !ok {
		t.Skip("the subject's channel does not confirm its source")
	}
	defer func() {
		if t.Failed() {
			t.Logf("confirmations = %+v", recorder.Receipts())
		}
	}()

	target := supportedTarget(s)
	f.deliverUpdate(target, "src-1", "upd-1", target.Thread)
	waitForReceipt(t, recorder, "upd-1", botflota.ReceiveCommitted)

	// The same provider id under a fresh cursor is a persisted duplicate.
	f.deliverUpdate(target, "src-1", "upd-2", target.Thread)
	waitForReceipt(t, recorder, "upd-2", botflota.ReceiveAlreadyRecorded)

	// An ignored sender is a deliberate discard: nothing is stored and the
	// graph never runs.
	dropped := target
	dropped.Thread = target.Thread + "-dropped"
	f.deliverUpdate(dropped, "src-2", "upd-3", droppedSender)
	waitForReceipt(t, recorder, "upd-3", botflota.ReceiveDiscarded)

	// A failure never confirms: a later success proves the failure was
	// already processed, yet its cursor stays unconfirmed.
	failed := target
	failed.Provider = ""
	f.deliverUpdate(failed, "src-3", "upd-4", target.Thread)
	f.deliverUpdate(target, "src-4", "upd-5", target.Thread)
	waitForReceipt(t, recorder, "upd-5", botflota.ReceiveCommitted)
	if outcome, found := receiptFor(recorder, "upd-4"); found {
		t.Fatalf("a failed update was confirmed as %s", outcome)
	}
	assertReceipts(t, recorder, []botflota.ReceiveReceipt{
		{Cursor: "upd-1", Outcome: botflota.ReceiveCommitted},
		{Cursor: "upd-2", Outcome: botflota.ReceiveAlreadyRecorded},
		{Cursor: "upd-3", Outcome: botflota.ReceiveDiscarded},
		{Cursor: "upd-5", Outcome: botflota.ReceiveCommitted},
	})
}

// droppedSender is the identity the update-source fixture ignores, so an
// update from it is a deliberate discard.
const droppedSender = "src-ignored"

func receiptFor(
	recorder receiptChannel, cursor string,
) (botflota.ReceiveOutcome, bool) {
	for _, receipt := range recorder.Receipts() {
		if receipt.Cursor == cursor {
			return receipt.Outcome, true
		}
	}
	return "", false
}

func waitForReceipt(
	t *testing.T, recorder receiptChannel, cursor string, want botflota.ReceiveOutcome,
) {
	t.Helper()
	waitFor(t, "the "+string(want)+" confirmation for "+cursor, func() bool {
		outcome, found := receiptFor(recorder, cursor)
		return found && outcome == want
	})
}

func assertReceipts(
	t *testing.T, recorder receiptChannel, want []botflota.ReceiveReceipt,
) {
	t.Helper()
	got := recorder.Receipts()
	if len(got) != len(want) {
		t.Fatalf("receipts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("receipt %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// deliverUpdate hands one source update to a running bot: an inbound entry
// with the source's own opaque cursor, so the fleet can settle it through the
// real botflota.ReceiveConfirmer path.
func (f *fixture) deliverUpdate(
	target botflota.ChatTarget, id, cursor, sender string,
) {
	f.deliverFor(botName, botflota.IncomingMessage{
		Bot: botName,
		Sender: botflota.IdentityKey{
			Provider: f.subject.declaration().Provider, Subject: sender,
		},
		Target:            target,
		Content:           botflota.TextContent{Body: "hello"},
		ProviderMessageID: id,
		ReceiveCursor:     cursor,
		At:                time.Now(),
	})
}
