package channel

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
)

func TestOnMessageMapsAndDedups(t *testing.T) {
	a := newTestAdapter()
	event := textEvent("5215500000000", "ABC", "hello")
	event.Info.PushName = "Ana"

	a.onMessage(event)
	a.onMessage(event) // redelivery

	select {
	case message := <-a.incoming:
		if message.Bot != "support-bot" ||
			botflota.ContentText(message.Content) != "hello" {
			t.Fatalf("message = %#v", message)
		}
		if message.Sender != waKey("5215500000000") ||
			message.Target != waTarget("5215500000000") {
			t.Fatalf("sender/target = %#v / %#v, want the normalized pair",
				message.Sender, message.Target)
		}
		if message.FromName != "Ana" {
			t.Fatalf("sender name = %q, want the push name", message.FromName)
		}
	default:
		t.Fatal("no message was delivered")
	}
	select {
	case message := <-a.incoming:
		t.Fatalf("duplicate was delivered: %#v", message)
	default:
	}
	if _, ok := a.last["5215500000000"]; !ok {
		t.Fatal("the last inbound message was not tracked")
	}
}

func TestOnMessageDropsUnresolvedLID(t *testing.T) {
	a := newTestAdapter()
	lid := types.NewJID("212150010273830", types.HiddenUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: lid, Chat: lid},
			ID:            types.MessageID("XYZ"),
			Timestamp:     t0,
		},
		Message: &waE2E.Message{Conversation: proto.String("hi")},
	}
	a.onMessage(event)
	select {
	case message := <-a.incoming:
		t.Fatalf("an unresolved message was delivered: %#v", message)
	default:
	}
}

// TestOnMessageNeverRoutesAnOwnMessageOrEmptyText: a message of our own account
// is an observation for the hand-over path, never a routable inbound, and an
// empty payload is dropped (ADR 0043).
func TestOnMessageNeverRoutesAnOwnMessageOrEmptyText(t *testing.T) {
	a := newTestAdapter()
	own := textEvent("5215500000000", "1", "mine")
	own.Info.IsFromMe = true
	a.onMessage(own)

	empty := textEvent("5215500000000", "2", "")
	a.onMessage(empty)

	select {
	case message := <-a.incoming:
		t.Fatalf("unexpected message: %#v", message)
	default:
	}
}

// TestOnOwnMessageReportsExternalReply: a live message that left the account
// and was not sent by this adapter hands the chat to a human (ADR 0043).
func TestOnOwnMessageReportsExternalReply(t *testing.T) {
	a := newTestAdapter()
	own := textEvent("5215500000000", "EXT1", "lo veo yo")
	own.Info.IsFromMe = true

	a.onMessage(own)

	select {
	case out := <-a.outbound:
		if out.Target != waTarget("5215500000000") ||
			botflota.ContentText(out.Content) != "lo veo yo" {
			t.Fatalf("outbound = %#v", out)
		}
	default:
		t.Fatal("an external reply was not observed")
	}
}

// TestOnOwnMessageIgnoresItsOwnEcho: the adapter's own sent id must never
// pause the chat it just answered (ADR 0043).
func TestOnOwnMessageIgnoresItsOwnEcho(t *testing.T) {
	a := newTestAdapter()
	a.markSent("MINE1")
	own := textEvent("5215500000000", "MINE1", "respuesta del bot")
	own.Info.IsFromMe = true

	a.onMessage(own)

	select {
	case out := <-a.outbound:
		t.Fatalf("our own echo was reported: %#v", out)
	default:
	}
}

// TestOnMessageWaitsWhenQueueIsFull proves backpressure instead of a drop:
// with a full queue the event handler blocks until the consumer takes a
// message, and never discards the inbound one (ADR 0011).
func TestOnMessageWaitsWhenQueueIsFull(t *testing.T) {
	a := newTestAdapter()
	a.incoming = make(chan botflota.IncomingMessage, 1)
	a.onMessage(textEvent("5215500000000", "1", "first"))

	delivered := make(chan struct{})
	go func() {
		a.onMessage(textEvent("5215500000001", "2", "second"))
		close(delivered)
	}()

	select {
	case <-delivered:
		t.Fatal("second message was dropped while the queue was full")
	case <-time.After(50 * time.Millisecond):
	}
	<-a.incoming // the consumer takes one entry and frees a slot
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("second message never reached the queue")
	}
	if message := <-a.incoming; botflota.ContentText(message.Content) != "second" {
		t.Fatalf("message = %#v, want the blocked one", message)
	}
}

// TestStoppedAdapterReleasesBlockedHandoffs: a full bounded queue blocks the
// event handler instead of dropping, and closing the adapter releases that wait
// so no producer goroutine leaks (ADR 0011, ADR 0040, ADR 0052).
func TestStoppedAdapterReleasesBlockedHandoffs(t *testing.T) {
	tests := []struct {
		name string
		fill func(a *Adapter)
		wait func(a *Adapter)
	}{
		{
			name: "inbound message",
			fill: func(a *Adapter) {
				a.incoming <- textInbound("5215500000000", "IN-1")
			},
			wait: func(a *Adapter) {
				a.onMessage(textEvent("5215500000001", "IN-2", "second"))
			},
		},
		{
			name: "external reply observation",
			fill: func(a *Adapter) {
				a.outbound <- botflota.ExternalSend{
					Bot: "support-bot", Target: waTarget("5215500000000"),
					Content: botflota.TextContent{Body: "held"},
				}
			},
			wait: func(a *Adapter) {
				own := textEvent("5215500000001", "OUT-2", "mine")
				own.Info.IsFromMe = true
				a.onMessage(own)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newTestAdapter()
			a.stop = make(chan struct{})
			a.incoming = make(chan botflota.IncomingMessage, 1)
			a.outbound = make(chan botflota.ExternalSend, 1)
			test.fill(a)
			released := make(chan struct{})
			go func() {
				test.wait(a)
				close(released)
			}()
			select {
			case <-released:
				t.Fatal("the handoff returned while the bounded queue was full")
			case <-time.After(50 * time.Millisecond):
			}
			close(a.stop)
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Fatal("closing the adapter did not release the blocked handoff")
			}
		})
	}
}

// TestObservationsShareTheLiveConversationValues: a live inbound and an
// external-reply observation of the same wire chat reduce to the same neutral
// conversation target and sender identity, so the hand-over path and the
// routable path agree (ADR 0043, ADR 0051).
func TestObservationsShareTheLiveConversationValues(t *testing.T) {
	a := newTestAdapter()
	a.onMessage(textEvent("5215500000000", "IN-1", "hi"))
	own := textEvent("5215500000000", "OUT-1", "mine")
	own.Info.IsFromMe = true
	a.onMessage(own)

	inbound := <-a.incoming
	external := <-a.outbound
	if inbound.Target != waTarget("5215500000000") ||
		inbound.Sender != waKey("5215500000000") {
		t.Fatalf("inbound sender/target = %+v / %+v", inbound.Sender, inbound.Target)
	}
	if external.Target != inbound.Target {
		t.Fatalf("external target = %+v, want the live %+v",
			external.Target, inbound.Target)
	}
	if external.MessageSpace != inbound.MessageSpace {
		t.Fatalf("external space = %q, want the live %q",
			external.MessageSpace, inbound.MessageSpace)
	}
}

// TestOptionalOperationsStayInertWhenDisabled proves the adapter never calls
// an optional SDK-backed feature it was not configured for: history sync, the
// address book, and their events are inert without their option (ADR 0035,
// ADR 0037, ADR 0052).
func TestOptionalOperationsStayInertWhenDisabled(t *testing.T) {
	a := newTestAdapter()
	tests := []struct {
		name string
		run  func()
	}{
		{"history chunk", func() {
			a.onHistory(&events.HistorySync{Data: &waHistorySync.HistorySync{}})
		}},
		{"history event", func() {
			a.handleEvent(&events.HistorySync{Data: &waHistorySync.HistorySync{}})
		}},
		{"address book event", func() { a.onContact(&events.Contact{}) }},
		{"address book event by handler", func() { a.handleEvent(&events.Contact{}) }},
		{"cached address book", func() { a.onceConnected() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.run()
		})
	}
	if a.importer != nil || a.addressBook != nil {
		t.Fatal("a disabled optional feature built a worker")
	}
}

func TestOnHistoryWithoutImporterIsInert(t *testing.T) {
	a := newTestAdapter()
	a.onHistory(&events.HistorySync{Data: &waHistorySync.HistorySync{}})
}

// textInbound is a minimal neutral inbound entry for a queue-fill helper.
func textInbound(subject, id string) botflota.IncomingMessage {
	return botflota.IncomingMessage{
		Bot: "support-bot", Sender: waKey(subject), Target: waTarget(subject),
		Content: botflota.TextContent{Body: "first"}, ProviderMessageID: id,
	}
}

// TestLoggedOutReportsTheNeutralState: the fleet reads the adapter's own
// lifecycle surface, so a provider logout moves it to logged_out. No stored
// pairing row carries that transition (ADR 0011).
func TestLoggedOutReportsTheNeutralState(t *testing.T) {
	a := newTestAdapter()
	a.stop = make(chan struct{})
	a.handleEvent(&events.LoggedOut{})
	if got := a.State(); got != StateLoggedOut {
		t.Fatalf("state = %q, want %q", got, StateLoggedOut)
	}
}

// TestConnectFailuresThatStopTheBot: 428 and 463 leave nothing to retry, so the
// bot stops in its own state and is reported, while an ordinary failure keeps
// the reconnection logic in charge (ADR 0011, ADR 0028).
func TestConnectFailuresThatStopTheBot(t *testing.T) {
	cases := []struct {
		name   string
		reason events.ConnectFailureReason
		want   State
	}{
		{"passkey required", events.ConnectFailureReason(428), StatePasskeyRequired},
		{"reachout timelock", events.ConnectFailureReason(463), StateReachoutTimelock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter()
			a.stop = make(chan struct{})
			a.handleEvent(&events.ConnectFailure{Reason: tc.reason})
			if got := a.State(); got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
			select {
			case <-a.stop:
			default:
				t.Fatal("the bot kept its reconnection loop")
			}
		})
	}

	t.Run("an ordinary failure is not fatal", func(t *testing.T) {
		a := newTestAdapter()
		a.stop = make(chan struct{})
		a.setState(StateConnecting)
		a.handleEvent(&events.ConnectFailure{Reason: events.ConnectFailureServiceUnavailable})
		if got := a.State(); got != StateConnecting {
			t.Fatalf("state = %q, want it unchanged", got)
		}
		select {
		case <-a.stop:
			t.Fatal("an ordinary failure stopped the bot")
		default:
		}
	})
}

// TestLiveIngressNeverBecomesATextTurn is the ingest-boundary conformance
// check for unsupported interactions: control traffic (reactions, edits,
// poll votes, protocol messages), receipts, and payloads the adapter cannot
// model are dropped before routing. None of them may masquerade as a text
// turn, reach the inbound queue, or surface as an external-send observation
// (ADR 0035, ADR 0036). No button, editing, or receipt machinery is added
// for them: the boundary stays honest by discarding them.
func TestLiveIngressNeverBecomesATextTurn(t *testing.T) {
	message := func(body *waE2E.Message, fromMe bool) *events.Message {
		event := textEvent("5215500000000", "CTRL", "")
		event.Message = body
		event.Info.IsFromMe = fromMe
		return event
	}
	tests := []struct {
		name  string
		event *events.Message
	}{
		{"reaction", message(&waE2E.Message{
			ReactionMessage: &waE2E.ReactionMessage{}}, false)},
		{"edited payload", message(&waE2E.Message{
			EditedMessage: &waE2E.FutureProofMessage{}}, false)},
		{"poll vote", message(&waE2E.Message{
			PollUpdateMessage: &waE2E.PollUpdateMessage{}}, false)},
		{"protocol traffic", message(&waE2E.Message{
			ProtocolMessage: &waE2E.ProtocolMessage{}}, false)},
		{"unmodelable payload", message(&waE2E.Message{
			PlaceholderMessage: &waE2E.PlaceholderMessage{}}, false)},
		{"own reaction", message(&waE2E.Message{
			ReactionMessage: &waE2E.ReactionMessage{}}, true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newTestAdapter()
			a.onMessage(test.event)
			select {
			case got := <-a.incoming:
				t.Fatalf("control traffic reached the inbound queue: %#v", got)
			default:
			}
			select {
			case got := <-a.outbound:
				t.Fatalf("control traffic was observed as an external send: %#v", got)
			default:
			}
		})
	}
}

// TestReceiptsAreNotTurns proves delivery/read receipts never enter the
// inbound or observation queues: they are transport signals, not content.
func TestReceiptsAreNotTurns(t *testing.T) {
	a := newTestAdapter()
	jid := types.NewJID("5215500000000", types.DefaultUserServer)
	a.handleEvent(&events.Receipt{
		MessageSource: types.MessageSource{Sender: jid, Chat: jid},
		MessageIDs:    []types.MessageID{"ABC"},
		Timestamp:     t0,
	})
	select {
	case got := <-a.incoming:
		t.Fatalf("a receipt reached the inbound queue: %#v", got)
	default:
	}
	select {
	case got := <-a.outbound:
		t.Fatalf("a receipt was observed as an external send: %#v", got)
	default:
	}
}

func newTestAdapter() *Adapter {
	return &Adapter{
		bot:      "support-bot",
		incoming: make(chan botflota.IncomingMessage, 4),
		outbound: make(chan botflota.ExternalSend, 4),
		seen:     newDedup(8),
		sentIDs:  newDedup(8),
		last:     map[string]lastInbound{},
		log:      botflota.DiscardLogger(),
	}
}

func textEvent(sender, id, body string) *events.Message {
	jid := types.NewJID(sender, types.DefaultUserServer)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: jid, Chat: jid},
			ID:            types.MessageID(id),
			Timestamp:     t0,
		},
		Message: &waE2E.Message{Conversation: proto.String(body)},
	}
}
