package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestCommitSendCreatesContactChatAndQueue(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	result, err := db.Sends().CommitSend(ctx, botflota.SendRecord{
		Scope: "support", Bot: "support-bot",
		Target:    waTarget("5215599999999"),
		Recipient: waPeerPtr("5215599999999"),
		Content:   botflota.TextContent{Body: "tu pedido está listo"},
		ClientID:  "order-1", At: t1,
	})
	if err != nil {
		t.Fatalf("CommitSend: %v", err)
	}
	if result.MessageID == 0 || result.OutboxID == 0 {
		t.Fatalf("result = %#v, want both ids", result)
	}

	contact, err := db.Peers().Get(ctx, "support", waKey("5215599999999"))
	if err != nil {
		t.Fatalf("contact: %v", err)
	}
	// An API send is not a live inbound: it must not consume the first-contact
	// hook or move first/last seen (ADR 0044, ADR 0045).
	if !contact.FirstSeen.IsZero() || !contact.LastSeen.IsZero() {
		t.Fatalf("contact seen = %v/%v, want zero", contact.FirstSeen, contact.LastSeen)
	}
	chat, err := db.Chats().ForTarget(ctx, "support", "support-bot",
		waTarget("5215599999999"))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !chat.UpdatedAt.Equal(t1) || !chat.StartedAt.Equal(t1) {
		t.Fatalf("chat activity = %v..%v, want %v", chat.StartedAt, chat.UpdatedAt, t1)
	}
	if chat.MessageCount != 0 {
		t.Fatalf("message_count = %d, want no inbound count for a send", chat.MessageCount)
	}

	messages, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	message := messages[0]
	if message.ID != result.MessageID ||
		message.Direction != botflota.Outbound ||
		message.SessionID != 0 ||
		message.Ingest != botflota.IngestAPI ||
		message.Origin != botflota.OriginAPI ||
		message.Routing != botflota.RoutingUnknown {
		t.Fatalf("message = %#v", message)
	}
	if body := botflota.ContentText(message.Content); body != "tu pedido está listo" {
		t.Fatalf("body = %q", body)
	}

	entries, err := db.Outbox().NextPending(ctx, "support", "support-bot", 5)
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("queued = %d, want 1", len(entries))
	}
	if entries[0].ID != result.OutboxID ||
		entries[0].Target != waTarget("5215599999999") ||
		entries[0].Origin != botflota.OriginAPI {
		t.Fatalf("entry = %#v", entries[0])
	}
	var queuedMessageID int64
	if err := db.QueryRow(
		`SELECT message_id FROM outbox WHERE id = ?`, entries[0].ID).
		Scan(&queuedMessageID); err != nil {
		t.Fatalf("outbox message id: %v", err)
	}
	if queuedMessageID != result.MessageID {
		t.Fatalf("queued message = %d, want %d", queuedMessageID, result.MessageID)
	}
	if body := botflota.ContentText(entries[0].Content); body != "tu pedido está listo" {
		t.Fatalf("queued body = %q", body)
	}
}

func TestCommitSendClientIDIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	send := botflota.SendRecord{
		Scope: "support", Bot: "support-bot",
		Target:    waTarget("5215599999999"),
		Recipient: waPeerPtr("5215599999999"),
		Content:   botflota.TextContent{Body: "aviso"}, ClientID: "notice-7", At: t0,
	}
	first, err := db.Sends().CommitSend(ctx, send)
	if err != nil {
		t.Fatalf("first CommitSend: %v", err)
	}
	second, err := db.Sends().CommitSend(ctx, send)
	if !errors.Is(err, botflota.ErrDuplicate) {
		t.Fatalf("repeat error = %v, want ErrDuplicate with ids %#v", err, second)
	}
	if second.MessageID != first.MessageID || second.OutboxID != first.OutboxID {
		t.Fatalf("repeat = %#v, want the accepted %#v", second, first)
	}
	// The rejected repeat stored nothing: one message, one queued send.
	var messages, queued int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM messages`).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM outbox`).Scan(&queued); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if messages != 1 || queued != 1 {
		t.Fatalf("stored messages/queued = %d/%d, want 1/1", messages, queued)
	}

	// A different client id is a new send, and an empty one is never deduped.
	send.ClientID = "notice-8"
	if _, err := db.Sends().CommitSend(ctx, send); err != nil {
		t.Fatalf("second client id: %v", err)
	}
	send.ClientID = ""
	if _, err := db.Sends().CommitSend(ctx, send); err != nil {
		t.Fatalf("empty client id: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&queued); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if queued != 3 {
		t.Fatalf("queued = %d, want 3", queued)
	}
}

func TestCommitSendKeepsTheExistingContactAndChat(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	contact, _, err := db.Peers().RecordInbound(ctx, "support", waKey("5215599999999"), "Ana", t0)
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	chat, err := db.Chats().Ensure(ctx, "support", "support-bot",
		waTarget("5215599999999"), contact.ID, t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := db.Sends().CommitSend(ctx, botflota.SendRecord{
		Scope: "support", Bot: "support-bot",
		Target:    waTarget("5215599999999"),
		Recipient: waPeerPtr("5215599999999"),
		Content:   botflota.TextContent{Body: "hola"}, At: t1,
	}); err != nil {
		t.Fatalf("CommitSend: %v", err)
	}
	got, err := db.Peers().Get(ctx, "support", waKey("5215599999999"))
	if err != nil {
		t.Fatalf("contact: %v", err)
	}
	// The send neither renames nor moves the identity's message times.
	if got.ID != contact.ID || got.DisplayName != "Ana" || !got.LastSeen.Equal(t0) {
		t.Fatalf("contact = %#v, want the kept identity with last_seen %v", got, t0)
	}
	again, err := db.Chats().ForTarget(ctx, "support", "support-bot",
		waTarget("5215599999999"))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if again.ID != chat.ID || !again.UpdatedAt.Equal(t1) {
		t.Fatalf("chat = %#v, want id %d at %v", again, chat.ID, t1)
	}
}
