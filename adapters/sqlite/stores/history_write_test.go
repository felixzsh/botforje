package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

var _ = sqlite.Open

// seedHistoryFiles a bot of one scope with a chat, a session, messages, a queued
// reply, and a contact, so an operation has something to remove or move.
func seedHistory(t *testing.T, db *sqlite.DB, bot string) *botflota.Chat {
	t.Helper()
	ctx := context.Background()
	sender := ensureSender(t, db)
	chat, err := db.Chats().Ensure(ctx, "support", bot, sender.ID, t0)
	if err != nil {
		t.Fatalf("Ensure chat: %v", err)
	}
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	message := &botflota.Message{
		ChatID: chat.ID, SessionID: session.ID, Direction: botflota.Outbound,
		Content: botflota.TextContent{Body: "hello"}, At: t1,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append message: %v", err)
	}
	// A queued reply is written by the turn transaction in production; here the
	// row is written directly, because a store test seeds state rather than runs
	// a turn.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO outbox (scope, bot, message_id, recipient, state, created_at,
			updated_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
		"support", bot, message.ID, "5215500000000",
		t0.UTC().Format(time.RFC3339Nano), t0.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.Contacts().Put(ctx, &botflota.Contact{
		Scope: "support", Bot: bot, Phone: "5215500000000", Name: "Ana",
		UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put contact: %v", err)
	}
	return chat
}

func TestDeleteBotRemovesItsHistoryAndKeepsTheScope(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	seedHistory(t, db, "support-bot")
	other := seedHistory(t, db, "other-bot")

	counts, err := db.History().DeleteBot(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("DeleteBot: %v", err)
	}
	if counts.Chats != 1 || counts.Messages != 1 || counts.Sessions != 1 ||
		counts.Contacts != 1 {
		t.Fatalf("counts = %+v, want one of each", counts)
	}

	// The deleted bot keeps nothing; the other bot and the scope's sender do.
	got := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "support-bot", got); err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(got.chats)+len(got.messages)+len(got.sessions)+len(got.contacts) != 0 {
		t.Fatalf("deleted bot still holds history: %+v", got)
	}
	kept := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "other-bot", kept); err != nil {
		t.Fatalf("ReadHistory other: %v", err)
	}
	if len(kept.chats) != 1 || len(kept.messages) != 1 {
		t.Fatalf("the other bot lost history: %+v", kept)
	}
	// The scope's sender is shared by its bots and must survive.
	if _, err := db.Senders().Get(ctx, "support", "5215500000000"); err != nil {
		t.Fatalf("the scope's sender was removed: %v", err)
	}
	// The queued reply went with its message.
	if depth, err := db.Outbox().Depth(ctx, "support", "support-bot"); err != nil || depth != 0 {
		t.Fatalf("outbox depth = %d, %v; want empty", depth, err)
	}
	// A repeat removes nothing and says so.
	again, err := db.History().DeleteBot(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("second DeleteBot: %v", err)
	}
	if again != (botflota.HistoryCounts{}) {
		t.Fatalf("second counts = %+v, want zeros", again)
	}
	_ = other
}

func TestMoveBotMovesTheChatWhole(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := seedHistory(t, db, "lost-bot")
	// The destination is a fresh bot of the same scope.
	if err := db.Contacts().Put(ctx, &botflota.Contact{
		Scope: "support", Bot: "new-bot", Phone: "5215599999999",
		Name: "Luis", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put destination contact: %v", err)
	}

	counts, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t2)
	if err != nil {
		t.Fatalf("MoveBot: %v", err)
	}
	if counts.Chats != 1 || counts.Messages != 1 || counts.Sessions != 1 {
		t.Fatalf("counts = %+v, want the source's history", counts)
	}

	moved := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "new-bot", moved); err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(moved.chats) != 1 || moved.chats[0].ID != chat.ID {
		t.Fatalf("chats = %+v, want the same chat row", moved.chats)
	}
	// The chat keeps what made it itself, so the replacement resumes it.
	if !moved.chats[0].StartedAt.Equal(t0) || moved.chats[0].MessageCount != 0 {
		t.Fatalf("chat = %+v, want its start and activity kept", moved.chats[0])
	}
	if len(moved.sessions) != 1 || len(moved.messages) != 1 {
		t.Fatalf("sessions/messages = %+v / %+v, want them moved",
			moved.sessions, moved.messages)
	}
	// The run that was live is over, so the destination does not look like it
	// resumes another bot's graph.
	if !moved.sessions[0].Expired(t2) {
		t.Fatalf("session expires at %v, want it expired at %v",
			moved.sessions[0].ExpiresAt, t2)
	}
	// Contacts arrive, and the destination's own row for another phone stays.
	if len(moved.contacts) != 2 {
		t.Fatalf("contacts = %+v, want both", moved.contacts)
	}
	// The source is empty and its queue is gone, never carried over.
	source := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "lost-bot", source); err != nil {
		t.Fatalf("ReadHistory source: %v", err)
	}
	if len(source.chats) != 0 || len(source.contacts) != 0 {
		t.Fatalf("the source kept history: %+v", source)
	}
	if depth, err := db.Outbox().Depth(ctx, "support", "lost-bot"); err != nil || depth != 0 {
		t.Fatalf("source outbox depth = %d, %v; want empty", depth, err)
	}
	// A repeat finds nothing left to move.
	again, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t2)
	if err != nil {
		t.Fatalf("second MoveBot: %v", err)
	}
	if again != (botflota.HistoryCounts{}) {
		t.Fatalf("second counts = %+v, want zeros", again)
	}
}

func TestMoveBotRefusesADestinationThatHoldsHistory(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	seedHistory(t, db, "lost-bot")
	seedHistory(t, db, "busy-bot")

	_, err := db.History().MoveBot(ctx, "support", "lost-bot", "busy-bot", t2)
	if !errors.Is(err, botflota.ErrDestinationNotEmpty) {
		t.Fatalf("error = %v, want ErrDestinationNotEmpty", err)
	}
	// Nothing moved: the source still holds everything.
	source := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "lost-bot", source); err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(source.chats) != 1 || len(source.messages) != 1 {
		t.Fatalf("the refused move changed the source: %+v", source)
	}
}
