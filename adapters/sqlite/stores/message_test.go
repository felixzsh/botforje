package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestChatAndMessages(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	first := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, SessionID: session.ID, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "hello"}, At: t1,
		ProviderMessageID: "MSG-1", Ingest: botflota.IngestLive,
		Routing: botflota.RoutingTriggerExact,
	}
	// A synced message is honest about its provenance: unknown origin and
	// routing, and no session run.
	second := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "hi there"}, At: t1,
		ProviderMessageID: "MSG-2", Ingest: botflota.IngestSync,
		Origin: botflota.OriginUnknown, Routing: botflota.RoutingUnknown,
	}
	for _, message := range []*botflota.Message{first, second} {
		if err := db.Messages().Append(ctx, message); err != nil {
			t.Fatalf("Append(%q): %v", botflota.ContentText(message.Content), err)
		}
		if message.ID == 0 {
			t.Fatalf("Append(%q) did not set an id", botflota.ContentText(message.Content))
		}
	}

	got, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 || botflota.ContentText(got[0].Content) != "hello" ||
		botflota.ContentText(got[1].Content) != "hi there" {
		t.Fatalf("History = %#v", got)
	}
	if got[0].SessionID != session.ID {
		t.Fatalf("SessionID = %d, want %d", got[0].SessionID, session.ID)
	}
	if got[0].ProviderMessageID != "MSG-1" ||
		got[0].Ingest != botflota.IngestLive ||
		got[0].Routing != botflota.RoutingTriggerExact {
		t.Fatalf("live provenance = %+v", got[0])
	}
	if got[1].ProviderMessageID != "MSG-2" ||
		got[1].Ingest != botflota.IngestSync ||
		got[1].Origin != botflota.OriginUnknown ||
		got[1].Routing != botflota.RoutingUnknown || got[1].SessionID != 0 {
		t.Fatalf("synced provenance = %+v", got[1])
	}

	// A zero cursor keeps the newest messages; a cursor resumes from it.
	last, err := db.Messages().History(ctx, chat.ID, 0, 1)
	if err != nil {
		t.Fatalf("History(1): %v", err)
	}
	if len(last) != 1 || botflota.ContentText(last[0].Content) != "hi there" {
		t.Fatalf("History(1) = %#v, want the newest", last)
	}
	newer, err := db.Messages().History(ctx, chat.ID, got[0].ID, 10)
	if err != nil {
		t.Fatalf("History(after): %v", err)
	}
	if len(newer) != 1 || newer[0].ID != got[1].ID {
		t.Fatalf("History(after) = %#v, want only the newer message", newer)
	}
}

func TestMessageWithoutSession(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	message := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "no session"}, At: t1,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != 0 {
		t.Fatalf("SessionID = %#v, want 0", got)
	}
}

// TestMessageContentRoundTrip keeps a structured payload: a poll's options
// survive the store, not only its visible text (ADR 0036).
func TestMessageContentRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	message := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
		Content: botflota.PollContent{
			Question: "Lunch?", Options: []string{"yes", "no"}, Selectable: 1,
		},
		At: t1, ProviderMessageID: "POLL-1", Ingest: botflota.IngestSync,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("History = %#v, want one message", got)
	}
	poll, ok := got[0].Content.(botflota.PollContent)
	if !ok || len(poll.Options) != 2 || poll.Selectable != 1 {
		t.Fatalf("content = %#v, want the structured poll", got[0].Content)
	}
}

// TestMessageProviderIDIsUnique covers the dedup key of a history import: a
// reported channel id cannot repeat in a chat, while rows without one may.
func TestMessageProviderIDIsUnique(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	message := func(id string) *botflota.Message {
		return &botflota.Message{
			ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hola"}, At: t1,
			ProviderMessageID: id, Ingest: botflota.IngestLive,
		}
	}
	if err := db.Messages().Append(ctx, message("A1")); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := db.Messages().Append(ctx, message("A1")); !errors.Is(err, botflota.ErrDuplicate) {
		t.Fatalf("repeated provider id error = %v, want ErrDuplicate", err)
	}
	if err := db.Messages().Append(ctx, message("")); err != nil {
		t.Fatalf("append without provider id: %v", err)
	}
	if err := db.Messages().Append(ctx, message("")); err != nil {
		t.Fatalf("second append without provider id: %v", err)
	}
}

// TestMessageOldest picks the oldest anchored message: a row without a channel
// id and a call row cannot anchor an on-demand request, so neither is chosen.
func TestMessageOldest(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	if _, err := db.Messages().Oldest(ctx, chat.ID); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("Oldest on an empty chat = %v, want ErrNotFound", err)
	}
	for _, spec := range []struct {
		at      time.Time
		id      string
		content botflota.MessageContent
	}{
		{t2, "C", botflota.TextContent{Body: "newest"}},
		{t0, "A", botflota.TextContent{Body: "oldest"}},
		{t1, "", botflota.TextContent{Body: "no anchor"}},
		{t0.Add(-time.Hour), "CALL-1", botflota.CallContent{Label: "Missed call"}},
	} {
		message := &botflota.Message{
			ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
			Content: spec.content, At: spec.at, ProviderMessageID: spec.id,
			Ingest: botflota.IngestSync,
		}
		if err := db.Messages().Append(ctx, message); err != nil {
			t.Fatalf("Append(%s): %v", spec.id, err)
		}
	}
	oldest, err := db.Messages().Oldest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Oldest: %v", err)
	}
	if botflota.ContentText(oldest.Content) != "oldest" || oldest.ProviderMessageID != "A" {
		t.Fatalf("oldest = %#v, want the oldest anchored message", oldest)
	}
}
