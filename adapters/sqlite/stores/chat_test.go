package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestChatEnsureIsGetOrCreate(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender := ensureSender(t, db)
	first := ensureChat(t, db)
	if first.ID == 0 {
		t.Fatal("Ensure did not set an id")
	}
	again, err := db.Chats().Ensure(ctx, "support", "support-bot", sender.ID, t2)
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("Ensure created a second chat: %d != %d", again.ID, first.ID)
	}
	if !again.StartedAt.Equal(t0) {
		t.Fatalf("started_at = %v, want %v", again.StartedAt, t0)
	}
	if err := db.Chats().RecordMessage(ctx, first.ID, t1); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	got, err := db.Chats().Ensure(ctx, "support", "support-bot", sender.ID, t2)
	if err != nil {
		t.Fatalf("Ensure after record: %v", err)
	}
	if !got.UpdatedAt.Equal(t1) {
		t.Fatalf("updated_at = %v, want %v", got.UpdatedAt, t1)
	}
	if got.MessageCount != 1 {
		t.Fatalf("message_count = %d, want 1", got.MessageCount)
	}
}

func TestChatGuardsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	if len(chat.Guards) != 0 {
		t.Fatalf("new chat guards = %#v, want empty", chat.Guards)
	}
	want := map[string]botflota.GuardState{
		"escalate": {
			Cooldown:  &botflota.CooldownState{LastRun: t1},
			RateLimit: &botflota.RateLimitState{WindowStart: t0, Count: 2},
		},
	}
	if err := db.Chats().SaveGuards(ctx, chat.ID, want); err != nil {
		t.Fatalf("SaveGuards: %v", err)
	}
	got, err := db.Chats().Ensure(ctx, "support", "support-bot", ensureSender(t, db).ID, t2)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !reflect.DeepEqual(got.Guards, want) {
		t.Fatalf("guards = %#v, want %#v", got.Guards, want)
	}
}

func TestChatGetAndList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender := ensureSender(t, db)
	chat := ensureChat(t, db)
	got, err := db.Chats().ForSender(ctx, "support", "support-bot", sender.ID)
	if err != nil {
		t.Fatalf("ForSender: %v", err)
	}
	if got.ID != chat.ID {
		t.Fatalf("ForSender = %d, want %d", got.ID, chat.ID)
	}
	if _, err := db.Chats().ForSender(ctx, "support", "other", sender.ID); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("ForSender missing = %v, want ErrNotFound", err)
	}
	chats, err := db.Chats().Recent(ctx, "support", "", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(chats) != 1 || chats[0].ID != chat.ID {
		t.Fatalf("Recent = %#v", chats)
	}
	if chats[0].Phone != "5215500000000" || chats[0].DisplayName != "Ana" {
		t.Fatalf("Recent sender profile = %#v", chats[0])
	}
	if byBot, err := db.Chats().Recent(ctx, "support", "support-bot", 10); err != nil ||
		len(byBot) != 1 {
		t.Fatalf("Recent by bot = %#v, %v", byBot, err)
	}
	if other, err := db.Chats().Recent(ctx, "support", "other", 10); err != nil ||
		len(other) != 0 {
		t.Fatalf("Recent other = %#v, %v", other, err)
	}
	// A zero limit means every chat, which `chat sync --all` walks.
	if every, err := db.Chats().Recent(ctx, "support", "", 0); err != nil ||
		len(every) != 1 || every[0].ID != chat.ID {
		t.Fatalf("Recent all = %#v, %v", every, err)
	}
}

// TestChatRecentCounts covers the stored message counts the read API shows:
// the total split by ingest kind, independent of the live inbound count.
func TestChatRecentCounts(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	ingests := []botflota.Ingest{
		botflota.IngestSync, botflota.IngestSync, botflota.IngestLive,
	}
	for i, ingest := range ingests {
		message := &botflota.Message{
			ChatID: chat.ID, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "m"},
			At:      t0, ProviderMessageID: "M" + string(rune('A'+i)),
			Ingest: ingest,
		}
		if err := db.Messages().Append(ctx, message); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	chats, err := db.Chats().Recent(ctx, "support", "support-bot", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(chats) != 1 || chats[0].Messages != 3 || chats[0].Sync != 2 ||
		chats[0].Live != 1 {
		t.Fatalf("counts = %#v, want 3 stored (2 sync, 1 live)", chats)
	}
}

// TestChatNoteHistoryWidensActivity covers the imported range: started_at moves
// back and updated_at forward, and neither moves the other way.
func TestChatNoteHistoryWidensActivity(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	from := t0.Add(-48 * time.Hour)
	to := t1
	if err := db.Chats().NoteHistory(ctx, chat.ID, from, to); err != nil {
		t.Fatalf("NoteHistory: %v", err)
	}
	senderID := ensureSender(t, db).ID
	got, err := db.Chats().ForSender(ctx, "support", "support-bot", senderID)
	if err != nil {
		t.Fatalf("ForSender: %v", err)
	}
	if !got.StartedAt.Equal(from) || !got.UpdatedAt.Equal(to) {
		t.Fatalf("activity = %v..%v, want %v..%v", got.StartedAt, got.UpdatedAt, from, to)
	}
	if err := db.Chats().NoteHistory(ctx, chat.ID, t0.Add(-24*time.Hour),
		t0.Add(-12*time.Hour)); err != nil {
		t.Fatalf("NoteHistory narrower: %v", err)
	}
	got, err = db.Chats().ForSender(ctx, "support", "support-bot", senderID)
	if err != nil {
		t.Fatalf("ForSender: %v", err)
	}
	if !got.StartedAt.Equal(from) || !got.UpdatedAt.Equal(to) {
		t.Fatalf("activity moved the wrong way: %v..%v", got.StartedAt, got.UpdatedAt)
	}
}

func TestOneSenderManyChats(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender := ensureSender(t, db)
	alpha, err := db.Chats().Ensure(ctx, "support", "alpha", sender.ID, t0)
	if err != nil {
		t.Fatalf("Ensure alpha: %v", err)
	}
	beta, err := db.Chats().Ensure(ctx, "support", "beta", sender.ID, t0)
	if err != nil {
		t.Fatalf("Ensure beta: %v", err)
	}
	if alpha.ID == beta.ID {
		t.Fatal("two bots must have two chats with the same sender")
	}
}
