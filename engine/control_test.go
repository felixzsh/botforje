package engine_test

import (
	"context"
	"testing"

	"github.com/felixzsh/botflota"
)

// TestRecordSuppressedKeepsHistoryWithoutASession covers a chat held by a
// human: the inbound is stored and deduplicated, and no session run appears
// (ADR 0043).
func TestRecordSuppressedKeepsHistoryWithoutASession(t *testing.T) {
	store := newMemStore()
	eng := fixtureEngine(t, store)
	sender := "5215500000000"
	msg := botflota.IncomingMessage{
		Bot: "support-bot", FromPhone: sender, At: t0,
		ProviderMessageID: "M1",
		Content:           botflota.TextContent{Body: "hola"},
	}
	if err := eng.RecordSuppressed(context.Background(), msg); err != nil {
		t.Fatalf("RecordSuppressed: %v", err)
	}
	if _, err := store.sessionFor("support", "support-bot", sender); err == nil {
		t.Fatal("a suppressed message created a session")
	}
	if err := eng.RecordSuppressed(context.Background(), msg); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	stored := 0
	for _, message := range store.messages {
		if message.ProviderMessageID == "M1" {
			stored++
			if message.Direction != botflota.Inbound {
				t.Fatalf("direction = %q, want inbound", message.Direction)
			}
		}
	}
	if stored != 1 {
		t.Fatalf("stored %d copies of M1, want 1", stored)
	}
}

// TestRecordOperatorStoresOutboundWithoutQueueing covers an operator reply
// from another client: it is history with the operator origin, never a queued
// automatic reply (ADR 0043).
func TestRecordOperatorStoresOutboundWithoutQueueing(t *testing.T) {
	store := newMemStore()
	eng := fixtureEngine(t, store)
	err := eng.RecordOperator(context.Background(), botflota.OutboundMessage{
		Bot: "support-bot", RecipientPhone: "5215500000000",
		ProviderMessageID: "OP1", At: t0,
		Content: botflota.TextContent{Body: "te atiendo yo"},
	})
	if err != nil {
		t.Fatalf("RecordOperator: %v", err)
	}
	for _, message := range store.messages {
		if message.ProviderMessageID != "OP1" {
			continue
		}
		if message.Direction != botflota.Outbound ||
			message.Origin != botflota.OriginOperator {
			t.Fatalf("message = %#v", message)
		}
		return
	}
	t.Fatal("operator reply was not stored")
}
