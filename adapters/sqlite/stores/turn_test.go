package stores_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestCommitTurnLandsEveryWriteTogether(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	session := testSession(chat.ID)
	inbound := &botflota.Message{
		ChatID: chat.ID, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "hola"}, At: t0,
		ProviderMessageID: "in-1", Ingest: botflota.IngestLive,
	}
	outbound := &botflota.Message{
		ChatID: chat.ID, Direction: botflota.Outbound,
		Content: botflota.TextContent{Body: "hola!"}, At: t1,
		Ingest: botflota.IngestLive,
	}
	guards := map[string]botflota.GuardState{
		"escalate": {Cooldown: &botflota.CooldownState{LastRun: t1}},
	}
	err := db.Turns().CommitTurn(ctx, botflota.TurnRecord{
		Scope: "support", Bot: "support-bot",
		Session: session, ChatID: chat.ID, At: t1,
		Inbound: inbound,
		Outbound: []botflota.OutboundRecord{{
			Message: outbound, Recipient: "5215500000000",
		}},
		Guards: guards,
	})
	if err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}

	stored, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if stored.ID == 0 || stored.Node != "purchase" {
		t.Fatalf("session = %#v, want the saved one", stored)
	}
	if inbound.SessionID != stored.ID || outbound.SessionID != stored.ID {
		t.Fatalf("message sessions = %d and %d, want %d",
			inbound.SessionID, outbound.SessionID, stored.ID)
	}
	if inbound.ID == 0 || outbound.ID == 0 {
		t.Fatalf("message ids = %d and %d, want both stored", inbound.ID, outbound.ID)
	}
	// The reply is queued with the turn that stored it (ADR 0011).
	depth, err := db.Outbox().Depth(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Depth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("outbox depth = %d, want the queued reply", depth)
	}
	messages, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want the inbound and the reply", len(messages))
	}
	chats, err := db.Chats().Recent(ctx, "support", "support-bot", 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(chats) != 1 || chats[0].MessageCount != 1 {
		t.Fatalf("chat = %#v, want one recorded message", chats)
	}
	state := chats[0].Guards["escalate"]
	if state.Cooldown == nil || !state.Cooldown.LastRun.Equal(t1) {
		t.Fatalf("guards = %#v, want the turn's cooldown", chats[0].Guards)
	}
}

// TestCommitTurnLeavesNothingBehind proves the transaction: a reply that
// fails inside the turn rolls back the session it advanced, the inbound it
// already stored, and the chat activity it counted.
func TestCommitTurnLeavesNothingBehindWhenOneWriteFails(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)

	first := testSession(chat.ID)
	err := db.Turns().CommitTurn(ctx, botflota.TurnRecord{
		Scope: "support", Bot: "support-bot",
		Session: first, ChatID: chat.ID, At: t0,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "first"}, At: t0,
			ProviderMessageID: "in-1", Ingest: botflota.IngestLive,
		},
	})
	if err != nil {
		t.Fatalf("first CommitTurn: %v", err)
	}

	// The second reply is empty, so Append fails after the session saved,
	// the inbound landed, and the first reply was queued.
	advanced := testSession(chat.ID)
	advanced.ID = first.ID
	advanced.Node = "menu"
	err = db.Turns().CommitTurn(ctx, botflota.TurnRecord{
		Scope: "support", Bot: "support-bot",
		Session: advanced, ChatID: chat.ID, At: t2,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "again"}, At: t2,
			ProviderMessageID: "in-2", Ingest: botflota.IngestLive,
		},
		Outbound: []botflota.OutboundRecord{
			{
				Message: &botflota.Message{
					ChatID: chat.ID, Direction: botflota.Outbound,
					Content: botflota.TextContent{Body: "kept?"}, At: t2,
					Ingest: botflota.IngestLive,
				},
				Recipient: "5215500000000",
			},
			{Recipient: "5215500000000"},
		},
		Guards: map[string]botflota.GuardState{
			"escalate": {Cooldown: &botflota.CooldownState{LastRun: t2}},
		},
	})
	if err == nil {
		t.Fatal("an empty reply must fail the turn")
	}

	stored, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if stored.Node != "purchase" {
		t.Fatalf("session node = %q, want the node before the failed turn", stored.Node)
	}
	messages, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(messages) != 1 || botflota.ContentText(messages[0].Content) != "first" {
		t.Fatalf("messages = %#v, want only the first turn's", messages)
	}
	chats, err := db.Chats().Recent(ctx, "support", "support-bot", 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(chats) != 1 || chats[0].MessageCount != 1 {
		t.Fatalf("chat = %#v, want the count before the failed turn", chats)
	}
	if len(chats[0].Guards) != 0 {
		t.Fatalf("guards = %#v, want none written", chats[0].Guards)
	}
	// The reply queued by the failed turn rolled back with it.
	depth, err := db.Outbox().Depth(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Depth: %v", err)
	}
	if depth != 0 {
		t.Fatalf("outbox depth = %d, want the queued reply rolled back", depth)
	}
}
