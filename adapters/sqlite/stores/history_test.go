package stores_test

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/felixzsh/botflota"
)

// collector is a HistorySink that keeps what it was handed, so a test can check
// the read without a zip in the way.
type collector struct {
	tables      []botflota.HistoryTable
	chats       []botflota.HistoryChat
	sessions    []botflota.Session
	messages    []botflota.Message
	addressBook []botflota.AddressBookEntry
}

func (c *collector) BeginTable(table botflota.HistoryTable) error {
	c.tables = append(c.tables, table)
	return nil
}

func (c *collector) Chat(chat botflota.HistoryChat) error {
	c.chats = append(c.chats, chat)
	return nil
}

func (c *collector) Session(session botflota.Session) error {
	c.sessions = append(c.sessions, session)
	return nil
}

func (c *collector) Message(message botflota.Message) error {
	c.messages = append(c.messages, message)
	return nil
}

func (c *collector) AddressBookEntry(entry botflota.AddressBookEntry) error {
	c.addressBook = append(c.addressBook, entry)
	return nil
}

func TestHistoryReadsOneBotsRowsInStoredOrder(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)

	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	for _, body := range []string{"first", "second"} {
		message := &botflota.Message{
			ChatID: chat.ID, SessionID: session.ID, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: body}, At: t1,
		}
		if err := db.Messages().Append(ctx, message); err != nil {
			t.Fatalf("Append %q: %v", body, err)
		}
	}
	if err := db.AddressBook().Put(ctx, &botflota.AddressBookEntry{
		Scope: "support", Bot: "support-bot", Phone: "5215500000000",
		Name: "Ana", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put address-book entry: %v", err)
	}
	// Another bot of the same scope must stay out of the export.
	if err := db.AddressBook().Put(ctx, &botflota.AddressBookEntry{
		Scope: "support", Bot: "other-bot", Phone: "5215500000001", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put other address-book entry: %v", err)
	}

	got := &collector{}
	if err := db.History().ReadHistory(ctx, "support", "support-bot", got); err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}

	if len(got.chats) != 1 || got.chats[0].ID != chat.ID {
		t.Fatalf("chats = %+v, want the bot's own", got.chats)
	}
	// The contact's identity rides along, so the exported file stands alone.
	if got.chats[0].ContactPhone != "5215500000000" || got.chats[0].ContactName != "Ana" {
		t.Fatalf("chat contact = %q / %q, want the scope's contact",
			got.chats[0].ContactPhone, got.chats[0].ContactName)
	}
	if len(got.sessions) != 1 || got.sessions[0].Node != "purchase" {
		t.Fatalf("sessions = %+v", got.sessions)
	}
	if len(got.messages) != 2 {
		t.Fatalf("messages = %+v, want both", got.messages)
	}
	if got.messages[0].ID >= got.messages[1].ID {
		t.Fatalf("messages are not in stored order: %d then %d",
			got.messages[0].ID, got.messages[1].ID)
	}
	if len(got.addressBook) != 1 || got.addressBook[0].Bot != "support-bot" {
		t.Fatalf("address book = %+v, want only the bot's own", got.addressBook)
	}
	// Every table is announced, in the order a sink writes its files, including
	// the one the bot never filled.
	want := []botflota.HistoryTable{botflota.TableChats, botflota.TableSessions,
		botflota.TableMessages, botflota.TableAddressBook}
	if !slices.Equal(got.tables, want) {
		t.Fatalf("tables = %v, want %v", got.tables, want)
	}
}

func TestHistoryStopsWhenTheSinkRefuses(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	message := &botflota.Message{
		ChatID: chat.ID, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "hola"}, At: t1,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}

	err := db.History().ReadHistory(ctx, "support", "support-bot",
		failOnMessage{err: io.ErrClosedPipe})
	if err != io.ErrClosedPipe {
		t.Fatalf("error = %v, want the sink's refusal", err)
	}
}

type failOnMessage struct{ err error }

func (f failOnMessage) BeginTable(botflota.HistoryTable) error { return nil }
func (f failOnMessage) Chat(botflota.HistoryChat) error        { return nil }
func (f failOnMessage) Session(botflota.Session) error         { return nil }
func (f failOnMessage) Message(botflota.Message) error         { return f.err }
func (f failOnMessage) AddressBookEntry(botflota.AddressBookEntry) error {
	return nil
}
