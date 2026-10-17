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
	contact := ensureContact(t, db)
	chat, err := db.Chats().Ensure(ctx, "support", bot, waChatTarget, contact.ID, t0)
	if err != nil {
		t.Fatalf("Ensure chat: %v", err)
	}
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	message := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, SessionID: session.ID,
		Direction: botflota.Outbound,
		Content:   botflota.TextContent{Body: "hello"}, At: t1,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append message: %v", err)
	}
	// A queued reply is written by the turn transaction in production; here the
	// row is written directly, because a store test seeds state rather than runs
	// a turn.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO outbox (scope, bot, message_id, target_provider, target_kind,
			target_thread, target_topic, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, '', 'pending', ?, ?)`,
		"support", bot, message.ID, "whatsapp", "direct", "5215500000000",
		t0.UTC().Format(time.RFC3339Nano), t0.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.Directory().Put(ctx, &botflota.DirectoryEntry{
		Scope: "support", Bot: bot, PeerKey: waKey("5215500000000"),
		SourceIdentity: waKey("5215500000000"), DisplayName: "Ana", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put address-book entry: %v", err)
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
	if counts.Chats != 1 || counts.Messages != 1 || counts.GraphRuns != 1 ||
		counts.Directory != 1 {
		t.Fatalf("counts = %+v, want one of each", counts)
	}

	// The deleted bot keeps nothing; the other bot and the scope's contact do.
	got := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "support-bot", got); err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	if len(got.chats)+len(got.messages)+len(got.sessions)+len(got.addressBook) != 0 {
		t.Fatalf("deleted bot still holds history: %+v", got)
	}
	kept := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "other-bot", kept); err != nil {
		t.Fatalf("ReadHistory other: %v", err)
	}
	if len(kept.chats) != 1 || len(kept.messages) != 1 {
		t.Fatalf("the other bot lost history: %+v", kept)
	}
	// The scope's contact identity is shared by its bots and must survive.
	if _, err := db.Peers().Get(ctx, "support", waKey("5215500000000")); err != nil {
		t.Fatalf("the scope's contact was removed: %v", err)
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
	if err := db.Directory().Put(ctx, &botflota.DirectoryEntry{
		Scope: "support", Bot: "new-bot", PeerKey: waKey("5215599999999"),
		SourceIdentity: waKey("5215599999999"), DisplayName: "Luis", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put destination address-book entry: %v", err)
	}

	counts, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t2)
	if err != nil {
		t.Fatalf("MoveBot: %v", err)
	}
	if counts.Chats != 1 || counts.Messages != 1 || counts.GraphRuns != 1 ||
		counts.Directory != 1 {
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
	// Address-book entries arrive, and the destination's own row for another
	// phone stays.
	if len(moved.addressBook) != 2 {
		t.Fatalf("address book = %+v, want both", moved.addressBook)
	}
	// The source is empty and its queue is gone, never carried over.
	source := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "lost-bot", source); err != nil {
		t.Fatalf("ReadHistory source: %v", err)
	}
	if len(source.chats) != 0 || len(source.addressBook) != 0 {
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

// TestMoveBotMovesAddressBookWithoutChats covers a source that holds only
// imported address-book entries; it is not an empty migration.
func TestMoveBotMovesAddressBookWithoutChats(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, entry := range []*botflota.DirectoryEntry{
		{Scope: "support", Bot: "lost-bot", PeerKey: waKey("5215500000001"),
			SourceIdentity: waKey("5215500000001"), DisplayName: "Ana", UpdatedAt: t0},
		{Scope: "support", Bot: "new-bot", PeerKey: waKey("5215500000002"),
			SourceIdentity: waKey("5215500000002"), DisplayName: "Beto", UpdatedAt: t1},
	} {
		if err := db.Directory().Put(ctx, entry); err != nil {
			t.Fatalf("Put %s: %v", entry.Bot, err)
		}
	}
	counts, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t2)
	if err != nil {
		t.Fatalf("MoveBot: %v", err)
	}
	if counts != (botflota.HistoryCounts{Directory: 1}) {
		t.Fatalf("counts = %+v, want one moved address-book entry", counts)
	}
	entries, err := db.Directory().List(ctx, "support", "new-bot", 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("destination entries = %+v, err %v; want both", entries, err)
	}
	source, err := db.Directory().List(ctx, "support", "lost-bot", 0)
	if err != nil || len(source) != 0 {
		t.Fatalf("source entries = %+v, err %v; want empty", source, err)
	}
}

func TestMoveBotMovesMessageOwnership(t *testing.T) {
	for _, target := range []botflota.ChatTarget{waChatTarget, waTarget("alternate")} {
		t.Run(target.Thread, func(t *testing.T) {
			ctx := context.Background()
			db := open(t, filepath.Join(t.TempDir(), "app.db"))
			chat := seedHistory(t, db, "lost-bot")
			message := &botflota.Message{
				ChatID: chat.ID, Target: target, Direction: botflota.Inbound,
				Content: botflota.TextContent{Body: "original"}, At: t1,
				ProviderMessageID: "same-id", Ingest: botflota.IngestSync,
			}
			if err := db.Messages().Append(ctx, message); err != nil {
				t.Fatalf("Append: %v", err)
			}
			if _, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t2); err != nil {
				t.Fatalf("MoveBot: %v", err)
			}
			var mismatched int
			if err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM messages m JOIN chats c ON c.id = m.chat_id
				 WHERE m.scope <> c.scope OR m.bot <> c.bot`).Scan(&mismatched); err != nil {
				t.Fatalf("read ownership: %v", err)
			}
			if mismatched != 0 {
				t.Fatalf("mismatched message owners = %d", mismatched)
			}
			for _, owner := range []struct {
				bot  string
				want bool
			}{{"lost-bot", false}, {"new-bot", true}} {
				got, err := db.Messages().HasProviderID(ctx, "support", owner.bot, "", target, "same-id")
				if err != nil || got != owner.want {
					t.Fatalf("HasProviderID(%s) = %v, %v; want %v",
						owner.bot, got, err, owner.want)
				}
			}
			stored, err := db.Messages().History(ctx, chat.ID, 0, 10)
			if err != nil {
				t.Fatalf("History: %v", err)
			}
			got := stored[len(stored)-1]
			if got.Target != target || got.ProviderMessageID != "same-id" ||
				got.Ingest != botflota.IngestSync || !got.At.Equal(t1) {
				t.Fatalf("migration changed message data: %+v", got)
			}
			if err := db.Messages().Append(ctx, message); !errors.Is(err, botflota.ErrDuplicate) {
				t.Fatalf("destination duplicate = %v, want ErrDuplicate", err)
			}
			fresh, err := db.Chats().Ensure(ctx, "support", "lost-bot", chat.Target, 0, t2)
			if err != nil {
				t.Fatalf("recreate source: %v", err)
			}
			message.ChatID = fresh.ID
			message.ID = 0
			if err := db.Messages().Append(ctx, message); err != nil {
				t.Fatalf("append to emptied source: %v", err)
			}
		})
	}
}

func TestMoveBotRollsBackMessageOwnership(t *testing.T) {
	for _, failTable := range []string{"chats", "directory_entries"} {
		t.Run(failTable, func(t *testing.T) {
			ctx := context.Background()
			db := open(t, filepath.Join(t.TempDir(), "app.db"))
			chat := seedHistory(t, db, "lost-bot")
			statement := `CREATE TRIGGER reject_move BEFORE UPDATE ON chats
			 BEGIN SELECT RAISE(ABORT, 'rejected move'); END`
			if failTable == "directory_entries" {
				statement = `CREATE TRIGGER reject_move BEFORE INSERT ON directory_entries
				 BEGIN SELECT RAISE(ABORT, 'rejected move'); END`
			}
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("create trigger: %v", err)
			}
			if _, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t1); err == nil {
				t.Fatal("MoveBot succeeded despite trigger")
			}
			var messageBot, chatBot string
			if err := db.QueryRowContext(ctx,
				`SELECT m.bot, c.bot FROM messages m JOIN chats c ON c.id = m.chat_id
				 WHERE c.id = ?`, chat.ID).Scan(&messageBot, &chatBot); err != nil {
				t.Fatalf("read ownership: %v", err)
			}
			if messageBot != "lost-bot" || chatBot != "lost-bot" {
				t.Fatalf("owners after rollback = %q/%q", messageBot, chatBot)
			}
			if depth, err := db.Outbox().Depth(ctx, "support", "lost-bot"); err != nil || depth != 1 {
				t.Fatalf("queue after rollback = %d, %v; want 1", depth, err)
			}
			session, err := db.Sessions().Latest(ctx, chat.ID)
			if err != nil || session.Expired(t1) {
				t.Fatalf("session after rollback = %+v, %v", session, err)
			}
		})
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
