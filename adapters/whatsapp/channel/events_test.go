package channel

import (
	"context"
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
		if message.SenderPhone != "5215500000000" {
			t.Fatalf("sender = %q, want the plain phone number", message.SenderPhone)
		}
		if message.SenderDisplayName != "Ana" {
			t.Fatalf("sender name = %q, want the push name", message.SenderDisplayName)
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

func TestOnMessageIgnoresOwnAndNonText(t *testing.T) {
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

func TestOnHistoryWithoutImporterIsInert(t *testing.T) {
	a := newTestAdapter()
	a.onHistory(&events.HistorySync{Data: &waHistorySync.HistorySync{}})
}

func TestLoggedOutPersistsUnpairedStatus(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "bot", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		scope: "dev", bot: "bot", accounts: db.LinkedAccounts(),
		pairings: db.Pairings(), state: StateConnected,
		stop: make(chan struct{}), log: botflota.DiscardLogger(), now: func() time.Time { return t1 },
	}
	a.handleEvent(&events.LoggedOut{})
	status, err := db.Pairings().Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != botflota.PairingUnpaired || !status.LastChange.Equal(t1) {
		t.Fatalf("status = %#v, want unpaired at %s", status, t1)
	}
}

func newTestAdapter() *Adapter {
	return &Adapter{
		bot:      "support-bot",
		incoming: make(chan botflota.IncomingMessage, 4),
		seen:     newDedup(8),
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
